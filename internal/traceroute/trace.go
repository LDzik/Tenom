package traceroute

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/storage"
)

type Engine struct {
	rootCtx          context.Context
	wg               *sync.WaitGroup
	cooldownDuration time.Duration
	incidentChan     chan<- storage.Incident
	sem              chan struct{}
	mu               sync.Mutex
	lastTriggered    map[string]time.Time
}

func NewEngine(rootCtx context.Context, wg *sync.WaitGroup, cooldown time.Duration, incidentChan chan<- storage.Incident) *Engine {
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	return &Engine{
		rootCtx:          rootCtx,
		wg:               wg,
		cooldownDuration: cooldown,
		incidentChan:     incidentChan,
		sem:              make(chan struct{}, 3),
		lastTriggered:    make(map[string]time.Time),
	}
}

func (e *Engine) Trigger(targetHost string, reason string) {
	if e.rootCtx.Err() != nil || config.ValidateHost(targetHost) != nil {
		return
	}

	now := time.Now()
	e.mu.Lock()
	for host, ts := range e.lastTriggered {
		if now.Sub(ts) >= e.cooldownDuration*2 {
			delete(e.lastTriggered, host)
		}
	}
	lastTime, exists := e.lastTriggered[targetHost]
	if exists && now.Sub(lastTime) < e.cooldownDuration {
		e.mu.Unlock()
		return
	}
	e.lastTriggered[targetHost] = now
	e.mu.Unlock()

	log.Printf("[Diagnostic] ANOMALY DETECTED on %s (%s). Launching traceroute...", targetHost, reason)

	if e.wg != nil {
		e.wg.Add(1)
	}
	go e.executeTrace(targetHost, reason)
}

func (e *Engine) executeTrace(targetHost string, reason string) {
	if e.wg != nil {
		defer e.wg.Done()
	}

	// Acquire concurrency semaphore (max 3 simultaneous OS traceroute subprocesses)
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	case <-e.rootCtx.Done():
		return
	}
	startTime := time.Now()

	ctx, cancel := context.WithTimeout(e.rootCtx, 45*time.Second)
	defer cancel()

	var cmd *exec.Cmd

	if runtime.GOOS == "windows" {
		// -d: Do not resolve addresses to hostnames (much faster)
		// -h 20: Maximum 20 hops
		// -w 1000: 1000ms timeout per reply
		cmd = exec.CommandContext(ctx, "tracert", "-d", "-h", "20", "-w", "1000", targetHost)
	} else {
		if _, err := exec.LookPath("traceroute"); err == nil {
			cmd = exec.CommandContext(ctx, "traceroute", "-n", "-m", "20", "-w", "1", "--", targetHost)
		} else {
			cmd = exec.CommandContext(ctx, "tracepath", "-n", "-m", "20", "--", targetHost)
		}
	}

	outputBytes, err := cmd.CombinedOutput()
	if e.rootCtx.Err() != nil {
		return
	}
	output := string(outputBytes)

	if err != nil && ctx.Err() == context.DeadlineExceeded {
		output += fmt.Sprintf("\n[Traceroute aborted: exceeded 45s timeout]")
	} else if err != nil {
		output += fmt.Sprintf("\n[Traceroute execution warning: %v]", err)
	}

	log.Printf("[Diagnostic] Traceroute finished for %s in %s. Saving incident snapshot...",
		targetHost, time.Since(startTime).Round(time.Millisecond))

	completedAt := time.Now()
	incident := storage.Incident{
		StartedAt:        startTime,
		ResolvedAt:       &completedAt,
		Target:           targetHost,
		TriggerReason:    reason,
		TracerouteOutput: output,
	}

	select {
	case e.incidentChan <- incident:
	default:
		log.Printf("[Diagnostic] Warning: incident channel full, dropped report for %s", targetHost)
	}
}