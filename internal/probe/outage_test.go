package probe

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/storage"
)

type mockStorage struct {
	mu       sync.Mutex
	outages  []storage.Outage
	downtime float64
}

func (m *mockStorage) InsertOutage(outage storage.Outage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outages = append(m.outages, outage)
	return nil
}

func (m *mockStorage) GetDowntimeSeconds24h() (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.downtime, nil
}

func TestOutageEngine_StateTransitions(t *testing.T) {
	cfg := config.ProbeConfig{
		LossThresholdPct:  10.0,
		JitterThresholdMs: 120.0,
	}

	targets := []config.Target{
		{Name: "Gateway", Host: "192.168.1.1", Category: "LAN"},
		{Name: "Cloudflare", Host: "1.1.1.1", Category: "Public Anycast"},
		{Name: "Google", Host: "8.8.8.8", Category: "Public Anycast"},
	}

	mockStore := &mockStorage{}
	tracker := NewOutageTracker(cfg, targets, mockStore)

	// 1. Initial state should be Healthy
	state, startedAt, _ := tracker.GetNetworkState()
	if state != string(StateHealthy) || startedAt != nil {
		t.Fatalf("Expected Healthy, got state=%s, startedAt=%v", state, startedAt)
	}

	// 2. Normal ping samples -> should stay Healthy
	now := time.Now()
	tracker.RecordPing(storage.PingSample{Target: "192.168.1.1", Category: "LAN", RttMs: 1.0, PacketLoss: 0, Timestamp: now})
	tracker.RecordPing(storage.PingSample{Target: "1.1.1.1", Category: "Public Anycast", RttMs: 12.0, PacketLoss: 0, Timestamp: now})
	tracker.RecordPing(storage.PingSample{Target: "8.8.8.8", Category: "Public Anycast", RttMs: 14.0, PacketLoss: 0, Timestamp: now})

	state, startedAt, _ = tracker.GetNetworkState()
	if state != string(StateHealthy) || startedAt != nil {
		t.Fatalf("Expected Healthy, got state=%s, startedAt=%v", state, startedAt)
	}

	// 3. Degraded: High jitter or moderate loss on WAN target
	tracker.RecordPing(storage.PingSample{Target: "1.1.1.1", Category: "Public Anycast", RttMs: 150.0, PacketLoss: 0, Timestamp: now.Add(time.Second)})
	state, startedAt, _ = tracker.GetNetworkState()
	if state != string(StateDegraded) || startedAt != nil {
		t.Fatalf("Expected Degraded, got state=%s, startedAt=%v", state, startedAt)
	}

	// 4. Outage: 100% loss to WAN targets while gateway is alive -> WAN_OUTAGE
	tracker.RecordPing(storage.PingSample{Target: "1.1.1.1", Category: "Public Anycast", RttMs: 0, PacketLoss: 100.0, Timestamp: now.Add(2 * time.Second)})
	tracker.RecordPing(storage.PingSample{Target: "8.8.8.8", Category: "Public Anycast", RttMs: 0, PacketLoss: 100.0, Timestamp: now.Add(2 * time.Second)})

	state, startedAt, failureType := tracker.GetNetworkState()
	if state != string(StateDown) {
		t.Fatalf("Expected Down, got %s", state)
	}
	if startedAt == nil {
		t.Fatalf("Expected startedAt timestamp to be set")
	}
	if failureType != FailureWANOutage {
		t.Fatalf("Expected failureType=%s, got %s", FailureWANOutage, failureType)
	}

	// Check active outage duration
	dur := tracker.GetActiveOutageDuration()
	if dur == nil || *dur < 0 {
		t.Fatalf("Expected positive active outage duration, got %v", dur)
	}

	// 5. Total gateway drop while in outage -> failure type updates to GATEWAY_DROP
	tracker.RecordPing(storage.PingSample{Target: "192.168.1.1", Category: "LAN", RttMs: 0, PacketLoss: 100.0, Timestamp: now.Add(4 * time.Second)})
	state, _, failureType = tracker.GetNetworkState()
	if state != string(StateDown) || failureType != FailureGatewayDrop {
		t.Fatalf("Expected Down with GATEWAY_DROP, got state=%s, failureType=%s", state, failureType)
	}

	// 6. Recovery: Gateway and WAN recover
	recoverTime := now.Add(10 * time.Second)
	tracker.RecordPing(storage.PingSample{Target: "192.168.1.1", Category: "LAN", RttMs: 0.8, PacketLoss: 0, Timestamp: recoverTime})
	tracker.RecordPing(storage.PingSample{Target: "1.1.1.1", Category: "Public Anycast", RttMs: 11.0, PacketLoss: 0, Timestamp: recoverTime})
	tracker.RecordPing(storage.PingSample{Target: "8.8.8.8", Category: "Public Anycast", RttMs: 12.0, PacketLoss: 0, Timestamp: recoverTime})

	state, startedAt, _ = tracker.GetNetworkState()
	if state != string(StateHealthy) || startedAt != nil {
		t.Fatalf("Expected Healthy after recovery, got state=%s, startedAt=%v", state, startedAt)
	}

	// Wait briefly for goroutine persistence
	time.Sleep(50 * time.Millisecond)

	mockStore.mu.Lock()
	defer mockStore.mu.Unlock()
	if len(mockStore.outages) != 1 {
		t.Fatalf("Expected 1 saved outage in store, got %d", len(mockStore.outages))
	}
	saved := mockStore.outages[0]
	if saved.DurationSeconds < 7.0 || saved.DurationSeconds > 9.0 {
		t.Fatalf("Expected outage duration around 8s, got %f", saved.DurationSeconds)
	}
	if saved.FailureType != FailureGatewayDrop {
		t.Fatalf("Expected FailureGatewayDrop, got %s", saved.FailureType)
	}
}

func TestOutageEngine_UptimeCalculation(t *testing.T) {
	cfg := config.ProbeConfig{}
	targets := []config.Target{
		{Name: "Google", Host: "8.8.8.8", Category: "WAN"},
	}

	// Case 1: 0 downtime -> 100.0%
	mockStore := &mockStorage{downtime: 0}
	tracker := NewOutageTracker(cfg, targets, mockStore)
	uptime, err := tracker.Compute24hUptime()
	if err != nil {
		t.Fatal(err)
	}
	if uptime != 100.0 {
		t.Fatalf("Expected 100.0, got %f", uptime)
	}

	// Case 2: 864s downtime -> 99.0%
	mockStore.downtime = 864.0
	uptime, err = tracker.Compute24hUptime()
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(uptime-99.0) > 0.01 {
		t.Fatalf("Expected 99.0, got %f", uptime)
	}

	// Case 3: Ongoing active outage
	now := time.Now().UTC()
	tracker.RecordPing(storage.PingSample{
		Target:     "8.8.8.8",
		Category:   "WAN",
		PacketLoss: 100.0,
		Timestamp:  now.Add(-60 * time.Second),
	})

	uptimeWithActive, err := tracker.Compute24hUptime()
	if err != nil {
		t.Fatal(err)
	}
	// Total downtime ~ 864 + 60 = 924s => uptime ~ (86400 - 924)/86400 * 100 ~ 98.93%
	if uptimeWithActive >= uptime {
		t.Fatalf("Expected active outage to reduce uptime, before=%f, after=%f", uptime, uptimeWithActive)
	}
}

func TestOutageEngine_Concurrency(t *testing.T) {
	cfg := config.ProbeConfig{LossThresholdPct: 10.0}
	targets := []config.Target{
		{Name: "GW", Host: "10.0.0.1", Category: "LAN"},
		{Name: "DNS", Host: "1.1.1.1", Category: "WAN"},
	}
	mockStore := &mockStorage{}
	tracker := NewOutageTracker(cfg, targets, mockStore)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				loss := 0.0
				if (id+j)%5 == 0 {
					loss = 100.0
				}
				tracker.RecordPing(storage.PingSample{
					Target:     fmt.Sprintf("1.1.1.%d", id%2),
					Category:   "WAN",
					RttMs:      float64(10 + j),
					PacketLoss: loss,
					Timestamp:  time.Now(),
				})
				_ = tracker.GetActiveOutageDuration()
				_, _, _ = tracker.GetNetworkState()
				_, _ = tracker.Compute24hUptime()
			}
		}(i)
	}
	wg.Wait()
}
