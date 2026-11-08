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
	mu          sync.Mutex
	cfg         config.ProbeConfig
	targets     []config.Target
	sampleChan  chan<- storage.PingSample
	traceEngine *traceroute.Engine
	ctx         context.Context
	workerWg    sync.WaitGroup
	cancels     []context.CancelFunc
}

func NewEngine(cfg config.ProbeConfig, targets []config.Target, sampleChan chan<- storage.PingSample, traceEngine *traceroute.Engine) *Engine {
	return &Engine{
		cfg:         cfg,
		targets:     targets,
		sampleChan:  sampleChan,
		traceEngine: traceEngine,
	}
}

func (e *Engine) Start(ctx context.Context, wg *sync.WaitGroup) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.ctx = ctx
	e.spawnWorkersLocked()

	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ctx.Done()
			e.mu.Lock()
			for _, cancel := range e.cancels {
				cancel()
			}
			e.mu.Unlock()
			e.workerWg.Wait()
		}()
	}
}

func (e *Engine) ReloadTargets(newTargets []config.Target) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, cancel := range e.cancels {
		cancel()
	}
	e.workerWg.Wait()
	e.cancels = nil

	e.targets = make([]config.Target, len(newTargets))
	copy(e.targets, newTargets)

	e.spawnWorkersLocked()
	log.Printf("[Probe] Targets reloaded: %d targets active", len(newTargets))
}

func (e *Engine) spawnWorkersLocked() {
	if e.ctx != nil && e.ctx.Err() == nil {
		e.cancels = make([]context.CancelFunc, 0, len(e.targets))
		for _, target := range e.targets {
			workerCtx, cancel := context.WithCancel(e.ctx)
			e.cancels = append(e.cancels, cancel)
			e.workerWg.Add(1)
			go e.runTargetWorker(workerCtx, target)
		}
	}
}

func (e *Engine) runTargetWorker(ctx context.Context, target config.Target) {
	defer e.workerWg.Done()

	ticker := time.NewTicker(time.Duration(e.cfg.IntervalSeconds) * time.Second)
	defer ticker.Stop()

	log.Printf("[Probe] Worker started for %s (%s)", target.Name, target.Host)

	e.executePing(ctx, target)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[Probe] Worker stopping for %s", target.Name)
			return
		case <-ticker.C:
			e.executePing(ctx, target)
		}
	}
}

func (e *Engine) executePing(ctx context.Context, target config.Target) {
	pinger, err := probing.NewPinger(target.Host)
	if err != nil {
		log.Printf("[Probe] [%s] Failed to resolve target host: %v", target.Name, err)
		return
	}

	pinger.SetPrivileged(true)
	pinger.Count = e.cfg.PacketCount
	pinger.Timeout = time.Duration(e.cfg.TimeoutSeconds) * time.Second

	err = pinger.RunWithContext(ctx)
	if ctx.Err() != nil {
		return
	}
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

