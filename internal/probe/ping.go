package probe

import(
	"context"
	"log"
	"sync"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/storage"
)

type Engine struct {
	cfg			config.ProbeConfig
	targets		[]config.Target
	sampleChan	chan<- storage.PingSample
}

func NewEngine(cfg config.ProbeConfig, targets []config.Target, sampleChan chan<- storage.PingSample) *Engine{
	return &Engine{
		cfg:		cfg,
		targets: 	targets,
		sampleChan: sampleChan,
	}
}

func (e *Engine) Start(ctx context.Context, wg *sync.WaitGroup) {
	for _, target := range e.targets {
		wg.Add(1)
		go e.RunTargetWorker(ctx, wg, target)
	}
}

func (e *Engine) RunTargetWorker(ctx context.Context, wg *sync.WaitGroup, target config.Target){
	defer wg.Done()

	ticker := time.NewTicker(time.Duration(e.cfg.IntervalSeconds) * time.Second)
	defer ticker.Stop()

	log.Printf("[Probe] Worker started for %s (%s)", target.Name, target.Host)

	e.ExecutePing(target)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[Probe] Worker stopping for %s", target.Name)
			return
		case <-ticker.C:
			e.ExecutePing(target)
		}
	}
}


func (e *Engine) ExecutePing(target config.Target) {
	pinger, err := probing.NewPinger(target.Host)
	if err != nil {
		log.Printf("[Probe] [%s] Failed to resolve target host: %v", target.Name, err)
		return
	}

	pinger.SetPrivileged(true)
	pinger.Count = e.cfg.PacketCount
	pinger.Timeout = time.Duration(e.cfg.TimeoutSeconds) * time.Second

	err = pinger.Run()
	if err != nil {
		log.Printf("[Probe] [%s] Ping execution error: %v", target.Name, err)
		return
	}

	stats := pinger.Statistics()
	rttMs := float64(stats.AvgRtt.Microseconds()) / 1000.0

	sample := storage.PingSample{
		Target:     target.Host,
		Category:   target.Category,
		RttMs:      rttMs,
		PacketLoss: stats.PacketLoss,
		Timestamp:  time.Now(),
	}

	log.Printf("[Probe] %-16s (%-15s) | Avg RTT: %6.2fms | Loss: %5.1f%%", target.Name, target.Host, sample.RttMs, sample.PacketLoss)

	select {
	case e.sampleChan <- sample:
	default:
		log.Printf("[Probe] Warning: sample buffer full, dropping metric for %s", target.Name)
	}
}

