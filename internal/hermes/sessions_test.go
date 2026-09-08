package hermes

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// newTestProvider builds a Provider against a temp SQLite DB shaped like
// Hermes' state.db, seeded with three sessions: one with assistant text,
// one whose assistant turns are all [SILENT], and one with no messages.
func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	schema := `
CREATE TABLE sessions (
	id TEXT PRIMARY KEY, source TEXT, model TEXT, billing_provider TEXT,
	message_count INT, tool_call_count INT, input_tokens INT, output_tokens INT,
	cache_read_tokens INT, reasoning_tokens INT,
	started_at REAL, ended_at REAL, end_reason TEXT, estimated_cost_usd REAL,
	title TEXT, cwd TEXT
);
CREATE TABLE messages (
	id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, timestamp REAL
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	// Production Hermes DBs are already in WAL mode; queryDB opens them
	// read-only and re-requests WAL, which only works when the file is
	// already WAL. Match that here or the read-only open fails.
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}

	seed := []struct {
		id    string
		start float64
		turns []struct {
			role, content string
			ts            float64
		}
	}{
		{id: "cron_a_1", start: 1788883000, turns: []struct {
			role, content string
			ts            float64
		}{
			{"assistant", "[SILENT]", 1788883010},
			{"assistant", "Briefing sent to Slack.", 1788883020},
			{"tool", `{"content": "tool noise"}`, 1788883025},
		}},
		{id: "cron_b_2", start: 1788882000, turns: []struct {
			role, content string
			ts            float64
		}{
			{"assistant", "[SILENT]", 1788882010},
			{"assistant", "", 1788882011},
		}},
		{id: "cli_c_3", start: 1788881000, turns: nil},
	}
	for _, s := range seed {
		if _, err := db.Exec(`INSERT INTO sessions (id, source, started_at) VALUES (?, 'cron', ?)`, s.id, s.start); err != nil {
			t.Fatal(err)
		}
		for _, m := range s.turns {
			if _, err := db.Exec(`INSERT INTO messages (session_id, role, content, timestamp) VALUES (?, ?, ?, ?)`, s.id, m.role, m.content, m.ts); err != nil {
				t.Fatal(err)
			}
		}
	}

	return &Provider{
		mainDB:   dbPath,
		profiles: filepath.Join(dir, "no-profiles"),
		jobsJSON: filepath.Join(dir, "no-jobs.json"),
	}
}

func TestListSurfacesLastAssistantOutput(t *testing.T) {
	p := newTestProvider(t)
	byID := map[string]Session{}
	for _, s := range p.List(10) {
		byID[s.ID] = s
	}

	text := byID["cron_a_1"]
	if text.LastOutput != "Briefing sent to Slack." {
		t.Fatalf("lastOutput = %q, want the visible text turn", text.LastOutput)
	}
	want := time.UnixMilli(int64(1788883020 * 1000))
	if text.LastOutputAt == nil || !text.LastOutputAt.Equal(want) {
		t.Fatalf("lastOutputAt = %v, want %v", text.LastOutputAt, want)
	}

	// Tool rows and [SILENT]/empty assistant turns never count as output.
	silent := byID["cron_b_2"]
	if silent.LastOutput != "" || silent.LastOutputAt != nil {
		t.Fatalf("silent session got output = %q/%v", silent.LastOutput, silent.LastOutputAt)
	}

	quiet := byID["cli_c_3"]
	if quiet.LastOutput != "" || quiet.LastOutputAt != nil {
		t.Fatalf("messageless session got output = %q/%v", quiet.LastOutput, quiet.LastOutputAt)
	}
}
