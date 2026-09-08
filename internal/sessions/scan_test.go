package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSession(t *testing.T, dir string, name string, lines ...string) {
	t.Helper()
	path := filepath.Join(dir, "repo", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPollSummarizesSessionFiles(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "2026-09-07_a.jsonl",
		`{"type":"session","version":3,"id":"abc","timestamp":"2026-09-07T12:00:00Z","cwd":"D:\\github\\HemSoft\\hs-tui-launcher"}`,
		`{"type":"model_change","id":"m1","timestamp":"2026-09-07T12:00:01Z","provider":"openai-codex","modelId":"gpt-6-astra"}`,
		`{"type":"thinking_level_change","id":"t1","timestamp":"2026-09-07T12:00:02Z","thinkingLevel":"medium"}`,
		`{"type":"message","id":"u1","timestamp":"2026-09-07T12:00:03Z","message":{"role":"user","content":[{"type":"text","text":"Make the effort medium please"}]}}`,
		`{"type":"message","id":"a1","timestamp":"2026-09-07T12:01:00Z","message":{"role":"assistant","provider":"openai-codex","model":"gpt-6-astra","usage":{"input":4066,"output":40,"totalTokens":4106,"cost":{"total":0.000315}}}}`,
	)
	writeSession(t, dir, "2026-09-06_b.jsonl",
		`{"type":"session","version":3,"id":"def","timestamp":"2026-09-06T08:00:00Z","cwd":"/home/franz/scratch"}`,
		`{"type":"message","id":"u2","timestamp":"2026-09-06T08:01:00Z","message":{"role":"user","content":"plain string prompt"}}`,
		`this line is not json and must be skipped`,
	)

	scanner := NewScanner(dir, 2*time.Minute)
	snap := scanner.Poll()

	if len(snap.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(snap.Sessions))
	}
	first := snap.Sessions[0]
	if first.ID != "abc" {
		t.Fatalf("most recent session id = %q, want abc", first.ID)
	}
	if first.Project != "hs-tui-launcher" {
		t.Fatalf("project = %q", first.Project)
	}
	if first.Model != "gpt-6-astra" || first.Provider != "openai-codex" {
		t.Fatalf("model = %q provider = %q", first.Model, first.Provider)
	}
	if first.ThinkingLevel != "medium" {
		t.Fatalf("thinkingLevel = %q", first.ThinkingLevel)
	}
	if first.FirstPrompt != "Make the effort medium please" {
		t.Fatalf("firstPrompt = %q", first.FirstPrompt)
	}
	if first.InputTokens != 4066 || first.OutputTokens != 40 || first.TotalTokens != 4106 {
		t.Fatalf("tokens = %d/%d/%d", first.InputTokens, first.OutputTokens, first.TotalTokens)
	}
	if first.CostUSD < 0.0003 || first.CostUSD > 0.0004 {
		t.Fatalf("costUsd = %v", first.CostUSD)
	}
	if first.MessageCount != 2 {
		t.Fatalf("messageCount = %d, want 2", first.MessageCount)
	}

	second := snap.Sessions[1]
	if second.ID != "def" || second.FirstPrompt != "plain string prompt" {
		t.Fatalf("second session = %+v", second)
	}
}

func TestPollIncrementalAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	base := `{"type":"session","version":3,"id":"inc","timestamp":"2026-09-07T12:00:00Z","cwd":"/repo"}`
	if err := os.WriteFile(path, []byte(base+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner(dir, 2*time.Minute)
	snap := scanner.Poll()
	if len(snap.Sessions) != 1 || snap.Sessions[0].MessageCount != 0 {
		t.Fatalf("initial poll = %+v", snap.Sessions)
	}

	// Append three entries; the next poll must fold them in without rereading
	// the header (message count proves the delta was applied exactly once).
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		entry, _ := json.Marshal(map[string]any{
			"type":      "message",
			"id":        i,
			"timestamp": "2026-09-07T12:05:00Z",
			"message":   map[string]any{"role": "user", "content": "tick"},
		})
		if _, err := f.Write(append(entry, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	snap = scanner.Poll()
	if len(snap.Sessions) != 1 {
		t.Fatalf("sessions after append = %d", len(snap.Sessions))
	}
	if got := snap.Sessions[0].MessageCount; got != 3 {
		t.Fatalf("messageCount after append = %d, want 3", got)
	}

	// A poll with no file changes must not double-count.
	snap = scanner.Poll()
	if got := snap.Sessions[0].MessageCount; got != 3 {
		t.Fatalf("messageCount after idle poll = %d, want 3", got)
	}
}

func TestPollTracksLastAssistantOutput(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "s.jsonl",
		`{"type":"session","version":3,"id":"out","timestamp":"2026-09-07T12:00:00Z","cwd":"/repo"}`,
		`{"type":"message","id":"u1","timestamp":"2026-09-07T12:01:00Z","message":{"role":"user","content":"go ahead"}}`,
		// Tool-call-only turn: not output, must not move LastOutput.
		`{"type":"message","id":"a1","timestamp":"2026-09-07T12:02:00Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"tc1","name":"read"}]}}`,
		// Thinking-only turn: also not output.
		`{"type":"message","id":"a2","timestamp":"2026-09-07T12:03:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"pondering"}]}}`,
		// Real text: becomes the last output.
		`{"type":"message","id":"a3","timestamp":"2026-09-07T12:04:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"All tests pass now."}]}}`,
		// A later tool-call-only turn must not clobber it.
		`{"type":"message","id":"a4","timestamp":"2026-09-07T12:05:00Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"tc2","name":"edit"}]}}`,
	)
	scanner := NewScanner(dir, 2*time.Minute)
	got := scanner.Poll().Sessions[0]
	if got.LastOutput != "All tests pass now." {
		t.Fatalf("lastOutput = %q, want the text turn", got.LastOutput)
	}
	want := time.Date(2026, 9, 7, 12, 4, 0, 0, time.UTC)
	if got.LastOutputAt == nil || !got.LastOutputAt.Equal(want) {
		t.Fatalf("lastOutputAt = %v, want %v", got.LastOutputAt, want)
	}
}

func TestPollMarksActivityWindow(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "s.jsonl",
		`{"type":"session","version":3,"id":"win","timestamp":"2026-09-07T12:00:00Z","cwd":"/repo"}`,
	)
	scanner := NewScanner(dir, 2*time.Minute)
	scanner.now = func() time.Time {
		return time.Date(2026, 9, 7, 12, 1, 0, 0, time.UTC) // one minute after start
	}
	if got := scanner.Poll().Sessions[0]; !got.Active {
		t.Fatal("session within the activity window should be active")
	}

	scanner.now = func() time.Time {
		return time.Date(2026, 9, 7, 12, 30, 0, 0, time.UTC) // well past the window
	}
	if got := scanner.Poll().Sessions[0]; got.Active {
		t.Fatal("session past the activity window should not be active")
	}
}
