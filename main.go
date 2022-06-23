package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Subscription struct {
	Webhook string `json:"webhook"`
	Topic   string `json:"topic"`
}

type MQTTServer struct {
	Address  string `json:"address"`
	Username string `json:"username"`
	Password string `json:"password"`
	Prefix   string `json:"prefix"`
	ClientID string `json:"client_id"`
	Webhook  string `json:"meta_webhook"`
}

type Config struct {
	MQTT          MQTTServer     `json:"mqtt_server"`
	Subscriptions []Subscription `json:"subscriptions"`
}

type QueuedMessage struct {
	WebhookUrl string
	Content    DiscordWebhookMessage
}

type DiscordWebhookMessage struct {
	Content string `json:"content"`
}

var config Config

func NewDiscordWebhookMessage(topic string, payload string) DiscordWebhookMessage {
	dateString := time.Now().Format("15:04:05")
	return DiscordWebhookMessage{
		Content: fmt.Sprintf("%s %s: `%s`", dateString, topic, payload),
	}
}

func (msg DiscordWebhookMessage) serialize() []byte {
	jsonMsg, _ := json.Marshal(msg)
	return []byte(jsonMsg)
}

func callbackSub(sub Subscription, messages chan<- QueuedMessage) func(client mqtt.Client, msg mqtt.Message) {
	return func(client mqtt.Client, msg mqtt.Message) {
		webhook := sub.Webhook
		messages <- QueuedMessage{
			webhook,
			NewDiscordWebhookMessage(msg.Topic(), string(msg.Payload())),
		}
	}
}

func main() {
	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		fmt.Println()
		fmt.Println(sig)
		done <- true
	}()

	jsonFile, err := os.Open("mqtt2discord.json")
	if err != nil {
		panic(err)
	}
	byt, _ := ioutil.ReadAll(jsonFile)
	jsonFile.Close()

	// var config map[string]interface{}
	if err := json.Unmarshal(byt, &config); err != nil {
		panic(err)
	}
	fmt.Println(config)

	opts := mqtt.NewClientOptions()
	opts.AddBroker(config.MQTT.Address)

	//TODO: append / to prefix if not already
	if config.MQTT.Prefix == "" {
		config.MQTT.Prefix = "mqtt2discord/"
	}

	if config.MQTT.ClientID != "" {
		opts.SetClientID(config.MQTT.ClientID)
	} else {
		opts.SetClientID("mqtt2discord")
	}

	if config.MQTT.Username != "" {
		opts.SetUsername(config.MQTT.Username)
		opts.SetPassword(config.MQTT.Password)
	}

	opts.SetDefaultPublishHandler(func(c mqtt.Client, m mqtt.Message) {
		log.Printf("WARN received unsolicited MQTT message on %s: %s\n", m.Topic(), m.Payload())
	})
	opts.OnConnect = func(c mqtt.Client) {
		log.Println("MQTT connected")
	}
	opts.OnConnectionLost = func(c mqtt.Client, err error) {
		log.Fatalf("MQTT connectionLost: %e", err)
	}

	client := mqtt.NewClient(opts)
	fmt.Printf("Connecting to %s\n", config.MQTT.Address)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Fatal(token.Error())
	}
	fmt.Println("Connected")
	client.Publish(fmt.Sprintf("%sstatus", config.MQTT.Prefix), 0, false, "online")

	defer client.Disconnect(2000)
	defer log.Println("Disconnecting MQTT")

	messages := make(chan QueuedMessage, 64)

	go func(queue <-chan QueuedMessage) {

		client := &http.Client{}

		for msg := range queue {
			for {
				req, err := http.NewRequest("POST", msg.WebhookUrl, bytes.NewReader(msg.Content.serialize()))
				if err != nil {
					log.Fatal(err)
				}

				req.Header.Add("Content-Type", "application/json")

				resp, err := client.Do(req)
				defer resp.Body.Close()

				if err != nil {
					log.Fatal(err)
				}

				if resp.StatusCode == 204 {
					break
				} else if resp.StatusCode == 429 {
					resetafter, err := strconv.ParseFloat(resp.Header.Get("X-RateLimit-Reset-After"), 32)
					if err != nil {
						resetafter = 2.0
					}
					if resetafter == 0.0 {
						resetafter = 3.0
					}
					sleepfor := time.Duration(resetafter) * time.Second
					log.Printf("%s (%d): sleeping for %s", msg.WebhookUrl, resp.StatusCode, sleepfor)
					time.Sleep(sleepfor)
				} else {
					log.Fatalf("%s (%d): \n\nPOSTdata was: %s", msg.WebhookUrl, resp.StatusCode, msg.Content.serialize())
				}
			}
		}
	}(messages)

	for i := range config.Subscriptions {
		fmt.Printf("Subscribing to topic: %s\n", config.Subscriptions[i].Topic)
		token := client.Subscribe(config.Subscriptions[i].Topic, 1, callbackSub(config.Subscriptions[i], messages))
		token.Wait()
	}

	<-done
	fmt.Println("SIGINT/SIGTERM received, exiting")

	messages <- QueuedMessage{
		config.MQTT.Webhook,
		NewDiscordWebhookMessage("meta", "SIGINT/SIGTERM"),
	}
	close(messages)
}
