package traceroute

import(
	"context"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/LDzik/Tenom/internal/storage"
)

type Engine struct {
	cooldownDuration	time.Duration
	incidentChan		chan<- storage.Incident
	mu					sync.Mutex
	lastTriggered		map[string]time.Time
}

func NewEngine(cooldown time.Duration, incidentChan chan<- storage.Incident) *Engine {
	return &Engine{
		cooldownDuration:	cooldown,
		incidentChan:		incidentChan,
		lastTriggered:		make(map[string]time.Time),
	}
}

func (e *Engine) Trigger(targetHost string, reason string) {
	e.mu.Lock()
	lastTime, exists := e.lastTriggered[targetHost]
	if exists && time.Since(lastTime) < e.cooldownDuration {
		e.mu.Unlock()
		return
	}
	e.lastTriggered[targetHost] = time.Now()
	e.mu.Unlock()

	log.Printf("[Diagnostic] ANOMALY DETECTED on %s (%s). Launching traceroute...", targetHost, reason)

	go e.executeTrace(targetHost, reason)
}

func (e *Engine) executeTrace(targetHost string, reason string) {
	startTime := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 45 * time.Second)
	defer cancel()

	var cmd *exec.Cmd

	if runtime.GOOS == "windows" {
		// -d: Do not resolve addresses to hostnames (much faster)
		// -h 20: Maximum 20 hops
		// -w 1000: 1000ms timeout per reply
		cmd = exec.CommandContext(ctx, "tracert", "-d", "-h", "20", "-w", "1000", targetHost)
	} else {
		if _, err := exec.LookPath("traceroute"); err == nil {
			cmd = exec.CommandContext(ctx, "traceroute", "-n", "-m", "20", "-w", "1", targetHost)
		} else {
			cmd = exec.CommandContext(ctx, "tracepath", "-n", "-m", "20", targetHost)
		}
	}

	outputBytes, err := cmd.CombinedOutput()
	output := string(outputBytes)

	if err != nil && ctx.Err() == context.DeadlineExceeded {
		output += fmt.Sprintf("\n[Traceroute aborted: exceeded 45s timeout]")
	} else if err != nil {
		output += fmt.Sprintf("\n[Traceroute execution warning: %v]", err)
	}

	log.Printf("[Diagnostic] Traceroute finished for %s in %s. Saving incident snapshot...",
		targetHost, time.Since(startTime).Round(time.Millisecond))

	incident := storage.Incident{
		StartedAt:			startTime,
		Target:				targetHost,
		TriggerReason:		reason,
		TracerouteOutput:	output,
	}

	select {
	case e.incidentChan <- incident:
	default:
		log.Printf("[Diagnostic] Warning: incident channel full, dropped report for %s", targetHost)
	}
}