package probe 

import(
	"context"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/LDzik/Tenom/internal/config"
	"github.com/LDzik/Tenom/internal/storage"
)

type DNSEngine struct {
	cfg			config.DNSConfig
	sampleChan	chan<- storage.DNSSample
}

func NewDNSEngine(cfg config.DNSConfig, sampleChan chan<- storage.DNSSample) *DNSEngine {
	return &DNSEngine{
		cfg:		cfg,
		sampleChan:	sampleChan,
	}
}

func (d *DNSEngine) Start(ctx context.Context, wg *sync.WaitGroup) {
	if !d.cfg.Enabled || len(d.cfg.Servers) == 0 {
		log.Println("[DNS] Benchmarking disabled or no servers configured.")
		return
	}

	wg.Add(1)
	go d.runWorker(ctx, wg)
}

func (d *DNSEngine) runWorker(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	log.Printf("[DNS] Engine started. Benchmarking %d server(s) every %ds (resolving %s)",
		len(d.cfg.Servers), d.cfg.IntervalSeconds, d.cfg.Domain)

	d.executeBenchmark(ctx)

	ticker := time.NewTicker(time.Duration(d.cfg.IntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[DNS] Benchmark engine stopping...")
			return
		case <-ticker.C:
			d.executeBenchmark(ctx)
		}
	}
}

func (d *DNSEngine) executeBenchmark(ctx context.Context) {
	var benchmarkWg sync.WaitGroup

	for _, srv := range d.cfg.Servers {
		benchmarkWg.Add(1)
		go func(server config.DNSServer) {
			defer benchmarkWg.Done()
			d.queryServer(ctx, server)
		}(srv)
	}

	benchmarkWg.Wait()
}

func (d *DNSEngine) queryServer(ctx context.Context, server config.DNSServer) {
	targetAddr := server.Host
	if !strings.Contains(targetAddr, ":") {
		targetAddr = net.JoinHostPort(targetAddr, "53")
	}

	timeout := time.Duration(d.cfg.TimeoutSeconds) * time.Second

	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(dialCtx context.Context, network, address string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: timeout}
			return dialer.DialContext(dialCtx, "udp", targetAddr)
		},
	}

	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	_, err := resolver.LookupHost(queryCtx, d.cfg.Domain)
	latency := float64(time.Since(start).Microseconds()) / 1000.0

	status := "OK"
	if err != nil {
		status = "FAIL"
		log.Printf("[DNS] %-16s (%-15s) | FAIL (%6.2fms) | err: %v", server.Name, server.Host, latency, err)
	} else {
		log.Printf("[DNS] %-16s (%-15s) | Lookup: %6.2fms | Domain: %s",
			server.Name, server.Host, latency, d.cfg.Domain)
	}

	sample := storage.DNSSample{
		Server:    server.Name,
		Domain:    d.cfg.Domain,
		LatencyMs: latency,
		Status:    status,
		Timestamp: time.Now(),
	}

	select {
	case d.sampleChan <- sample:
	default:
		log.Printf("[DNS] Warning: sample buffer full, dropping sample for %s", server.Name)
	}
}