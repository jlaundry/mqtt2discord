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

const (
	mqtt_client_id = "mqtt2discord"
)

type Subscription struct {
	Webhook string `json:"webhook"`
	Topic   string `json:"topic"`
}

type Server struct {
	Address  string `json:"address"`
	Username string `json:"username"`
	Password string `json:"password"`
	Webhook  string `json:"meta_webhook"`
}

type Config struct {
	Server        Server         `json:"mqtt_server"`
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

var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	fmt.Println("connectHandler Connected")
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	log.Fatalf("connectLostHandler Connect lost: %v", err)
}

var messagePubHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	fmt.Printf("INFO: Default message handler received message: %s from topic: %s\n", msg.Payload(), msg.Topic())
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
	opts.AddBroker(config.Server.Address)
	opts.SetClientID(mqtt_client_id)
	// opts.SetUsername("emqx")
	// opts.SetPassword("public")
	opts.SetDefaultPublishHandler(messagePubHandler)
	opts.OnConnect = connectHandler
	opts.OnConnectionLost = connectLostHandler

	client := mqtt.NewClient(opts)
	fmt.Printf("Connecting to %s\n", config.Server.Address)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

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
		config.Server.Webhook,
		NewDiscordWebhookMessage("meta", "SIGINT/SIGTERM"),
	}
	close(messages)

	client.Disconnect(500)
	fmt.Println("Finished")
}
