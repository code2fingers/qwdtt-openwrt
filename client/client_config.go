package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type clientFileConfig struct {
	Peer        string   `json:"peer"`
	Hashes      []string `json:"hashes"`
	Password    string   `json:"password"`
	DeviceID    string   `json:"device_id"`
	Workers     int      `json:"workers"`
	DNS         string   `json:"dns"`
	Obfs        string   `json:"obfs"`
	CaptchaMode string   `json:"captcha_mode"`
	VKAuth      string   `json:"vk_auth"`
	VKAnonPath  string   `json:"vk_anon_path"`
	NoDTLS      bool     `json:"no_dtls"`
	TurnTCP     bool     `json:"turn_tcp"`
	TunName     string   `json:"tun_name"`
}

func configPathFromArgs(args []string) string {
	for i, arg := range args {
		if arg == "-config" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "-config=") {
			return strings.TrimPrefix(arg, "-config=")
		}
	}
	return ""
}

func loadClientFileConfig(path string) (clientFileConfig, error) {
	var cfg clientFileConfig
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

func (c clientFileConfig) hashesCSV() string {
	return strings.Join(c.Hashes, ",")
}
