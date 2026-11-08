package storage

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type dummyTracker struct {
	state          string
	startedAt      *time.Time
	failureType    string
	activeDuration *float64
}

func (d *dummyTracker) GetNetworkState() (string, *time.Time, string) {
	return d.state, d.startedAt, d.failureType
}

func (d *dummyTracker) GetActiveOutageDuration() *float64 {
	return d.activeDuration
}

func setupTestDB(t *testing.T) (*Storage, func()) {
	tmpDir, err := os.MkdirTemp("", "tenom_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(tmpDir, "test_tenom.db")

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	cleanup := func() {
		store.Close()
		os.RemoveAll(tmpDir)
	}

	return store, cleanup
}

func TestStorage_CRUD(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)

	// 1. Insert and retrieve PingSample
	err := store.InsertPingSample(PingSample{
		Target:     "1.1.1.1",
		Category:   "WAN",
		RttMs:      12.5,
		PacketLoss: 0,
		Timestamp:  now,
	})
	if err != nil {
		t.Fatalf("InsertPingSample failed: %v", err)
	}

	latest, err := store.GetLatestSamples()
	if err != nil {
		t.Fatalf("GetLatestSamples failed: %v", err)
	}
	if len(latest) != 1 || latest[0].Target != "1.1.1.1" || latest[0].RttMs != 12.5 {
		t.Fatalf("Unexpected latest samples: %+v", latest)
	}

	// 2. Insert and retrieve DNSSample
	err = store.InsertDNSSample(DNSSample{
		Server:    "Cloudflare",
		Domain:    "google.com",
		LatencyMs: 18.2,
		Status:    "OK",
		Timestamp: now,
	})
	if err != nil {
		t.Fatalf("InsertDNSSample failed: %v", err)
	}

	// 3. Insert and retrieve Incident
	resolvedAt := now.Add(2 * time.Minute)
	err = store.InsertIncident(Incident{
		StartedAt:        now,
		ResolvedAt:       &resolvedAt,
		Target:           "1.1.1.1",
		TriggerReason:    "Packet loss 100%",
		TracerouteOutput: "1 * * *\n2 1.1.1.1",
	})
	if err != nil {
		t.Fatalf("InsertIncident failed: %v", err)
	}

	// 4. Insert and retrieve Outage
	err = store.InsertOutage(Outage{
		StartedAt:       now,
		ResolvedAt:      &resolvedAt,
		DurationSeconds: 120.0,
		FailureType:     "WAN_OUTAGE",
	})
	if err != nil {
		t.Fatalf("InsertOutage failed: %v", err)
	}

	incidents, err := store.GetIncidents(10)
	if err != nil {
		t.Fatalf("GetIncidents failed: %v", err)
	}
	if len(incidents.Traceroutes) != 1 || len(incidents.Outages) != 1 {
		t.Fatalf("Expected 1 traceroute and 1 outage, got %+v", incidents)
	}
	if incidents.Outages[0].FailureType != "WAN_OUTAGE" {
		t.Fatalf("Expected WAN_OUTAGE, got %s", incidents.Outages[0].FailureType)
	}
}

func TestStorage_GetHistory_Bucketing(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	baseTime := time.Now().UTC().Add(-30 * time.Minute)

	// Insert samples at 15-second intervals over 20 minutes
	for i := 0; i < 80; i++ {
		ts := baseTime.Add(time.Duration(i*15) * time.Second)
		err := store.InsertPingSample(PingSample{
			Target:     "8.8.8.8",
			Category:   "WAN",
			RttMs:      10.0 + float64(i%5),
			PacketLoss: 0.0,
			Timestamp:  ts,
		})
		if err != nil {
			t.Fatalf("InsertPingSample failed: %v", err)
		}
	}

	// 1. Query with 1h range -> 1-minute buckets
	pts1h, err := store.GetHistory("8.8.8.8", "1h")
	if err != nil {
		t.Fatalf("GetHistory 1h failed: %v", err)
	}
	if len(pts1h) == 0 {
		t.Fatalf("Expected points for 1h, got 0")
	}
	// 20 minutes of data at 1m buckets should yield around 20-21 buckets
	if len(pts1h) > 30 {
		t.Fatalf("Expected around 20 buckets, got %d", len(pts1h))
	}

	// 2. Query with 24h range -> 5-minute buckets
	pts24h, err := store.GetHistory("8.8.8.8", "24h")
	if err != nil {
		t.Fatalf("GetHistory 24h failed: %v", err)
	}
	// 20 minutes of data at 5m buckets should yield around 4-5 buckets
	if len(pts24h) < 3 || len(pts24h) > 6 {
		t.Fatalf("Expected ~4-5 buckets for 24h range, got %d", len(pts24h))
	}

	// 3. Query with 7d range -> 30-minute buckets
	pts7d, err := store.GetHistory("8.8.8.8", "7d")
	if err != nil {
		t.Fatalf("GetHistory 7d failed: %v", err)
	}
	if len(pts7d) < 1 || len(pts7d) > 2 {
		t.Fatalf("Expected 1-2 buckets for 7d range, got %d", len(pts7d))
	}
}

func TestStorage_GetDNSHistory(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	baseTime := time.Now().UTC().Add(-10 * time.Minute)

	servers := []string{"Cloudflare", "Google"}
	for i := 0; i < 20; i++ {
		ts := baseTime.Add(time.Duration(i*30) * time.Second)
		for _, srv := range servers {
			err := store.InsertDNSSample(DNSSample{
				Server:    srv,
				Domain:    "google.com",
				LatencyMs: 15.0 + float64(i),
				Status:    "OK",
				Timestamp: ts,
			})
			if err != nil {
				t.Fatalf("InsertDNSSample failed: %v", err)
			}
		}
	}

	points, err := store.GetDNSHistory("1h")
	if err != nil {
		t.Fatalf("GetDNSHistory failed: %v", err)
	}
	if len(points) == 0 {
		t.Fatalf("Expected DNS points, got 0")
	}

	seenCloudflare := false
	seenGoogle := false
	for _, p := range points {
		if p.Server == "Cloudflare" {
			seenCloudflare = true
		}
		if p.Server == "Google" {
			seenGoogle = true
		}
	}
	if !seenCloudflare || !seenGoogle {
		t.Fatalf("Expected both servers in DNS history, got %v", points)
	}
}

func TestStorage_GetLiveStatus(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	// 1. Without tracker or samples
	status, err := store.GetLiveStatus()
	if err != nil {
		t.Fatalf("GetLiveStatus failed: %v", err)
	}
	if status.State != "Healthy" || status.Uptime24h != 100.0 {
		t.Fatalf("Expected default Healthy/100%%, got %+v", status)
	}

	// 2. Add sample
	err = store.InsertPingSample(PingSample{
		Target:     "192.168.1.1",
		Category:   "LAN",
		RttMs:      0.5,
		PacketLoss: 0,
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add an outage of 864s (1% of 24h)
	started := time.Now().UTC().Add(-2 * time.Hour)
	resolved := started.Add(864 * time.Second)
	err = store.InsertOutage(Outage{
		StartedAt:       started,
		ResolvedAt:      &resolved,
		DurationSeconds: 864.0,
		FailureType:     "WAN_OUTAGE",
	})
	if err != nil {
		t.Fatal(err)
	}

	// With attached tracker
	dur := 12.0
	actStart := time.Now().UTC().Add(-12 * time.Second)
	tracker := &dummyTracker{
		state:          "Degraded",
		startedAt:      &actStart,
		failureType:    "WAN_OUTAGE",
		activeDuration: &dur,
	}
	store.SetOutageTracker(tracker)

	status, err = store.GetLiveStatus()
	if err != nil {
		t.Fatalf("GetLiveStatus failed: %v", err)
	}
	if status.State != "Degraded" {
		t.Fatalf("Expected Degraded state from tracker, got %s", status.State)
	}
	if !status.ActiveOutage || status.ActiveOutageDuration == nil {
		t.Fatalf("Expected active outage to be true with duration, got %+v", status)
	}
	// 864s + 12s = 876s downtime => uptime ~ ((86400 - 876)/86400)*100 ~ 98.99%
	if math.Abs(status.Uptime24h-98.99) > 0.05 {
		t.Fatalf("Expected uptime around 98.99%%, got %f", status.Uptime24h)
	}
	if len(status.Targets) != 1 {
		t.Fatalf("Expected 1 target snapshot, got %d", len(status.Targets))
	}
}

func TestStorage_PruneOldSamples(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	oldTime := time.Now().UTC().Add(-10 * 24 * time.Hour)
	newTime := time.Now().UTC()

	_ = store.InsertPingSample(PingSample{Target: "1.1.1.1", Category: "WAN", Timestamp: oldTime})
	_ = store.InsertPingSample(PingSample{Target: "1.1.1.1", Category: "WAN", Timestamp: newTime})

	deleted, err := store.PruneOldSamples(7)
	if err != nil {
		t.Fatalf("PruneOldSamples failed: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("Expected 1 deleted row, got %d", deleted)
	}

	samples, _ := store.GetLatestSamples()
	if len(samples) != 1 {
		t.Fatalf("Expected 1 remaining sample, got %d", len(samples))
	}
}

func TestStorage_ReconcileOrphanedRecords(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "orphan_test.db")

	store1, err := New(dbPath)
	if err != nil {
		t.Fatalf("Failed to open store1: %v", err)
	}

	// Simulate an unclosed outage and incident from 48 hours ago before a crash
	oldStart := time.Now().UTC().Add(-48 * time.Hour)
	_ = store1.InsertOutage(Outage{
		StartedAt:       oldStart,
		ResolvedAt:      nil,
		DurationSeconds: 30.0,
		FailureType:     "WAN_OUTAGE",
	})
	_ = store1.InsertIncident(Incident{
		StartedAt:        oldStart,
		ResolvedAt:       nil,
		Target:           "1.1.1.1",
		TriggerReason:    "High loss",
		TracerouteOutput: "trace",
	})
	_ = store1.Close()

	// Re-open storage; startup reconciliation should close both NULL records
	store2, err := New(dbPath)
	if err != nil {
		t.Fatalf("Failed to reopen store2: %v", err)
	}
	defer store2.Close()

	downtime, err := store2.GetDowntimeSeconds24h()
	if err != nil {
		t.Fatalf("GetDowntimeSeconds24h failed: %v", err)
	}
	if downtime != 0 {
		t.Fatalf("Expected 0s downtime in past 24h after reconciling 48h-old orphan, got %f", downtime)
	}

	incResp, err := store2.GetIncidents(10)
	if err != nil {
		t.Fatalf("GetIncidents failed: %v", err)
	}
	if len(incResp.Incidents) != 1 || incResp.Incidents[0].ResolvedAt == nil {
		t.Fatalf("Expected reconciled incident to have non-nil ResolvedAt, got %+v", incResp.Incidents)
	}
}
