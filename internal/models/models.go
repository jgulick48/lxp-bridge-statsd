package models

type MQTTConfiguration struct {
	StatsServer string   `json:"statsServer"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	DeviceIDs   []string `json:"deviceIDs"`
}

type MessageJson map[string]interface{}
