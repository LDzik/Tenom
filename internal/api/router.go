package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/storage"
)

type Server struct {
	storage *storage.Storage
	cfg     *config.Config
}

func NewRouter(store *storage.Storage, cfg *config.Config) http.Handler {
	s := &Server{
		storage: store,
		cfg:     cfg,
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
		r.Get("/metrics/dns", s.handleDNSMetrics)
		r.Get("/incidents", s.handleIncidents)
	})

	return r
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
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

	if target == "" && s.cfg != nil && len(s.cfg.Targets) > 0 {
		target = s.cfg.Targets[0].Host
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

// GET /api/v1/metrics/dns?range=1h|24h -> DNS latency time-series.
func (s *Server) handleDNSMetrics(w http.ResponseWriter, r *http.Request) {
	timeRange := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("range")))
	if timeRange == "" {
		timeRange = "24h"
	}

	switch timeRange {
	case "1h", "24h":
		// valid
	default:
		renderError(w, http.StatusBadRequest, "Invalid range parameter. Valid values: 1h, 24h")
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
