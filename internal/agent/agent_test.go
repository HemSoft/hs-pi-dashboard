package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HemSoft/hs-pi-dashboard/internal/sessions"
)

func TestSessionsEndpointCombinesPiAndClaudeForFleet(t *testing.T) {
	piDir, claudeDir := t.TempDir(), t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	write := func(dir, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(content+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(piDir, `{"type":"session","id":"same","timestamp":"`+now+`","cwd":"/repo"}`)
	write(claudeDir, `{"type":"user","sessionId":"same","uuid":"u1","timestamp":"`+now+`","cwd":"/repo","message":{"role":"user","content":"Hello"}}`)
	handler := sessionHandler(Options{Dir: piDir, ClaudeDir: claudeDir, Machine: "test-machine"})
	server := httptest.NewServer(handler)
	defer server.Close()
	for range 2 {
		response, err := http.Get(server.URL + "/sessions")
		if err != nil {
			t.Fatal(err)
		}
		var snap sessions.Snapshot
		err = json.NewDecoder(response.Body).Decode(&snap)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || snap.Machine != "test-machine" || snap.ActiveSessions != 2 || len(snap.Sessions) != 2 {
			t.Fatalf("agent response = %+v", snap)
		}
		ids := map[string]bool{}
		for _, s := range snap.Sessions {
			ids[s.ID] = true
		}
		if !ids["same"] || !ids["claude-code:same"] {
			t.Fatalf("session ids collided: %+v", ids)
		}
	}
}
