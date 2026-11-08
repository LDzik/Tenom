package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type OutageStateProvider interface {
	GetNetworkState() (string, *time.Time, string)
	GetActiveOutageDuration() *float64
}

type Storage struct {
	db            *sql.DB
	trackerMu     sync.RWMutex
	outageTracker OutageStateProvider
}

func New(dbPath string) (*Storage, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// SQLite connection string with WAL and pragmas
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", dbPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	s := &Storage{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to apply migration: %w", err)
	}

	return s, nil
}

func (s *Storage) SetOutageTracker(tracker OutageStateProvider) {
	s.trackerMu.Lock()
	defer s.trackerMu.Unlock()
	s.outageTracker = tracker
}

func (s *Storage) getOutageTracker() OutageStateProvider {
	s.trackerMu.RLock()
	defer s.trackerMu.RUnlock()
	return s.outageTracker
}

func (s *Storage) migrate() error {
	query := `
	CREATE TABLE IF NOT EXISTS ping_samples (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		target TEXT NOT NULL,
		category TEXT NOT NULL,
		rtt_ms REAL NOT NULL,
		packet_loss REAL NOT NULL,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS dns_samples (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		server TEXT NOT NULL,
		domain TEXT NOT NULL,
		latency_ms REAL NOT NULL,
		status TEXT NOT NULL,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS incidents (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		started_at DATETIME NOT NULL,
		resolved_at DATETIME,
		target TEXT NOT NULL,
		trigger_reason TEXT NOT NULL,
		traceroute_output TEXT
	);

	CREATE TABLE IF NOT EXISTS outages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		started_at DATETIME NOT NULL,
		resolved_at DATETIME,
		duration_seconds REAL NOT NULL,
		failure_type TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_ping_samples_ts ON ping_samples(target, timestamp);
	CREATE INDEX IF NOT EXISTS idx_dns_samples_ts ON dns_samples(server, timestamp);
	CREATE INDEX IF NOT EXISTS idx_incidents_target ON incidents(target, started_at);
	CREATE INDEX IF NOT EXISTS idx_outages_started ON outages(started_at);
	`
	_, err := s.db.Exec(query)
	return err
}

func formatDBTime(t time.Time) string {
	if t.IsZero() {
		return time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func parseDBTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty timestamp string")
	}

	// SQLite standard datetime format or prefix: YYYY-MM-DD HH:MM:SS
	if len(s) >= 19 {
		cleaned := strings.Replace(s[:19], "T", " ", 1)
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", cleaned, time.UTC); err == nil {
			return t, nil
		}
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse timestamp: %s", s)
}

func (s *Storage) InsertPingSample(sample PingSample) error {
	query := `
	INSERT INTO ping_samples (target, category, rtt_ms, packet_loss, timestamp)
	VALUES (?, ?, ?, ?, ?)
	`
	ts := formatDBTime(sample.Timestamp)
	_, err := s.db.Exec(query, sample.Target, sample.Category, sample.RttMs, sample.PacketLoss, ts)
	return err
}

func (s *Storage) InsertDNSSample(sample DNSSample) error {
	query := `
	INSERT INTO dns_samples (server, domain, latency_ms, status, timestamp)
	VALUES (?, ?, ?, ?, ?)
	`
	ts := formatDBTime(sample.Timestamp)
	_, err := s.db.Exec(query, sample.Server, sample.Domain, sample.LatencyMs, sample.Status, ts)
	return err
}

func (s *Storage) InsertIncident(inc Incident) error {
	query := `
	INSERT INTO incidents (started_at, resolved_at, target, trigger_reason, traceroute_output)
	VALUES (?, ?, ?, ?, ?)
	`
	startTs := formatDBTime(inc.StartedAt)
	var resolvedTs *string
	if inc.ResolvedAt != nil {
		r := formatDBTime(*inc.ResolvedAt)
		resolvedTs = &r
	}
	_, err := s.db.Exec(query, startTs, resolvedTs, inc.Target, inc.TriggerReason, inc.TracerouteOutput)
	return err
}

func (s *Storage) InsertOutage(outage Outage) error {
	query := `
	INSERT INTO outages (started_at, resolved_at, duration_seconds, failure_type)
	VALUES (?, ?, ?, ?)
	`
	startTs := formatDBTime(outage.StartedAt)
	var resolvedTs *string
	if outage.ResolvedAt != nil {
		r := formatDBTime(*outage.ResolvedAt)
		resolvedTs = &r
	}
	_, err := s.db.Exec(query, startTs, resolvedTs, outage.DurationSeconds, outage.FailureType)
	return err
}

func (s *Storage) PruneOldSamples(retentionDays int) (int64, error) {
	queryPing := `DELETE FROM ping_samples WHERE datetime(substr(timestamp, 1, 19)) < datetime('now', printf('-%d days', ?));`
	res1, err := s.db.Exec(queryPing, retentionDays)
	if err != nil {
		return 0, err
	}
	n1, _ := res1.RowsAffected()

	queryDNS := `DELETE FROM dns_samples WHERE datetime(substr(timestamp, 1, 19)) < datetime('now', printf('-%d days', ?));`
	res2, err := s.db.Exec(queryDNS, retentionDays)
	if err != nil {
		return n1, err
	}
	n2, _ := res2.RowsAffected()

	return n1 + n2, nil
}

func (s *Storage) StartPruningWorker(ctx context.Context, retentionDays int) {
	ticker := time.NewTicker(6 * time.Hour)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				deleted, err := s.PruneOldSamples(retentionDays)
				if err != nil {
					log.Printf("[Storage] Prune worker error: %v", err)
				} else if deleted > 0 {
					log.Printf("[Storage] Pruned %d stale ping records", deleted)
				}
			}
		}
	}()
}

func (s *Storage) GetLatestSamples() ([]PingSample, error) {
	query := `
	SELECT id, target, category, rtt_ms, packet_loss, timestamp
	FROM (
		SELECT id, target, category, rtt_ms, packet_loss, timestamp,
		       ROW_NUMBER() OVER(PARTITION BY target ORDER BY id DESC) as rn
		FROM ping_samples
	)
	WHERE rn = 1
	ORDER BY target ASC
	`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest samples: %w", err)
	}
	defer rows.Close()

	var samples []PingSample
	for rows.Next() {
		var (
			smp   PingSample
			tsRaw string
		)
		if err := rows.Scan(&smp.ID, &smp.Target, &smp.Category, &smp.RttMs, &smp.PacketLoss, &tsRaw); err != nil {
			return nil, fmt.Errorf("failed to scan sample row: %w", err)
		}
		if t, err := parseDBTime(tsRaw); err == nil {
			smp.Timestamp = t
		}
		samples = append(samples, smp)
	}
	if samples == nil {
		samples = []PingSample{}
	}
	return samples, rows.Err()
}

func (s *Storage) GetDowntimeSeconds24h() (float64, error) {
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02 15:04:05")
	query := `
	SELECT started_at, resolved_at, duration_seconds
	FROM outages
	WHERE datetime(substr(resolved_at, 1, 19)) >= datetime(?)
	   OR resolved_at IS NULL
	`
	rows, err := s.db.Query(query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to query outages for 24h downtime: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	windowStart := now.Add(-24 * time.Hour)
	var totalDowntime float64

	for rows.Next() {
		var (
			startedRaw  string
			resolvedRaw sql.NullString
			duration    float64
		)
		if err := rows.Scan(&startedRaw, &resolvedRaw, &duration); err != nil {
			return 0, fmt.Errorf("failed to scan outage row: %w", err)
		}

		startedAt, err := parseDBTime(startedRaw)
		if err != nil {
			continue
		}

		resolvedAt := now
		if resolvedRaw.Valid && strings.TrimSpace(resolvedRaw.String) != "" {
			if r, err := parseDBTime(resolvedRaw.String); err == nil {
				resolvedAt = r
			}
		}

		effStart := startedAt
		if effStart.Before(windowStart) {
			effStart = windowStart
		}
		effEnd := resolvedAt
		if effEnd.After(now) {
			effEnd = now
		}

		diff := effEnd.Sub(effStart).Seconds()
		if diff > 0 {
			totalDowntime += diff
		}
	}
	return totalDowntime, rows.Err()
}

func (s *Storage) deduceState(samples []PingSample) string {
	if len(samples) == 0 {
		return "Healthy"
	}

	var (
		lanCount     int
		lanDownCount int
		wanCount     int
		wanDownCount int
		hasDegraded  bool
	)

	for _, sample := range samples {
		isLAN := strings.ToUpper(sample.Category) == "LAN"
		if isLAN {
			lanCount++
			if sample.PacketLoss >= 100.0 {
				lanDownCount++
			} else if sample.PacketLoss >= 10.0 || sample.RttMs >= 120.0 || sample.PacketLoss > 0 {
				hasDegraded = true
			}
		} else {
			wanCount++
			if sample.PacketLoss >= 100.0 {
				wanDownCount++
			} else if sample.PacketLoss >= 10.0 || sample.RttMs >= 120.0 {
				hasDegraded = true
			}
		}
	}

	if lanCount > 0 && lanDownCount == lanCount {
		return "Down"
	}
	if wanCount > 0 && wanDownCount == wanCount {
		return "Down"
	}
	if hasDegraded || wanDownCount > 0 || lanDownCount > 0 {
		return "Degraded"
	}
	return "Healthy"
}

func (s *Storage) GetLiveStatus() (*LiveStatus, error) {
	latestSamples, err := s.GetLatestSamples()
	if err != nil {
		return nil, fmt.Errorf("failed to get latest samples: %w", err)
	}

	resolvedDowntime, err := s.GetDowntimeSeconds24h()
	if err != nil {
		log.Printf("[Storage] Warning: error calculating 24h downtime: %v", err)
		resolvedDowntime = 0
	}

	state := "Healthy"
	var activeDuration *float64
	isActiveOutage := false

	tracker := s.getOutageTracker()
	if tracker != nil {
		netState, startedAt, _ := tracker.GetNetworkState()
		state = netState
		if startedAt != nil {
			isActiveOutage = true
			now := time.Now().UTC()
			windowStart := now.Add(-24 * time.Hour)

			dur := now.Sub(*startedAt).Seconds()
			if dur < 0 {
				dur = 0
			}
			activeDuration = &dur

			effStart := *startedAt
			if effStart.Before(windowStart) {
				effStart = windowStart
			}
			active24h := now.Sub(effStart).Seconds()
			if active24h > 0 {
				resolvedDowntime += active24h
			}
		}
	} else {
		state = s.deduceState(latestSamples)
	}

	// 24h uptime calculation: ((86400 - downtime_seconds) / 86400) * 100
	if resolvedDowntime > 86400 {
		resolvedDowntime = 86400
	}
	if resolvedDowntime < 0 {
		resolvedDowntime = 0
	}

	uptimePct := ((86400.0 - resolvedDowntime) / 86400.0) * 100.0
	uptimePct = math.Round(uptimePct*100) / 100.0

	return &LiveStatus{
		State:                state,
		Uptime24h:            uptimePct,
		UptimePercentage:     uptimePct,
		ActiveOutage:         isActiveOutage,
		ActiveOutageDuration: activeDuration,
		Targets:              latestSamples,
	}, nil
}

func (s *Storage) GetHistory(target string, timeRange string) ([]PingHistoryPoint, error) {
	var (
		bucketSeconds int
		duration      time.Duration
	)

	switch strings.ToLower(strings.TrimSpace(timeRange)) {
	case "1h":
		bucketSeconds = 60 // 1 minute
		duration = 1 * time.Hour
	case "7d":
		bucketSeconds = 1800 // 30 minutes
		duration = 7 * 24 * time.Hour
	case "24h":
		fallthrough
	default:
		bucketSeconds = 300 // 5 minutes
		duration = 24 * time.Hour
	}

	cutoff := time.Now().UTC().Add(-duration).Format("2006-01-02 15:04:05")

	var (
		query string
		rows  *sql.Rows
		err   error
	)

	if target != "" {
		query = `
		SELECT 
			datetime((CAST(strftime('%s', substr(timestamp, 1, 19)) AS INTEGER) / ?) * ?, 'unixepoch') AS bucket,
			ROUND(AVG(rtt_ms), 2) AS avg_rtt,
			ROUND(AVG(packet_loss), 2) AS avg_packet_loss
		FROM ping_samples
		WHERE target = ? AND datetime(substr(timestamp, 1, 19)) >= datetime(?)
		GROUP BY bucket
		ORDER BY bucket ASC
		`
		rows, err = s.db.Query(query, bucketSeconds, bucketSeconds, target, cutoff)
	} else {
		query = `
		SELECT 
			datetime((CAST(strftime('%s', substr(timestamp, 1, 19)) AS INTEGER) / ?) * ?, 'unixepoch') AS bucket,
			ROUND(AVG(rtt_ms), 2) AS avg_rtt,
			ROUND(AVG(packet_loss), 2) AS avg_packet_loss
		FROM ping_samples
		WHERE datetime(substr(timestamp, 1, 19)) >= datetime(?)
		GROUP BY bucket
		ORDER BY bucket ASC
		`
		rows, err = s.db.Query(query, bucketSeconds, bucketSeconds, cutoff)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query ping history: %w", err)
	}
	defer rows.Close()

	var points []PingHistoryPoint
	for rows.Next() {
		var (
			bucketRaw  string
			avgRtt     sql.NullFloat64
			packetLoss sql.NullFloat64
		)
		if err := rows.Scan(&bucketRaw, &avgRtt, &packetLoss); err != nil {
			return nil, fmt.Errorf("failed to scan ping history row: %w", err)
		}

		t, err := parseDBTime(bucketRaw)
		if err != nil {
			continue
		}

		points = append(points, PingHistoryPoint{
			Timestamp:  t,
			AvgRtt:     avgRtt.Float64,
			PacketLoss: packetLoss.Float64,
		})
	}
	if points == nil {
		points = []PingHistoryPoint{}
	}
	return points, rows.Err()
}

func (s *Storage) GetDNSHistory(timeRange string) ([]DNSHistoryPoint, error) {
	var (
		bucketSeconds int
		duration      time.Duration
	)

	switch strings.ToLower(strings.TrimSpace(timeRange)) {
	case "1h":
		bucketSeconds = 60 // 1 minute
		duration = 1 * time.Hour
	case "7d":
		bucketSeconds = 1800 // 30 minutes
		duration = 7 * 24 * time.Hour
	case "24h":
		fallthrough
	default:
		bucketSeconds = 300 // 5 minutes
		duration = 24 * time.Hour
	}

	cutoff := time.Now().UTC().Add(-duration).Format("2006-01-02 15:04:05")

	query := `
	SELECT 
		server,
		datetime((strftime('%s', substr(timestamp, 1, 19)) / ?) * ?, 'unixepoch') AS bucket,
		ROUND(AVG(latency_ms), 2) AS avg_latency
	FROM dns_samples
	WHERE datetime(substr(timestamp, 1, 19)) >= datetime(?)
	GROUP BY server, bucket
	ORDER BY bucket ASC, server ASC
	`
	rows, err := s.db.Query(query, bucketSeconds, bucketSeconds, cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to query dns history: %w", err)
	}
	defer rows.Close()

	var points []DNSHistoryPoint
	for rows.Next() {
		var (
			server     string
			bucketRaw  string
			avgLatency sql.NullFloat64
		)
		if err := rows.Scan(&server, &bucketRaw, &avgLatency); err != nil {
			return nil, fmt.Errorf("failed to scan dns history row: %w", err)
		}

		t, err := parseDBTime(bucketRaw)
		if err != nil {
			continue
		}

		points = append(points, DNSHistoryPoint{
			Server:    server,
			Timestamp: t,
			LatencyMs: avgLatency.Float64,
		})
	}
	if points == nil {
		points = []DNSHistoryPoint{}
	}
	return points, rows.Err()
}

func (s *Storage) GetTraceroutes(limit int) ([]Incident, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
	SELECT id, started_at, resolved_at, target, trigger_reason, traceroute_output
	FROM incidents
	ORDER BY id DESC
	LIMIT ?
	`
	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query traceroutes: %w", err)
	}
	defer rows.Close()

	var incidents []Incident
	for rows.Next() {
		var (
			inc         Incident
			startedRaw  string
			resolvedRaw sql.NullString
		)
		if err := rows.Scan(&inc.ID, &startedRaw, &resolvedRaw, &inc.Target, &inc.TriggerReason, &inc.TracerouteOutput); err != nil {
			return nil, fmt.Errorf("failed to scan incident row: %w", err)
		}
		if t, err := parseDBTime(startedRaw); err == nil {
			inc.StartedAt = t
		}
		if resolvedRaw.Valid && strings.TrimSpace(resolvedRaw.String) != "" {
			if t, err := parseDBTime(resolvedRaw.String); err == nil {
				inc.ResolvedAt = &t
			}
		}
		incidents = append(incidents, inc)
	}
	if incidents == nil {
		incidents = []Incident{}
	}
	return incidents, rows.Err()
}

func (s *Storage) GetOutages(limit int) ([]Outage, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
	SELECT id, started_at, resolved_at, duration_seconds, failure_type
	FROM outages
	ORDER BY id DESC
	LIMIT ?
	`
	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query outages: %w", err)
	}
	defer rows.Close()

	var outages []Outage
	for rows.Next() {
		var (
			out         Outage
			startedRaw  string
			resolvedRaw sql.NullString
		)
		if err := rows.Scan(&out.ID, &startedRaw, &resolvedRaw, &out.DurationSeconds, &out.FailureType); err != nil {
			return nil, fmt.Errorf("failed to scan outage row: %w", err)
		}
		if t, err := parseDBTime(startedRaw); err == nil {
			out.StartedAt = t
		}
		if resolvedRaw.Valid && strings.TrimSpace(resolvedRaw.String) != "" {
			if t, err := parseDBTime(resolvedRaw.String); err == nil {
				out.ResolvedAt = &t
			}
		}
		outages = append(outages, out)
	}
	if outages == nil {
		outages = []Outage{}
	}
	return outages, rows.Err()
}

func (s *Storage) GetIncidents(limit int) (*IncidentsResponse, error) {
	if limit <= 0 {
		limit = 50
	}

	traceroutes, err := s.GetTraceroutes(limit)
	if err != nil {
		return nil, err
	}

	outages, err := s.GetOutages(limit)
	if err != nil {
		return nil, err
	}

	return &IncidentsResponse{
		Traceroutes: traceroutes,
		Incidents:   traceroutes,
		Outages:     outages,
	}, nil
}

func (s *Storage) Close() error {
	return s.db.Close()
}
