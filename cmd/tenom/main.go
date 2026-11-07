package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/LDzik/Tenom/internal/api"
	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/probe"
	"github.com/LDzik/Tenom/internal/storage"
	"github.com/LDzik/Tenom/internal/traceroute"
)

func main() {
	// Configuration
	configPath := flag.String("config", "config.json", "Path to configuration file")
	flag.Parse()

	log.Println("[Tenom] Starting network monitor...")

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[Tenom] Error loading configuration: %v", err)
	}

	// Database initialization
	db, err := storage.New(cfg.Database.Path)
	if err != nil {
		log.Fatalf("[Tenom] Storage initialization failed: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("[Tenom] Error closing database: %v", err)
		} else {
			log.Println("[Tenom] Database closed cleanly.")
		}
	}()

	// Root context with termination signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Periodic DB maintenance
	db.StartPruningWorker(ctx, cfg.Database.RetentionDays)

	// Outage & SLA engine
	outageTracker := probe.NewOutageTracker(cfg.Probe, cfg.Targets, db)
	db.SetOutageTracker(outageTracker)

	// Ingestion channels
	pingChan := make(chan storage.PingSample, 128)
	dnsChan := make(chan storage.DNSSample, 64)
	incidentChan := make(chan storage.Incident, 16)

	var wg sync.WaitGroup

	// Storage ingestion worker
	wg.Add(1)
	go func() {
		defer wg.Done()
		for pingChan != nil || dnsChan != nil || incidentChan != nil {
			select {
			case sample, ok := <-pingChan:
				if !ok {
					pingChan = nil
					continue
				}
				if err := db.InsertPingSample(sample); err != nil {
					log.Printf("[Storage] Failed to persist ping sample: %v", err)
				}
				outageTracker.RecordPing(sample)
			case sample, ok := <-dnsChan:
				if !ok {
					dnsChan = nil
					continue
				}
				if err := db.InsertDNSSample(sample); err != nil {
					log.Printf("[Storage] Failed to persist DNS sample: %v", err)
				}
			case incident, ok := <-incidentChan:
				if !ok {
					incidentChan = nil
					continue
				}
				if err := db.InsertIncident(incident); err != nil {
					log.Printf("[Storage] Failed to record incident: %v", err)
				} else {
					log.Printf("[Storage] Incident recorded for %s in database.", incident.Target)
				}
			}
		}
		log.Println("[Storage] All ingestion streams drained and closed.")
	}()

	// Diagnostic engines
	traceEngine := traceroute.NewEngine(3*time.Minute, incidentChan)

	probeEngine := probe.NewEngine(cfg.Probe, cfg.Targets, pingChan, traceEngine)
	probeEngine.Start(ctx, &wg)

	dnsEngine := probe.NewDNSEngine(cfg.DNS, dnsChan)
	dnsEngine.Start(ctx, &wg)

	// REST API HTTP server
	router := api.NewRouter(db, cfg)
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("[API] Server listening on http://localhost:%d", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[API] HTTP server fatal error: %v", err)
		}
	}()

	log.Printf("[Tenom] Running. Monitoring %d ping target(s) and %d DNS server(s). Press Ctrl+C to stop.",
		len(cfg.Targets), len(cfg.DNS.Servers))

	//
	<-ctx.Done()
	log.Println("[Tenom] Termination signal received. Stopping server and probe workers...")

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[API] Server shutdown warning: %v", err)
	} else {
		log.Println("[API] Server stopped cleanly.")
	}

	if err := outageTracker.Close(); err != nil {
		log.Printf("[Outage] Error closing outage tracker: %v", err)
	}

	go func() {
		time.Sleep(500 * time.Millisecond)
		close(pingChan)
		close(dnsChan)
		close(incidentChan)
	}()

	wg.Wait()
	log.Println("[Tenom] Shutdown complete.")
}
