package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

type DNSServer struct {
	Name string `json:"name"`
	Host string `json:"host"`
}

type DNSConfig struct {
	Enabled         bool        `json:"enabled"`
	IntervalSeconds int         `json:"interval_seconds"`
	TimeoutSeconds  int         `json:"timeout_seconds"`
	Domain          string      `json:"domain"`
	Servers         []DNSServer `json:"servers"`
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
	DNS      DNSConfig      `json:"dns"`
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

func SaveConfig(path string, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config cannot be nil")
	}
	if len(cfg.Targets) == 0 {
		return fmt.Errorf("config validation error: at least one target must be defined")
	}

	for i, t := range cfg.Targets {
		if t.Host == "" {
			return fmt.Errorf("target #%d host cannot be empty", i+1)
		}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize config JSON: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory for config: %w", err)
		}
	}

	
	tmpFile, err := os.CreateTemp(dir, "tenom-config-*.tmp")
	if err != nil {
		return os.WriteFile(path, data, 0644)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write temp config file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp config file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		if err := os.WriteFile(path, data, 0644); err != nil {
			return fmt.Errorf("failed to write config file: %w", err)
		}
	}

	return nil
}
