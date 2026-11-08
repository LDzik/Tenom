package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/storage"
	"github.com/LDzik/Tenom/web"
)

type TargetReloader interface {
	ReloadTargets(newTargets []config.Target)
}

type ProbeConfigReloader interface {
	ReloadProbe(newCfg config.ProbeConfig, newTargets []config.Target)
}

type Server struct {
	storage    *storage.Storage
	cfg        *config.Config
	cfgMu      sync.RWMutex
	configPath string
	reloaders  []TargetReloader
}

func NewRouter(store *storage.Storage, cfg *config.Config, reloader ...TargetReloader) http.Handler {
	return NewRouterWithPath(store, cfg, "config.json", reloader...)
}

func NewRouterWithPath(store *storage.Storage, cfg *config.Config, configPath string, reloader ...TargetReloader) http.Handler {
	if configPath == "" {
		configPath = "config.json"
	}

	s := &Server{
		storage:    store,
		cfg:        cfg,
		configPath: configPath,
		reloaders:  reloader,
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(corsMiddleware)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/status", s.handleStatus)
		r.Get("/metrics/ping", s.handlePingMetrics)
		r.Get("/metrics/latency", s.handlePingMetrics)
		r.Get("/metrics/icmp", s.handlePingMetrics)
		r.Get("/metrics/dns", s.handleDNSMetrics)
		r.Get("/incidents", s.handleIncidents)
		r.Get("/config", s.handleGetConfig)
		r.Put("/config", s.handleUpdateConfig)
	})

	r.Handle("/*", web.Handler())

	return r
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept, X-Requested-With")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func renderJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func renderError(w http.ResponseWriter, statusCode int, message string) {
	renderJSON(w, statusCode, map[string]string{"error": message})
}

// GET /api/v1/status -> Live health, uptime %, target snapshots.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.storage.GetLiveStatus()
	if err != nil {
		renderError(w, http.StatusInternalServerError, "Failed to retrieve live status: "+err.Error())
		return
	}

	s.cfgMu.RLock()
	if s.cfg != nil && len(s.cfg.Targets) > 0 {
		allowed := make(map[string]bool, len(s.cfg.Targets))
		for _, t := range s.cfg.Targets {
			allowed[t.Host] = true
		}
		filtered := make([]storage.PingSample, 0, len(status.Targets))
		for _, smp := range status.Targets {
			if allowed[smp.Target] {
				filtered = append(filtered, smp)
			}
		}
		status.Targets = filtered
	}
	s.cfgMu.RUnlock()

	renderJSON(w, http.StatusOK, status)
}

// GET /api/v1/metrics/ping?range=1h|24h|7d&target=<host> -> Downsampled ping time-series.
func (s *Server) handlePingMetrics(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	timeRange := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("range")))
	if timeRange == "" {
		timeRange = "24h"
	}

	switch timeRange {
	case "1h", "24h", "7d":
		// valid
	default:
		renderError(w, http.StatusBadRequest, "Invalid range parameter. Valid values: 1h, 24h, 7d")
		return
	}

	if target == "" {
		s.cfgMu.RLock()
		if s.cfg != nil && len(s.cfg.Targets) > 0 {
			target = s.cfg.Targets[0].Host
		}
		s.cfgMu.RUnlock()
	}

	points, err := s.storage.GetHistory(target, timeRange)
	if err != nil {
		renderError(w, http.StatusInternalServerError, "Failed to retrieve ping metrics: "+err.Error())
		return
	}
	if points == nil {
		points = []storage.PingHistoryPoint{}
	}

	renderJSON(w, http.StatusOK, points)
}

// GET /api/v1/metrics/dns?range=1h|24h|7d -> DNS latency time-series.
func (s *Server) handleDNSMetrics(w http.ResponseWriter, r *http.Request) {
	timeRange := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("range")))
	if timeRange == "" {
		timeRange = "24h"
	}

	switch timeRange {
	case "1h", "24h", "7d":
		// valid
	default:
		renderError(w, http.StatusBadRequest, "Invalid range parameter. Valid values: 1h, 24h, 7d")
		return
	}

	points, err := s.storage.GetDNSHistory(timeRange)
	if err != nil {
		renderError(w, http.StatusInternalServerError, "Failed to retrieve DNS metrics: "+err.Error())
		return
	}
	if points == nil {
		points = []storage.DNSHistoryPoint{}
	}

	renderJSON(w, http.StatusOK, points)
}

// GET /api/v1/incidents -> List of triggered traceroutes and outage logs.
func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	limitStr := strings.TrimSpace(r.URL.Query().Get("limit"))
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	incidents, err := s.storage.GetIncidents(limit)
	if err != nil {
		renderError(w, http.StatusInternalServerError, "Failed to retrieve incidents: "+err.Error())
		return
	}

	renderJSON(w, http.StatusOK, incidents)
}

// GET /api/v1/config -> Active configuration JSON.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.RLock()
	cfgSnapshot := s.cfg.Clone()
	s.cfgMu.RUnlock()

	if cfgSnapshot == nil {
		renderError(w, http.StatusNotFound, "Configuration is uninitialized")
		return
	}

	renderJSON(w, http.StatusOK, cfgSnapshot)
}

// PUT /api/v1/config -> Updates running config in-memory and persists to disk.
func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB max body size

	var updated config.Config
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&updated); err != nil {
		renderError(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error())
		return
	}

	if err := updated.SanitizeAndValidate(); err != nil {
		renderError(w, http.StatusBadRequest, "Configuration validation failed: "+err.Error())
		return
	}

	s.cfgMu.RLock()
	if s.cfg != nil {
		// Prevent HTTP clients from mutating listening port or filesystem database path
		updated.Server.Port = s.cfg.Server.Port
		updated.Database.Path = s.cfg.Database.Path
		if updated.Database.RetentionDays <= 0 {
			updated.Database.RetentionDays = s.cfg.Database.RetentionDays
		}
	}
	configPath := s.configPath
	s.cfgMu.RUnlock()

	if configPath == "" {
		configPath = "config.json"
	}

	if err := config.SaveConfig(configPath, &updated); err != nil {
		renderError(w, http.StatusInternalServerError, "Failed to persist configuration to disk: "+err.Error())
		return
	}

	s.cfgMu.Lock()
	s.cfg = updated.Clone()
	s.cfgMu.Unlock()

	for _, rldr := range s.reloaders {
		if rldr == nil {
			continue
		}
		if pr, ok := rldr.(ProbeConfigReloader); ok {
			pr.ReloadProbe(updated.Probe, updated.Targets)
		} else {
			rldr.ReloadTargets(updated.Targets)
		}
	}

	renderJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"message": "Configuration successfully saved and applied",
		"config":  updated,
	})
}
