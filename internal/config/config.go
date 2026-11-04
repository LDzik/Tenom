package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Target struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Category string `json:"category"`
}

type ProbeConfig struct {
	IntervalSeconds   int     `json:"interval_seconds"`
	PacketCount       int     `json:"packet_count"`
	TimeoutSeconds    int     `json:"timeout_seconds"`
	JitterThresholdMs float64 `json:"jitter_threshold_ms"`
	LossThresholdPct  float64 `json:"loss_threshold_pct"`
}

type DatabaseConfig struct {
	Path          string `json:"path"`
	RetentionDays int    `json:"retention_days"`
}

type ServerConfig struct {
	Port int `json:"port"`
}

type Config struct {
	Server   ServerConfig   `json:"server"`
	Database DatabaseConfig `json:"database"`
	Probe    ProbeConfig    `json:"probe"`
	Targets  []Target       `json:"targets"`
}

func LoadConfig(filePath string) (*Config, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var cfg Config
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config JSON: %w", err)
	}

	if len(cfg.Targets) == 0 {
		return nil, fmt.Errorf("config validation error: at least one target must be defined")
	}

	return &cfg, nil
}