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
)

func main() {
	configPath := flag.String("config", "config.json", "Path to configuration file")
	flag.Parse()

	log.Println("[Tenom] Starting network monitor...")

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[Tenom] Error loading confuguration: %v", err)
	}

	log.Printf("[Tenom] Loaded %d target(s) from %s", len(cfg.Targets), *configPath)
	for _, target := range cfg.Targets {
		log.Printf(" -> Target: %-16s | Host: %-15s | Category: %s", target.Name, target.Host, target.Category)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("[Tenom] Core initialized. Awaiting shutdown signal or engine hook...")

	<-ctx.Done()

	log.Println("[Tenom] Shutdown signal received. Performing cleanup...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5 * time.Second)
	defer cancel()

	<-shutdownCtx.Done()
	log.Println("[Tenom] Procss exited cleanly.")
}