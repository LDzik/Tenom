package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var validHostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`)

func ValidateHost(host string) error {
	h := strings.TrimSpace(host)
	if h == "" || strings.HasPrefix(h, "-") {
		return fmt.Errorf("invalid host %q: cannot be empty or start with '-'", host)
	}
	if ip := net.ParseIP(h); ip != nil {
		return nil
	}
	if !validHostnameRe.MatchString(h) {
		return fmt.Errorf("invalid host %q: must be a valid IP address or RFC 1123 hostname", host)
	}
	return nil
}

func ValidateDNSServerHost(hostPort string) error {
	hp := strings.TrimSpace(hostPort)
	if hp == "" || strings.HasPrefix(hp, "-") {
		return fmt.Errorf("invalid DNS server host %q: cannot be empty or start with '-'", hostPort)
	}
	host := hp
	if strings.Contains(hp, ":") {
		if h, _, err := net.SplitHostPort(hp); err == nil {
			host = h
		} else if ip := net.ParseIP(hp); ip != nil {
			return nil
		} else {
			return fmt.Errorf("invalid DNS server address %q: %w", hostPort, err)
		}
	}
	return ValidateHost(host)
}

func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	cp := *c
	if c.Targets != nil {
		cp.Targets = make([]Target, len(c.Targets))
		copy(cp.Targets, c.Targets)
	}
	if c.DNS.Servers != nil {
		cp.DNS.Servers = make([]DNSServer, len(c.DNS.Servers))
		copy(cp.DNS.Servers, c.DNS.Servers)
	}
	return &cp
}

func (c *Config) SanitizeAndValidate() error {
	if c == nil {
		return fmt.Errorf("config cannot be nil")
	}
	if len(c.Targets) == 0 {
		return fmt.Errorf("config validation error: at least one target must be defined")
	}

	for i, t := range c.Targets {
		if err := ValidateHost(t.Host); err != nil {
			return fmt.Errorf("target #%d %w", i+1, err)
		}
		c.Targets[i].Host = strings.TrimSpace(t.Host)
		if strings.TrimSpace(t.Name) == "" {
			c.Targets[i].Name = c.Targets[i].Host
		}
		if strings.TrimSpace(t.Category) == "" {
			c.Targets[i].Category = "WAN"
		}
	}

	if c.Probe.IntervalSeconds <= 0 {
		c.Probe.IntervalSeconds = 15
	} else if c.Probe.IntervalSeconds > 3600 {
		c.Probe.IntervalSeconds = 3600
	}
	if c.Probe.PacketCount <= 0 {
		c.Probe.PacketCount = 3
	} else if c.Probe.PacketCount > 20 {
		c.Probe.PacketCount = 20
	}
	if c.Probe.TimeoutSeconds <= 0 {
		c.Probe.TimeoutSeconds = 3
	} else if c.Probe.TimeoutSeconds > 60 {
		c.Probe.TimeoutSeconds = 60
	}
	if c.Probe.JitterThresholdMs <= 0 {
		c.Probe.JitterThresholdMs = 120.0
	}
	if c.Probe.LossThresholdPct <= 0 {
		c.Probe.LossThresholdPct = 10.0
	} else if c.Probe.LossThresholdPct > 100.0 {
		c.Probe.LossThresholdPct = 100.0
	}

	if c.DNS.IntervalSeconds <= 0 {
		c.DNS.IntervalSeconds = 30
	} else if c.DNS.IntervalSeconds > 3600 {
		c.DNS.IntervalSeconds = 3600
	}
	if c.DNS.TimeoutSeconds <= 0 {
		c.DNS.TimeoutSeconds = 3
	} else if c.DNS.TimeoutSeconds > 30 {
		c.DNS.TimeoutSeconds = 30
	}
	if strings.TrimSpace(c.DNS.Domain) == "" {
		c.DNS.Domain = "google.com"
	}

	if c.DNS.Enabled {
		for i, srv := range c.DNS.Servers {
			if err := ValidateDNSServerHost(srv.Host); err != nil {
				return fmt.Errorf("DNS server #%d %w", i+1, err)
			}
			c.DNS.Servers[i].Host = strings.TrimSpace(srv.Host)
			if strings.TrimSpace(srv.Name) == "" {
				c.DNS.Servers[i].Name = c.DNS.Servers[i].Host
			}
		}
	}

	if c.Database.RetentionDays <= 0 {
		c.Database.RetentionDays = 7
	}
	return nil
}

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

	if err := cfg.SanitizeAndValidate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func SaveConfig(path string, cfg *Config) error {
	if err := cfg.SanitizeAndValidate(); err != nil {
		return err
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
