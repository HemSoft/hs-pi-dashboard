// Package agent serves a JSON snapshot of this machine's pi sessions so the
// fleet dashboard can aggregate it.
package agent

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/HemSoft/hs-pi-dashboard/internal/sessions"
	"github.com/HemSoft/hs-pi-dashboard/internal/usage"
)

// Options configures one agent.
type Options struct {
	Addr         string        // listen address, e.g. 100.101.122.39:8787
	Dir          string        // pi sessions dir; empty = ~/.pi/agent/sessions
	Machine      string        // label reported in the snapshot
	ActiveWindow time.Duration // how long a quiet session still counts as active
	UsagePoll    time.Duration // provider usage refresh interval (default 1m)
}

// Run starts the agent HTTP server and blocks until it exits.
func Run(opts Options) error {
	scanner := sessions.NewScanner(opts.Dir, opts.ActiveWindow)
	usageStore := usage.NewStore(usage.New(), opts.Machine, usagePollInterval(opts.UsagePoll))
	mux := http.NewServeMux()

	mux.HandleFunc("GET /sessions", func(w http.ResponseWriter, r *http.Request) {
		snap := scanner.Poll()
		snap.Machine = opts.Machine
		writeJSON(w, http.StatusOK, snap)
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"machine": opts.Machine,
		})
	})

	mux.HandleFunc("GET /usage", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, usageStore.Snapshot())
	})

	// Refresh provider usage in the background; the endpoint serves the cache.
	// A fresh background context (not the request's) — the loop outlives requests.
	go usageStore.Start(context.Background())

	srv := &http.Server{
		Addr:              opts.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("hs-pi-dashboard agent listening on %s (machine=%s dir=%s)", opts.Addr, opts.Machine, opts.Dir)
	return srv.ListenAndServe()
}

func usagePollInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Minute
	}
	return d
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(payload)
}
