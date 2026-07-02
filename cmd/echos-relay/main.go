// Command echos-relay is the zero-knowledge relay server: a public-key
// directory plus a TTL-bound mailbox/blob store.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/heurema/echos/internal/relayserver"
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOrDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func main() {
	addr := flag.String("addr", envOrDefault("ECHOS_RELAY_ADDR", ":8080"), "listen address")
	dbPath := flag.String("db", envOrDefault("ECHOS_RELAY_DB", "echos-relay.db"), "bbolt database path")
	ttl := flag.Duration("ttl", time.Duration(envIntOrDefault("ECHOS_RELAY_TTL_SECONDS", int(relayserver.DefaultTTL.Seconds())))*time.Second, "default mailbox TTL")
	maxBlobSize := flag.Int64("max-blob-size", int64(envIntOrDefault("ECHOS_RELAY_MAX_BLOB_SIZE", relayserver.DefaultMaxBlobSize)), "max envelope size in bytes")
	rateLimit := flag.Int("rate-limit", envIntOrDefault("ECHOS_RELAY_RATE_LIMIT", relayserver.DefaultRateLimit), "requests/minute per source IP for POST /keys and POST /mailbox/{fpr}")
	sweepInterval := flag.Duration("sweep-interval", time.Minute, "how often to purge expired blobs")
	flag.Parse()

	store, err := relayserver.OpenStore(*dbPath)
	if err != nil {
		log.Fatalf("open store %s: %v", *dbPath, err)
	}
	defer store.Close()

	srv := relayserver.New(store, relayserver.Config{
		TTL:         *ttl,
		MaxBlobSize: *maxBlobSize,
		RateLimit:   *rateLimit,
	})

	stop := make(chan struct{})
	defer close(stop)
	store.StartSweeper(*sweepInterval, time.Now, stop)

	fmt.Fprintf(os.Stderr, "echos-relay listening on %s (ttl=%s max-blob-size=%d rate-limit=%d/min)\n",
		*addr, *ttl, *maxBlobSize, *rateLimit)
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
