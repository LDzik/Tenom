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
	mu            sync.Mutex
	cfg           config.ProbeConfig
	targets       []config.Target
	sampleChan    chan<- storage.PingSample
	traceEngine   *traceroute.Engine
	outageTracker *OutageTracker
	ctx           context.Context
	workerWg      sync.WaitGroup
	cancels       []context.CancelFunc
}

func NewEngine(cfg config.ProbeConfig, targets []config.Target, sampleChan chan<- storage.PingSample, traceEngine *traceroute.Engine, tracker ...*OutageTracker) *Engine {
	var ot *OutageTracker
	if len(tracker) > 0 {
		ot = tracker[0]
	}
	return &Engine{
		cfg:           cfg,
		targets:       targets,
		sampleChan:    sampleChan,
		traceEngine:   traceEngine,
		outageTracker: ot,
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
	cfg := e.cfg
	e.mu.Unlock()
	e.ReloadProbe(cfg, newTargets)
}

func (e *Engine) ReloadProbe(newCfg config.ProbeConfig, newTargets []config.Target) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, cancel := range e.cancels {
		cancel()
	}
	e.workerWg.Wait()
	e.cancels = nil

	if newCfg.IntervalSeconds > 0 {
		e.cfg = newCfg
	}
	e.targets = make([]config.Target, len(newTargets))
	copy(e.targets, newTargets)

	e.spawnWorkersLocked()
	log.Printf("[Probe] Targets reloaded: %d targets active", len(newTargets))
}

func (e *Engine) spawnWorkersLocked() {
	if e.ctx != nil && e.ctx.Err() == nil {
		if e.cfg.IntervalSeconds <= 0 {
			e.cfg.IntervalSeconds = 15
		}
		if e.cfg.PacketCount <= 0 {
			e.cfg.PacketCount = 3
		}
		if e.cfg.TimeoutSeconds <= 0 {
			e.cfg.TimeoutSeconds = 3
		}
		cfgSnapshot := e.cfg
		e.cancels = make([]context.CancelFunc, 0, len(e.targets))
		for _, target := range e.targets {
			workerCtx, cancel := context.WithCancel(e.ctx)
			e.cancels = append(e.cancels, cancel)
			e.workerWg.Add(1)
			go e.runTargetWorker(workerCtx, cfgSnapshot, target)
		}
	}
}

func (e *Engine) runTargetWorker(ctx context.Context, cfg config.ProbeConfig, target config.Target) {
	defer e.workerWg.Done()

	ticker := time.NewTicker(time.Duration(cfg.IntervalSeconds) * time.Second)
	defer ticker.Stop()

	log.Printf("[Probe] Worker started for %s (%s)", target.Name, target.Host)

	e.executePing(ctx, cfg, target)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[Probe] Worker stopping for %s", target.Name)
			return
		case <-ticker.C:
			e.executePing(ctx, cfg, target)
		}
	}
}

func (e *Engine) executePing(ctx context.Context, cfg config.ProbeConfig, target config.Target) {
	if ctx.Err() != nil {
		return
	}

	var sample storage.PingSample
	pinger, err := probing.NewPinger(target.Host)
	if err != nil {
		log.Printf("[Probe] [%s] Failed to resolve target host (%v); recording 100%% packet loss", target.Name, err)
		sample = storage.PingSample{
			Target:     target.Host,
			Category:   target.Category,
			RttMs:      0,
			JitterMs:   0,
			PacketLoss: 100.0,
			Timestamp:  time.Now(),
		}
	} else {
		pinger.SetPrivileged(true)
		pinger.Count = cfg.PacketCount
		pinger.Timeout = time.Duration(cfg.TimeoutSeconds) * time.Second

		err = pinger.RunWithContext(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("[Probe] [%s] Ping execution error (%v); recording 100%% packet loss", target.Name, err)
			sample = storage.PingSample{
				Target:     target.Host,
				Category:   target.Category,
				RttMs:      0,
				JitterMs:   0,
				PacketLoss: 100.0,
				Timestamp:  time.Now(),
			}
		} else {
			stats := pinger.Statistics()
			rttMs := float64(stats.AvgRtt.Microseconds()) / 1000.0
			jitterMs := float64(stats.StdDevRtt.Microseconds()) / 1000.0

			sample = storage.PingSample{
				Target:     target.Host,
				Category:   target.Category,
				RttMs:      rttMs,
				JitterMs:   jitterMs,
				PacketLoss: stats.PacketLoss,
				Timestamp:  time.Now(),
			}
		}
	}

	log.Printf("[Probe] %-16s (%-15s) | Avg RTT: %6.2fms | Jitter: %5.2fms | Loss: %5.1f%%",
		target.Name, target.Host, sample.RttMs, sample.JitterMs, sample.PacketLoss)

	// Evaluate SLA state immediately so disk I/O backpressure cannot delay outage detection
	if e.outageTracker != nil {
		e.outageTracker.RecordPing(sample)
	}

	select {
	case e.sampleChan <- sample:
	default:
		log.Printf("[Probe] Warning: sample buffer full, dropping metric for %s", target.Name)
	}

	if target.Category != "LAN" && e.traceEngine != nil {
		if sample.PacketLoss >= cfg.LossThresholdPct {
			reason := fmt.Sprintf("Packet loss %.1f%% >= threshold %.1f%%", sample.PacketLoss, cfg.LossThresholdPct)
			e.traceEngine.Trigger(target.Host, reason)
		} else if cfg.JitterThresholdMs > 0 && (sample.JitterMs >= cfg.JitterThresholdMs || sample.RttMs >= cfg.JitterThresholdMs) {
			reason := fmt.Sprintf("Latency/Jitter (RTT %.2fms, Jitter %.2fms) >= threshold %.2fms",
				sample.RttMs, sample.JitterMs, cfg.JitterThresholdMs)
			e.traceEngine.Trigger(target.Host, reason)
		}
	}
}

