package config_test

import (
	"path/filepath"
	"testing"

	"github.com/LDzik/Tenom/internal/config"
)

func TestSaveAndLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	cfg := &config.Config{
		Server: config.ServerConfig{Port: 7366},
		Database: config.DatabaseConfig{
			Path:          "./data/tenom.db",
			RetentionDays: 7,
		},
		Probe: config.ProbeConfig{
			IntervalSeconds:   15,
			PacketCount:       3,
			TimeoutSeconds:    3,
			JitterThresholdMs: 120.0,
			LossThresholdPct:  10.0,
		},
		DNS: config.DNSConfig{
			Enabled:         true,
			IntervalSeconds: 30,
			TimeoutSeconds:  3,
			Domain:          "google.com",
			Servers: []config.DNSServer{
				{Name: "Cloudflare DNS", Host: "1.1.1.1:53"},
			},
		},
		Targets: []config.Target{
			{Name: "Gateway", Host: "192.168.1.1", Category: "LAN"},
			{Name: "Cloudflare", Host: "1.1.1.1", Category: "Public Anycast"},
		},
	}

	// 1. Save
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// 2. Load
	loaded, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if loaded.Server.Port != 7366 {
		t.Errorf("Expected port 7366, got %d", loaded.Server.Port)
	}
	if len(loaded.Targets) != 2 {
		t.Errorf("Expected 2 targets, got %d", len(loaded.Targets))
	}
	if loaded.Targets[0].Name != "Gateway" {
		t.Errorf("Expected first target 'Gateway', got %s", loaded.Targets[0].Name)
	}

	// 3. Validation failure when targets empty
	cfg.Targets = []config.Target{}
	if err := config.SaveConfig(cfgPath, cfg); err == nil {
		t.Errorf("Expected validation error for empty targets")
	}
}
