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
