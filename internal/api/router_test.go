package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/storage"
)

func setupTestRouter(t *testing.T) (http.Handler, *storage.Storage, func()) {
	tmpDir, err := os.MkdirTemp("", "tenom_api_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(tmpDir, "test_api.db")

	store, err := storage.New(dbPath)
	if err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	cfg := &config.Config{
		Server: config.ServerConfig{Port: 7366},
		Targets: []config.Target{
			{Name: "Gateway", Host: "192.168.1.1", Category: "LAN"},
			{Name: "Cloudflare", Host: "1.1.1.1", Category: "Public Anycast"},
		},
	}

	cfgPath := filepath.Join(tmpDir, "test_config.json")
	_ = config.SaveConfig(cfgPath, cfg)
	router := NewRouterWithPath(store, cfg, cfgPath)

	cleanup := func() {
		store.Close()
		os.RemoveAll(tmpDir)
	}

	return router, store, cleanup
}

func TestRouter_Status(t *testing.T) {
	router, store, cleanup := setupTestRouter(t)
	defer cleanup()

	now := time.Now().UTC()
	_ = store.InsertPingSample(storage.PingSample{
		Target:     "192.168.1.1",
		Category:   "LAN",
		RttMs:      0.45,
		PacketLoss: 0.0,
		Timestamp:  now,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", rec.Code)
	}

	var status storage.LiveStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if status.State != "Healthy" {
		t.Fatalf("Expected Healthy, got %s", status.State)
	}
	if status.Uptime24h != 100.0 {
		t.Fatalf("Expected 100.0 uptime, got %f", status.Uptime24h)
	}
	if len(status.Targets) != 1 || status.Targets[0].Target != "192.168.1.1" {
		t.Fatalf("Expected 1 target, got %+v", status.Targets)
	}
}

func TestRouter_PingMetrics(t *testing.T) {
	router, store, cleanup := setupTestRouter(t)
	defer cleanup()

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		_ = store.InsertPingSample(storage.PingSample{
			Target:     "1.1.1.1",
			Category:   "WAN",
			RttMs:      10.0 + float64(i),
			PacketLoss: 0.0,
			Timestamp:  now.Add(-time.Duration(i*2) * time.Minute),
		})
	}

	// 1. Success with default range and target
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/ping?target=1.1.1.1&range=1h", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var points []storage.PingHistoryPoint
	if err := json.Unmarshal(rec.Body.Bytes(), &points); err != nil {
		t.Fatalf("Failed to decode points: %v", err)
	}
	if len(points) == 0 {
		t.Fatalf("Expected non-empty points")
	}

	// 2. Invalid range returns 400
	req = httptest.NewRequest(http.MethodGet, "/api/v1/metrics/ping?target=1.1.1.1&range=invalid", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 for invalid range, got %d", rec.Code)
	}

	// 3. 7d range
	req = httptest.NewRequest(http.MethodGet, "/api/v1/metrics/ping?target=1.1.1.1&range=7d", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for 7d, got %d", rec.Code)
	}
}

func TestRouter_DNSMetrics(t *testing.T) {
	router, store, cleanup := setupTestRouter(t)
	defer cleanup()

	now := time.Now().UTC()
	_ = store.InsertDNSSample(storage.DNSSample{
		Server:    "Cloudflare",
		Domain:    "google.com",
		LatencyMs: 14.2,
		Status:    "OK",
		Timestamp: now,
	})

	// 1. Success with 24h
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/dns?range=24h", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var points []storage.DNSHistoryPoint
	if err := json.Unmarshal(rec.Body.Bytes(), &points); err != nil {
		t.Fatalf("Failed to decode points: %v", err)
	}
	if len(points) != 1 || points[0].Server != "Cloudflare" {
		t.Fatalf("Unexpected DNS points: %+v", points)
	}

	// 2. 7d range supported
	req = httptest.NewRequest(http.MethodGet, "/api/v1/metrics/dns?range=7d", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for 7d on DNS, got %d", rec.Code)
	}

	// 3. Invalid range
	req = httptest.NewRequest(http.MethodGet, "/api/v1/metrics/dns?range=invalid", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 for invalid range on DNS, got %d", rec.Code)
	}
}

func TestRouter_Incidents(t *testing.T) {
	router, store, cleanup := setupTestRouter(t)
	defer cleanup()

	now := time.Now().UTC()
	resolved := now.Add(30 * time.Second)
	_ = store.InsertIncident(storage.Incident{
		StartedAt:        now,
		ResolvedAt:       &resolved,
		Target:           "1.1.1.1",
		TriggerReason:    "High loss",
		TracerouteOutput: "trace output",
	})
	_ = store.InsertOutage(storage.Outage{
		StartedAt:       now,
		ResolvedAt:      &resolved,
		DurationSeconds: 30.0,
		FailureType:     "WAN_OUTAGE",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?limit=10", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp storage.IncidentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode incidents: %v", err)
	}
	if len(resp.Traceroutes) != 1 || len(resp.Outages) != 1 {
		t.Fatalf("Expected 1 traceroute and 1 outage, got %+v", resp)
	}
}

func TestRouter_CORS(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// Preflight OPTIONS request
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for OPTIONS, got %d", rec.Code)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Fatalf("Expected Access-Control-Allow-Origin: *, got %s", origin)
	}

	// Normal GET request has CORS header
	req = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Fatalf("Expected Access-Control-Allow-Origin: *, got %s", origin)
	}
}

func TestRouter_WebFrontend(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// 1. Root serves index.html
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for /, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "TENOM") {
		t.Fatalf("Expected index.html content at /")
	}

	// 2. Static CSS
	req = httptest.NewRequest(http.MethodGet, "/static/css/style.css", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for /static/css/style.css, got %d", rec.Code)
	}

	// 3. Static JS
	req = httptest.NewRequest(http.MethodGet, "/static/js/app.js", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for /static/js/app.js, got %d", rec.Code)
	}
}

func TestRouter_Config(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// 1. GET /api/v1/config
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for GET /api/v1/config, got %d: %s", rec.Code, rec.Body.String())
	}

	var loadedCfg config.Config
	if err := json.Unmarshal(rec.Body.Bytes(), &loadedCfg); err != nil {
		t.Fatalf("Failed to decode config response: %v", err)
	}
	if len(loadedCfg.Targets) != 2 {
		t.Fatalf("Expected 2 targets, got %d", len(loadedCfg.Targets))
	}

	// 2. PUT /api/v1/config success
	loadedCfg.Targets = append(loadedCfg.Targets, config.Target{
		Name:     "Google DNS",
		Host:     "8.8.8.8",
		Category: "Public Anycast",
	})
	loadedCfg.Probe.IntervalSeconds = 10

	payload, _ := json.Marshal(loadedCfg)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for PUT /api/v1/config, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify update via GET
	req = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var verifiedCfg config.Config
	if err := json.Unmarshal(rec.Body.Bytes(), &verifiedCfg); err != nil {
		t.Fatalf("Failed to decode updated config: %v", err)
	}
	if len(verifiedCfg.Targets) != 3 {
		t.Fatalf("Expected 3 targets after PUT, got %d", len(verifiedCfg.Targets))
	}
	if verifiedCfg.Probe.IntervalSeconds != 10 {
		t.Fatalf("Expected interval 10, got %d", verifiedCfg.Probe.IntervalSeconds)
	}

	// 3. PUT /api/v1/config validation failure (empty targets)
	emptyTargetsCfg := verifiedCfg
	emptyTargetsCfg.Targets = []config.Target{}
	badPayload, _ := json.Marshal(emptyTargetsCfg)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(string(badPayload)))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 for empty targets, got %d", rec.Code)
	}

	// 4. PUT /api/v1/config cannot overwrite immutable Database.Path or Server.Port
	tamperedCfg := verifiedCfg
	tamperedCfg.Database.Path = "../../evil.db"
	tamperedCfg.Server.Port = 9999
	tamperedPayload, _ := json.Marshal(tamperedCfg)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(string(tamperedPayload)))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for PUT /api/v1/config, got %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var afterTamper config.Config
	_ = json.Unmarshal(rec.Body.Bytes(), &afterTamper)
	if afterTamper.Database.Path == "../../evil.db" || afterTamper.Server.Port == 9999 {
		t.Fatalf("Expected Database.Path and Server.Port to remain immutable, got path=%q port=%d",
			afterTamper.Database.Path, afterTamper.Server.Port)
	}
}

type mockReloader struct {
	reloadedTargets []config.Target
}

func (m *mockReloader) ReloadTargets(newTargets []config.Target) {
	m.reloadedTargets = newTargets
}

func TestRouter_ConfigReload(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenom_api_reload_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_reload.db")
	store, err := storage.New(dbPath)
	if err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}
	defer store.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Port: 7366},
		Targets: []config.Target{
			{Name: "Gateway", Host: "192.168.1.1", Category: "LAN"},
		},
	}

	cfgPath := filepath.Join(tmpDir, "test_config.json")
	_ = config.SaveConfig(cfgPath, cfg)

	mock := &mockReloader{}
	router := NewRouterWithPath(store, cfg, cfgPath, mock)

	cfg.Targets = append(cfg.Targets, config.Target{
		Name:     "New Target",
		Host:     "10.0.0.1",
		Category: "LAN",
	})
	payload, _ := json.Marshal(cfg)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", rec.Code)
	}
	if len(mock.reloadedTargets) != 2 {
		t.Fatalf("Expected mock reloader to receive 2 targets, got %d", len(mock.reloadedTargets))
	}
	if mock.reloadedTargets[1].Host != "10.0.0.1" {
		t.Fatalf("Expected reloaded target 10.0.0.1, got %s", mock.reloadedTargets[1].Host)
	}
}
