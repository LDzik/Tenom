package probe

import (
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/storage"
)

type NetworkState string

const (
	StateHealthy	NetworkState = "Healthy"
	StateDegraded	NetworkState = "Degraded"
	StateDown		NetworkState = "Down"
)

const (
	FailureGatewayDrop	= "GATEWAY_DROP"
	FailureWANOutage	= "WAN_OUTAGE"
)

type OutageStorage interface {
	InsertOutage(outage storage.Outage) error
	GetDowntimeSeconds24h() (float64, error)
}

type ActiveOutage struct {
	StartedAt   time.Time `json:"started_at"`
	FailureType string    `json:"failure_type"`
}

type OutageTracker struct {
	mu            sync.RWMutex
	cfg           config.ProbeConfig
	targets       map[string]config.Target
	latestSamples map[string]storage.PingSample
	currentState  NetworkState
	activeOutage  *ActiveOutage
	store         OutageStorage
}

func NewOutageTracker(cfg config.ProbeConfig, targets []config.Target, store OutageStorage) *OutageTracker {
	targetMap := make(map[string]config.Target, len(targets))
	for _, t := range targets {
		targetMap[t.Host] = t
	}

	return &OutageTracker{
		cfg:           cfg,
		targets:       targetMap,
		latestSamples: make(map[string]storage.PingSample),
		currentState:  StateHealthy,
		store:         store,
	}
}

func (o *OutageTracker) evaluateState() (NetworkState, string) {
	if len(o.latestSamples) == 0 {
		return StateHealthy, ""
	}

	var (
		lanCount     int
		lanDownCount int
		wanCount     int
		wanDownCount int
		hasDegraded  bool
	)

	lossThreshold := o.cfg.LossThresholdPct
	if lossThreshold <= 0 {
		lossThreshold = 10.0
	}
	jitterThreshold := o.cfg.JitterThresholdMs
	if jitterThreshold <= 0 {
		jitterThreshold = 120.0
	}

	for host, sample := range o.latestSamples {
		tgt, exists := o.targets[host]
		isLAN := false
		if exists && strings.ToUpper(tgt.Category) == "LAN" {
			isLAN = true
		} else if strings.ToUpper(sample.Category) == "LAN" {
			isLAN = true
		}

		if isLAN {
			lanCount++
			if sample.PacketLoss >= 100.0 {
				lanDownCount++
			} else if sample.PacketLoss >= lossThreshold || (jitterThreshold > 0 && sample.RttMs >= jitterThreshold) || sample.PacketLoss > 0 {
				hasDegraded = true
			}
		} else {
			wanCount++
			if sample.PacketLoss >= 100.0 {
				wanDownCount++
			} else if sample.PacketLoss >= lossThreshold || (jitterThreshold > 0 && sample.RttMs >= jitterThreshold) {
				hasDegraded = true
			}
		}
	}

	if lanCount > 0 && lanDownCount == lanCount {
		return StateDown, FailureGatewayDrop
	}

	if wanCount > 0 && wanDownCount == wanCount {
		return StateDown, FailureWANOutage
	}

	if hasDegraded || wanDownCount > 0 || lanDownCount > 0 {
		return StateDegraded, ""
	}

	return StateHealthy, ""
}

func (o *OutageTracker) RecordPing(sample storage.PingSample) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.latestSamples[sample.Target] = sample
	newState, failureType := o.evaluateState()

	now := sample.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	if newState == StateDown {
		if o.activeOutage == nil {
			o.activeOutage = &ActiveOutage{
				StartedAt:   now,
				FailureType: failureType,
			}
			log.Printf("[Outage] Network OUTAGE detected! Type: %s, Started at: %s",
				failureType, now.Format(time.RFC3339))
		} else if failureType == FailureGatewayDrop && o.activeOutage.FailureType != FailureGatewayDrop {
			o.activeOutage.FailureType = FailureGatewayDrop
		}
	} else {
		if o.activeOutage != nil {
			startedAt := o.activeOutage.StartedAt
			duration := now.Sub(startedAt).Seconds()
			if duration < 0 {
				duration = 0
			}
			resolvedAt := now
			ft := o.activeOutage.FailureType
			o.activeOutage = nil

			log.Printf("[Outage] Connection recovered! Total downtime: %.1fs (Type: %s)", duration, ft)

			if o.store != nil {
				record := storage.Outage{
					StartedAt:       startedAt,
					ResolvedAt:      &resolvedAt,
					DurationSeconds: duration,
					FailureType:     ft,
				}
				go func(rec storage.Outage) {
					if err := o.store.InsertOutage(rec); err != nil {
						log.Printf("[Outage] Failed to save resolved outage: %v", err)
					}
				}(record)
			}
		}
	}

	o.currentState = newState
}

func (o *OutageTracker) GetNetworkState() (string, *time.Time, string) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if o.activeOutage != nil {
		t := o.activeOutage.StartedAt
		return string(o.currentState), &t, o.activeOutage.FailureType
	}
	return string(o.currentState), nil, ""
}

func (o *OutageTracker) GetActiveOutageDuration() *float64 {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if o.activeOutage != nil {
		dur := time.Since(o.activeOutage.StartedAt).Seconds()
		if dur < 0 {
			dur = 0
		}
		return &dur
	}
	return nil
}

// Compute24hUptime calculates the real-time 24h uptime percentage:
// ((86400 - downtime_seconds) / 86400) * 100
func (o *OutageTracker) Compute24hUptime() (float64, error) {
	var totalDowntime float64

	if o.store != nil {
		d, err := o.store.GetDowntimeSeconds24h()
		if err != nil {
			return 0, err
		}
		totalDowntime = d
	}

	o.mu.RLock()
	if o.activeOutage != nil {
		now := time.Now().UTC()
		windowStart := now.Add(-24 * time.Hour)
		effStart := o.activeOutage.StartedAt
		if effStart.Before(windowStart) {
			effStart = windowStart
		}
		active24h := now.Sub(effStart).Seconds()
		if active24h > 0 {
			totalDowntime += active24h
		}
	}
	o.mu.RUnlock()

	if totalDowntime > 86400 {
		totalDowntime = 86400
	}
	if totalDowntime < 0 {
		totalDowntime = 0
	}

	uptime := ((86400.0 - totalDowntime) / 86400.0) * 100.0
	return math.Round(uptime*100) / 100.0, nil
}

func (o *OutageTracker) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.activeOutage != nil {
		now := time.Now().UTC()
		startedAt := o.activeOutage.StartedAt
		duration := now.Sub(startedAt).Seconds()
		if duration < 0 {
			duration = 0
		}
		ft := o.activeOutage.FailureType
		o.activeOutage = nil

		if o.store != nil {
			record := storage.Outage{
				StartedAt:       startedAt,
				ResolvedAt:      &now,
				DurationSeconds: duration,
				FailureType:     ft,
			}
			return o.store.InsertOutage(record)
		}
	}
	return nil
}
