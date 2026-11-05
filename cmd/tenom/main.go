package main

import(
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/probe"
	"github.com/LDzik/Tenom/internal/storage"
)

func main() {
	// config
	configPath := flag.String("config", "config.json", "Path to configuration file")
	flag.Parse()

	log.Println("[Tenom] Starting network monitor...")

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[Tenom] Error loading confuguration: %v", err)
	}

	// db
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

	// ctx
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// db maintenance
	db.StartPruningWorker(ctx, cfg.Database.RetentionDays)

	// probe + dns
	pingChan := make(chan storage.PingSample, 128)
	dnsChan := make(chan storage.DNSSample, 64)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for pingChan != nil || dnsChan != nil {
			select {
			case sample, ok := <-pingChan:
				if !ok {
					pingChan = nil
					continue
				}
				if err := db.InsertPingSample(sample); err != nil {
					log.Printf("[Storage] Failed to persist ping sample: %v", err)
				}
			case sample, ok := <-dnsChan:
				if !ok {
					dnsChan = nil
					continue
				}
				if err := db.InsertDNSSample(sample); err != nil {
					log.Printf("[Storage] Failed to persist DNS sample: %v", err)
				}
			}
		}
		log.Println("[Storage] All ingestion streams drained and closed.")
	}()

	probeEngine := probe.NewEngine(cfg.Probe, cfg.Targets, pingChan)
	probeEngine.Start(ctx, &wg)

	dnsEngine := probe.NewDNSEngine(cfg.DNS, dnsChan)
	dnsEngine.Start(ctx, &wg)

	log.Printf("[Tenom] Running. Monitoring %d ping target(s) and %d DNS server(s). Press Ctrl+C to stop.",
		len(cfg.Targets), len(cfg.DNS.Servers))


	// 
	<-ctx.Done()
	log.Println("[Tenom] Termination signal received. Stopping workers...")
	
	go func() {
		time.Sleep(500 * time.Millisecond)
		close(pingChan)
		close(dnsChan)
	}()

	wg.Wait()
	log.Println("[Tenom] Shutdown complete.")
}