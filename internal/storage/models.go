package storage

import "time"

type PingSample struct{
	ID			int64		`json:"id"`
	Target		string		`json:"target"`
	Category	string		`json:"category"`
	RttMs		float64		`json:"rtt_ms"`
	PacketLoss	float64		`json:"packet_loss"`
	Timestamp	time.Time	`json:"timestamp"`
}
type Incident struct {
	ID					int64		`json:"id"`
	StartedAt        	time.Time	`json:"started_at"`
	ResolvedAt			*time.Time	`json:"resolved_at,omitempty"`
	Target				string		`json:"target"`
	TriggerReason		string		`json:"trigger_reason"`
	TracerouteOutput	string		`json:"traceroute_output"`
}