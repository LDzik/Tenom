package storage

import(
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Storage struct{
	db *sql.DB
}

func New(dbPath string) (*Storage, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("Fauked ti create database durectiry: %w", err)
	}

	// SQLite connection string with WAL and pragmas
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", dbPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("Failed to open sqlite database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 3 * time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("Failed to ping database: %w", err)
	}

	s := &Storage{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("Failed to apply migration: %w", err)
	}


	return s, nil
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

	CREATE INDEX IF NOT EXISTS idx_ping_samples_ts ON ping_samples(target, timestamp);
	CREATE INDEX IF NOT EXISTS idx_dns_samples_ts ON dns_samples(server, timestamp);
	CREATE INDEX IF NOT EXISTS idx_incidents_target ON incidents(target, started_at);
	`
	_, err := s.db.Exec(query)
	return err
}

func (s *Storage) InsertPingSample(sample PingSample) error {
	query := `
	INSERT INTO ping_samples (target, category, rtt_ms, packet_loss, timestamp)
	VALUES (?, ?, ?, ?, COALESCE(NULLIF(?, '0001-01-01 00:00:00+00:00'), CURRENT_TIMESTAMP))
	`
	_, err := s.db.Exec(query, sample.Target, sample.Category, sample.RttMs, sample.PacketLoss, sample.Timestamp)
	return err
}

func (s *Storage) InsertDNSSample(sample DNSSample) error {
	query := `
	INSERT INTO dns_samples (server, domain, latency_ms, status, timestamp)
	VALUES (?, ?, ?, ?, COALESCE(NULLIF(?, '0001-01-01 00:00:00+00:00'), CURRENT_TIMESTAMP))
	`
	_, err := s.db.Exec(query, sample.Server, sample.Domain, sample.LatencyMs, sample.Status, sample.Timestamp)
	return err
}

func (s *Storage) InsertIncident(inc Incident) error {
	query := `
	INSERT INTO incidents (started_at, resolved_at, target, trigger_reason, traceroute_output)
	VALUES (?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query, inc.StartedAt, inc.ResolvedAt, inc.Target, inc.TriggerReason, inc.TracerouteOutput)
	return err
}

func (s *Storage) PruneOldSamples(retentionDays int) (int64, error) {
	query := `
	DELETE FROM ping_samples WHERE timestamp < datetime('now', printf('-%d days', ?))
	DELETE FROM dns_samples WHERE timestamp < datetime('now', printf('-%d days', ?));
	`
	
	res, err := s.db.Exec(query, retentionDays)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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

func (s *Storage) Close() error {
	return s.db.Close()
}