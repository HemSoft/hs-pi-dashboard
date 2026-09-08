package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HemSoft/hs-pi-dashboard/internal/sessions"
)

func TestParseTargets(t *testing.T) {
	targets, err := ParseTargets("home|http://100.101.122.39:8787, laptop|http://100.117.202.124:8787")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].Name != "home" || targets[1].URL != "http://100.117.202.124:8787" {
		t.Fatalf("targets = %+v", targets)
	}
	if _, err := ParseTargets("broken"); err == nil {
		t.Fatal("expected error for entry without name|url")
	}
	if _, err := ParseTargets("  "); err == nil {
		t.Fatal("expected error for empty spec")
	}
}

func TestServerAggregatesMachinesAndMarksOffline(t *testing.T) {
	payload, err := json.Marshal(sessions.Snapshot{
		Machine:     "home",
		GeneratedAt: time.Now(),
		Sessions: []sessions.Summary{{
			ID:        "abc",
			Project:   "hs-tui-launcher",
			Active:    true,
			Model:     "gpt-6-astra",
			LastActivity: time.Now(),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer agent.Close()

	url := agent.URL
	targets, err := ParseTargets("home|" + url + ",ghost|http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	server := NewServer(targets, time.Second)
	server.poll(context.Background())
	// Poll directly against a real HTTP mux so handleFleet sees the cache.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/fleet", server.handleFleet)
	req := httptest.NewRequest(http.MethodGet, "/api/fleet", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var snap FleetSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Machines) != 2 {
		t.Fatalf("machines = %d, want 2", len(snap.Machines))
	}

	home := snap.Machines[0]
	if !home.Online || home.Error != "" {
		t.Fatalf("home state = %+v", home)
	}
	if len(home.Sessions) != 1 || home.Sessions[0].ID != "abc" {
		t.Fatalf("home sessions = %+v", home.Sessions)
	}

	ghost := snap.Machines[1]
	if ghost.Online || ghost.Error == "" {
		t.Fatalf("ghost state = %+v", ghost)
	}
}

func TestServerKeepsStaleSessionsWhenAgentFails(t *testing.T) {
	fail := false
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"machine":"home","sessions":[{"id":"keep","active":false}]}`))
	}))
	defer agent.Close()

	targets, err := ParseTargets("home|" + agent.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(targets, time.Hour) // never repolls on its own
	server.poll(context.Background())

	snap := server.cachedFleet()
	if len(snap.Machines) != 1 || !snap.Machines[0].Online || len(snap.Machines[0].Sessions) != 1 {
		t.Fatalf("first poll = %+v", snap.Machines)
	}

	fail = true
	server.poll(context.Background())
	snap = server.cachedFleet()
	state := snap.Machines[0]
	if state.Online {
		t.Fatal("state should be offline after failure")
	}
	if !strings.Contains(state.Error, "500") {
		t.Fatalf("error = %q", state.Error)
	}
	if len(state.Sessions) != 1 || state.Sessions[0].ID != "keep" {
		t.Fatalf("stale sessions not retained: %+v", state.Sessions)
	}
}
