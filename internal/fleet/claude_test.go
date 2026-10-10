package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HemSoft/hs-pi-dashboard/internal/sessions"
)

func TestFleetCountsClaudeAlongsidePiAndPreservesOutput(t *testing.T) {
	now := time.Now()
	payload := sessions.MergeSnapshots(
		sessions.Snapshot{GeneratedAt: now, Sessions: []sessions.Summary{{ID: "same", Active: true}}},
		sessions.Snapshot{GeneratedAt: now, Sessions: []sessions.Summary{{
			ID: "claude-code:same", Provider: "claude-code", Active: true,
			Outputs: []sessions.Output{{Text: "Claude result", At: now}},
		}}},
	)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Error(err)
		}
	}))
	defer agent.Close()
	server := NewServer([]Target{{Name: "test", URL: agent.URL}}, time.Second)
	state := server.fetch(context.Background(), server.targets[0])
	if !state.Online || state.ActiveSessions != 2 || len(state.Sessions) != 2 {
		t.Fatalf("machine state = %+v", state)
	}
	if got := activeSessionCount([]MachineState{state}, 0); got != 2 {
		t.Fatalf("Pulse count = %d, want 2", got)
	}
	var found bool
	for _, s := range state.Sessions {
		if s.ID == "claude-code:same" {
			found = s.Provider == "claude-code" && len(s.Outputs) == 1 && s.Outputs[0].Text == "Claude result"
		}
	}
	if !found {
		t.Fatalf("Claude output not propagated: %+v", state.Sessions)
	}
	state.Online = false
	if got := activeSessionCount([]MachineState{state}, 0); got != 0 {
		t.Fatalf("offline Claude counted as live: %d", got)
	}
}

func TestFleetClassifiesLegacyOriginsWithoutGuessingFromProvider(t *testing.T) {
	now := time.Now()
	payload := sessions.Snapshot{GeneratedAt: now, SessionsDir: "/custom/pi-sessions", Sessions: []sessions.Summary{
		{ID: "legacy", Provider: "openai-codex", Active: true},
		{ID: "claude-code:legacy", Provider: "openai-codex", Active: true},
		{ID: "t3:env:legacy", Provider: "openai-codex", Active: true},
		{ID: "future:unknown", Provider: "openai-codex", Active: true},
		{ID: "explicit", Source: "future", Provider: "openai-codex", Active: true},
		{ID: "pi:explicit", Source: "pi", Provider: "openai-codex", Active: true},
	}}
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Error(err)
		}
	}))
	defer agent.Close()
	server := NewServer([]Target{{Name: "test", URL: agent.URL}}, time.Second)
	state := server.fetch(context.Background(), server.targets[0])
	want := []string{"pi", "claude-code", "t3", "", "future", "pi"}
	if len(state.Sessions) != len(want) {
		t.Fatalf("rows changed: %+v", state)
	}
	for i, row := range state.Sessions {
		if row.Source != want[i] || row.Provider != "openai-codex" {
			t.Fatalf("row %d = %+v, want source %q with unchanged provider", i, row, want[i])
		}
	}
	if got := activeSessionCount([]MachineState{state}, 0); got != 6 {
		t.Fatalf("count changed: %d", got)
	}
	server.cache["test"] = state
	agent.Close()
	cached := server.fetch(context.Background(), server.targets[0])
	if cached.Online || len(cached.Sessions) != len(want) {
		t.Fatalf("offline rows = %+v", cached)
	}
	for i, row := range cached.Sessions {
		if row.Source != want[i] {
			t.Fatalf("cached row %d lost app: %+v", i, row)
		}
	}
	if got := activeSessionCount([]MachineState{cached}, 0); got != 0 {
		t.Fatalf("offline count = %d", got)
	}
}

func TestFleetLeavesOriginUnknownWithoutCollectorMetadata(t *testing.T) {
	payload := sessions.Snapshot{GeneratedAt: time.Now(), Sessions: []sessions.Summary{{ID: "unclassified", Provider: "openai-codex", Active: true}}}
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Error(err)
		}
	}))
	defer agent.Close()
	server := NewServer([]Target{{Name: "test", URL: agent.URL}}, time.Second)
	state := server.fetch(context.Background(), server.targets[0])
	if !state.Online || len(state.Sessions) != 1 || state.Sessions[0].Source != "" {
		t.Fatalf("unknown origin mislabeled: %+v", state)
	}
}
