// Package fleet aggregates per-machine agent snapshots and serves the
// dashboard UI.
package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/HemSoft/hs-pi-dashboard/internal/sessions"
	"github.com/HemSoft/hs-pi-dashboard/internal/web"
)

// Target is one machine's agent.
type Target struct {
	Name string
	URL  string
}

// MachineState is the aggregated view of one machine.
type MachineState struct {
	Name        string             `json:"name"`
	URL         string             `json:"url"`
	Online      bool               `json:"online"`
	Error       string             `json:"error,omitempty"`
	GeneratedAt time.Time          `json:"generatedAt"`
	Sessions    []sessions.Summary `json:"sessions"`
}

// FleetSnapshot is the dashboard-facing aggregation of all machines.
type FleetSnapshot struct {
	GeneratedAt time.Time       `json:"generatedAt"`
	Machines    []MachineState  `json:"machines"`
}

// Server polls agents and serves /api/fleet plus the embedded UI.
type Server struct {
	targets   []Target
	pollEvery time.Duration
	timeout   time.Duration

	mu    sync.Mutex
	cache map[string]MachineState
}

// ParseTargets decodes the -fleet flag: comma-separated name|url pairs.
func ParseTargets(spec string) ([]Target, error) {
	var targets []Target
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, url, ok := strings.Cut(part, "|")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(url) == "" {
			return nil, fmt.Errorf("invalid fleet entry %q: want name|url", part)
		}
		targets = append(targets, Target{Name: strings.TrimSpace(name), URL: strings.TrimSpace(url)})
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("empty fleet spec")
	}
	return targets, nil
}

// NewServer builds the aggregator.
func NewServer(targets []Target, pollEvery time.Duration) *Server {
	if pollEvery <= 0 {
		pollEvery = 5 * time.Second
	}
	return &Server{
		targets:   targets,
		pollEvery: pollEvery,
		timeout:   3 * time.Second,
		cache:     make(map[string]MachineState, len(targets)),
	}
}

// Run starts the poll loop and the HTTP server. It blocks until the server
// exits.
func (s *Server) Run(addr string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticker := time.NewTicker(s.pollEvery)
	defer ticker.Stop()
	s.poll(ctx) // first pass immediately
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.poll(ctx)
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/fleet", s.handleFleet)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(web.Index)
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("hs-pi-dashboard serve listening on %s (poll=%s, machines=%d)", addr, s.pollEvery, len(s.targets))
	return srv.ListenAndServe()
}

func (s *Server) poll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, t := range s.targets {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			state := s.fetch(ctx, t)
			s.mu.Lock()
			s.cache[t.Name] = state
			s.mu.Unlock()
		}(t)
	}
	wg.Wait()
}

func (s *Server) fetch(ctx context.Context, t Target) MachineState {
	state := MachineState{Name: t.Name, URL: t.URL}

	reqCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(t.URL, "/")+"/sessions", nil)
	if err != nil {
		state.Error = err.Error()
		return s.mergeStale(state)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		state.Error = err.Error()
		return s.mergeStale(state)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		state.Error = fmt.Sprintf("agent returned %s", resp.Status)
		return s.mergeStale(state)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		state.Error = err.Error()
		return s.mergeStale(state)
	}

	var snap sessions.Snapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		state.Error = err.Error()
		return s.mergeStale(state)
	}

	state.Online = true
	state.GeneratedAt = snap.GeneratedAt
	state.Sessions = snap.Sessions
	return state
}

// mergeStale keeps the last good session list so a rebooting machine shows
// its history grayed out instead of vanishing.
func (s *Server) mergeStale(state MachineState) MachineState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.cache[state.Name]; ok && prev.Online {
		state.Sessions = prev.Sessions
		state.GeneratedAt = prev.GeneratedAt
	}
	return state
}

func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cachedFleet())
}

// cachedFleet snapshots the current aggregation under lock.
func (s *Server) cachedFleet() FleetSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	machines := make([]MachineState, 0, len(s.targets))
	for _, t := range s.targets {
		state, ok := s.cache[t.Name]
		if !ok {
			state = MachineState{Name: t.Name, URL: t.URL, Error: "no poll yet"}
		}
		machines = append(machines, state)
	}
	return FleetSnapshot{
		GeneratedAt: time.Now(),
		Machines:    machines,
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(payload)
}
