# mqtt2discord

A simple Go app that connects to a MQTT server, subscribes to various topics, and pushes those messages into a Discord webhook.

## Usage

Create a `config.json` with a list of topics and their destination webhooks, like below:

```json
{
    "mqtt_server": {
        "address": "tcp://localhost:1883",
        "meta_webhook": "https://discord.com/api/webhooks/1/your_key_here"
    },
    "subscriptions": [
        {
            "topic": "test/#",
            "webhook": "https://discord.com/api/webhooks/2/your_key_here"
        },
        {
            "topic": "test2/#",
            "webhook": "https://discord.com/api/webhooks/2/your_key_here"
        },
    ]
}
```

Build, and run!
