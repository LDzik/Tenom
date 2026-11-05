package main

import(
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/storage"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to configuration file")
	flag.Parse()

	log.Println("[Tenom] Starting network monitor...")

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[Tenom] Error loading confuguration: %v", err)
	}

	log.Printf("[Tenom] Initializing storage at %s...", cfg.Database.Path)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db.StartPruningWorker(ctx, cfg.Database.RetentionDays)

	err = db.InsertPingSample(storage.PingSample{
		Target:     "1.1.1.1",
		Category:   "Public Anycast",
		RttMs:      12.45,
		PacketLoss: 0.0,
		Timestamp:  time.Now(),
	})
	if err != nil {
		log.Printf("[Tenom] Warning: Sample write test failed: %v", err)
	} else {
		log.Println("[Tenom] Sample write test succeeded.")
	}


	log.Printf("[Tenom] Core initialized. Awaiting shutdown signal or engine hook...")

	<-ctx.Done()
	log.Println("[Tenom] Shutdown signal received.")
}