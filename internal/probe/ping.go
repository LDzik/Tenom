package probe

import(
	"context"
	"log"
	"fmt"
	"sync"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/storage"
	"github.com/LDzik/Tenom/internal/traceroute"
)

type Engine struct {
	cfg			config.ProbeConfig
	targets		[]config.Target
	sampleChan	chan<- storage.PingSample
	traceEngine	*traceroute.Engine
}

func NewEngine(cfg config.ProbeConfig, targets []config.Target, sampleChan chan<- storage.PingSample, traceEngine *traceroute.Engine) *Engine{
	return &Engine{
		cfg:			cfg,
		targets: 		targets,
		sampleChan: 	sampleChan,
		traceEngine:	traceEngine,
	}
}

func (e *Engine) Start(ctx context.Context, wg *sync.WaitGroup) {
	for _, target := range e.targets {
		wg.Add(1)
		go e.runTargetWorker(ctx, wg, target)
	}
}

func (e *Engine) runTargetWorker(ctx context.Context, wg *sync.WaitGroup, target config.Target){
	defer wg.Done()

	ticker := time.NewTicker(time.Duration(e.cfg.IntervalSeconds) * time.Second)
	defer ticker.Stop()

	log.Printf("[Probe] Worker started for %s (%s)", target.Name, target.Host)

	e.executePing(target)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[Probe] Worker stopping for %s", target.Name)
			return
		case <-ticker.C:
			e.executePing(target)
		}
	}
}


func (e *Engine) executePing(target config.Target) {
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

	if target.Category != "LAN" && e.traceEngine != nil {
		if sample.PacketLoss >= e.cfg.LossThresholdPct {
			reason := fmt.Sprintf("Packet loss %.1f%% >= threshold %.1f%%", sample.PacketLoss, e.cfg.LossThresholdPct)
			e.traceEngine.Trigger(target.Host, reason)
		} else if e.cfg.JitterThresholdMs > 0 && sample.RttMs >= e.cfg.JitterThresholdMs {
			reason := fmt.Sprintf("RTT %.2fms >= threshold %.2fms", sample.RttMs, e.cfg.JitterThresholdMs)
			e.traceEngine.Trigger(target.Host, reason)
		}
	}
}

