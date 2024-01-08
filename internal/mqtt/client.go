package mqtt

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/jgulick48/lxp-bridge-statsd/internal/metrics"

	"github.com/jgulick48/lxp-bridge-statsd/internal/models"
)

const metricPrefix = "lxp"

type Client interface {
	Close()
	IsEnabled() bool
	Connect()
}

func NewClient(config models.MQTTConfiguration, debug bool) Client {
	if config.Host != "" {
		client := client{
			config:   config,
			done:     make(chan bool),
			messages: make(chan mqtt.Message),
			debug:    debug,
		}
		return &client
	}
	return &client{config: config}
}

type client struct {
	config     models.MQTTConfiguration
	done       chan bool
	mqttClient mqtt.Client
	messages   chan mqtt.Message
	debug      bool
	values     map[string]map[string]float64
}

func (c *client) Close() {
	c.done <- true
}

func (c *client) IsEnabled() bool {
	return c.config.Host != ""
}

func (c *client) Connect() {
	go func() {
		for message := range c.messages {
			c.ProcessData(message.Topic(), message.Payload())
		}
	}()
	log.Printf("Connecting to %s", fmt.Sprintf("tcp://%s:%d", c.config.Host, c.config.Port))
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", c.config.Host, c.config.Port))
	opts.SetClientID("go_mqtt_client")
	opts.SetDefaultPublishHandler(c.messagePubHandler)
	opts.OnConnect = connectHandler
	opts.OnConnectionLost = c.connectLostHandler
	c.mqttClient = mqtt.NewClient(opts)
	if token := c.mqttClient.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}
	c.sub()
	defer c.mqttClient.Disconnect(250)
	c.keepAlive()
}

func (c *client) keepAlive() {
	ticker := time.NewTicker(10 * time.Second)
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			for _, deviceID := range c.config.DeviceIDs {
				token := c.mqttClient.Publish(fmt.Sprintf("lxp/cmd/%s/read/inputs/1", deviceID), 0, true, "")
				token.Wait()
				token = c.mqttClient.Publish(fmt.Sprintf("lxp/cmd/%s/read/inputs/2", deviceID), 0, true, "")
				token.Wait()
			}
		}
	}
}

func (c *client) messagePubHandler(client mqtt.Client, msg mqtt.Message) {
	c.messages <- msg
}

var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	log.Println("Connected")
}

func (c *client) connectLostHandler(client mqtt.Client, err error) {
	log.Printf("Connect lost: %v", err)
	c.done <- true
}

func (c *client) sub() {
	topics := make(map[string]byte)
	topicNames := make([]string, 0, len(c.config.DeviceIDs))
	for _, device := range c.config.DeviceIDs {
		topics[fmt.Sprintf("lxp/%s/inputs/#", device)] = 1
	}
	token := c.mqttClient.SubscribeMultiple(topics, nil)
	token.Wait()
	log.Printf("Subscribed to topics: %s", strings.Join(topicNames, ", "))
}

func (c *client) ProcessData(topic string, message []byte) error {
	var payload models.MessageJson
	err := json.Unmarshal(message, &payload)
	if err != nil {
		return err
	}
	segments := strings.Split(topic, "/")
	parser := c.GetDataParser(segments, DefaultParser)
	parser(segments, payload)
	if c.debug {
		log.Printf("Got message from topic: %s %s", topic, message)
	}
	return nil
}

func DefaultParser(segments []string, message models.MessageJson) {
}

func (c *client) GetDataParser(segments []string, defaultParser func(topic []string, message models.MessageJson)) func(topic []string, message models.MessageJson) {
	if len(segments) < 4 {
		return defaultParser
	}
	switch segments[3] {
	case "1", "2", "all":
		return c.ParseInputs
	default:
		return defaultParser
	}

}

func (c *client) ParseInputs(segments []string, message models.MessageJson) {
	if len(segments) < 4 {
		return
	}
	baseTags := []string{
		metrics.FormatTag("deploymentID", segments[1]),
	}
	for key, value := range message {
		switch key {
		case "time", "runtime", "datalog":
			continue
		default:
			switch v := value.(type) {
			case int:
				metrics.SendIntGaugeMetric(fmt.Sprintf("%s_%s", metricPrefix, key), baseTags, v)
			case float64:
				metrics.SendGaugeMetric(fmt.Sprintf("%s_%s", metricPrefix, key), baseTags, v)
			}
		}
	}

}
