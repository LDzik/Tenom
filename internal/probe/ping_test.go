package probe

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/storage"
)

func TestEngine_ReloadTargets(t *testing.T) {
	cfg := config.ProbeConfig{
		IntervalSeconds:  1,
		PacketCount:      1,
		TimeoutSeconds:   1,
		JitterThresholdMs: 120,
		LossThresholdPct:  10,
	}

	initialTargets := []config.Target{
		{Name: "Target1", Host: "127.0.0.1", Category: "LAN"},
	}

	sampleChan := make(chan storage.PingSample, 16)
	engine := NewEngine(cfg, initialTargets, sampleChan, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	engine.Start(ctx, &wg)

	time.Sleep(100 * time.Millisecond)

	newTargets := []config.Target{
		{Name: "Target2", Host: "127.0.0.1", Category: "LAN"},
		{Name: "Target3", Host: "127.0.0.2", Category: "LAN"},
	}

	engine.ReloadTargets(newTargets)

	engine.mu.Lock()
	if len(engine.targets) != 2 {
		engine.mu.Unlock()
		t.Fatalf("Expected 2 targets, got %d", len(engine.targets))
	}
	if len(engine.cancels) != 2 {
		engine.mu.Unlock()
		t.Fatalf("Expected 2 worker cancels, got %d", len(engine.cancels))
	}
	engine.mu.Unlock()

	cancel()
	wg.Wait()
}
