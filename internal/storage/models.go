package storage

import "time"

type PingSample struct {
	ID         int64     `json:"id"`
	Target     string    `json:"target"`
	Category   string    `json:"category"`
	RttMs      float64   `json:"rtt_ms"`
	PacketLoss float64   `json:"packet_loss"`
	Timestamp  time.Time `json:"timestamp"`
}

type DNSSample struct {
	ID        int64     `json:"id"`
	Server    string    `json:"server"`
	Domain    string    `json:"domain"`
	LatencyMs float64   `json:"latency_ms"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

type Incident struct {
	ID               int64      `json:"id"`
	StartedAt        time.Time  `json:"started_at"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
	Target           string     `json:"target"`
	TriggerReason    string     `json:"trigger_reason"`
	TracerouteOutput string     `json:"traceroute_output"`
}

type Outage struct {
	ID              int64      `json:"id"`
	StartedAt       time.Time  `json:"started_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	DurationSeconds float64    `json:"duration_seconds"`
	FailureType     string     `json:"failure_type"`
}

type LiveStatus struct {
	State                string       `json:"state"`
	Uptime24h            float64      `json:"uptime_24h"`
	UptimePercentage     float64      `json:"uptime_percentage"`
	ActiveOutage         bool         `json:"active_outage"`
	ActiveOutageDuration *float64     `json:"active_outage_duration_seconds,omitempty"`
	Targets              []PingSample `json:"targets"`
}

type PingHistoryPoint struct {
	Timestamp  time.Time `json:"timestamp"`
	AvgRtt     float64   `json:"avg_rtt"`
	PacketLoss float64   `json:"packet_loss"`
}

type DNSHistoryPoint struct {
	Server    string    `json:"server"`
	Timestamp time.Time `json:"timestamp"`
	LatencyMs float64   `json:"latency_ms"`
}

type IncidentsResponse struct {
	Traceroutes []Incident `json:"traceroutes"`
	Incidents   []Incident `json:"incidents"`
	Outages     []Outage   `json:"outages"`
}
