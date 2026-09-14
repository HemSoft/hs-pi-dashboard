package hermes

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestProvider builds a Provider against a temp SQLite DB shaped like
// Hermes' state.db, seeded with three sessions: one with several assistant
// text turns, one whose assistant turns are all [SILENT]/empty, and one with
// no messages.
func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Hermes data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
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

	type turn struct {
		role, content string
		ts            float64
	}
	seed := []struct {
		id    string
		start float64
		turns []turn
	}{
		{
			id: "cron_a_1", start: 1788883000,
			turns: []turn{{"assistant", "[SILENT]", 1788883005}},
		},
		{
			id: "cli_b_2", start: 1788882000,
			turns: []turn{
				{"assistant", "brief 1", 1788882010},
				{"tool", `{"content": "tool noise"}`, 1788882012},
				{"assistant", "brief 2", 1788882020},
				{"assistant", "brief 3", 1788882030},
				{"assistant", "brief 4", 1788882040},
				{"assistant", "brief 5", 1788882050},
				{"assistant", "brief 6", 1788882060},
				{"assistant", "brief 7", 1788882070},
			},
		},
		{
			id: "cli_c_3", start: 1788881000,
			turns: []turn{
				{"assistant", "[SILENT]", 1788881010},
				{"assistant", "", 1788881011},
			},
		},
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

func TestSnapshotExcludesStaleUnendedSessionsWithoutDroppingActiveRows(t *testing.T) {
	p := newTestProvider(t)
	p.now = func() time.Time { return time.Unix(1788883060, 0) }

	db, err := sql.Open("sqlite", p.mainDB)
	if err != nil {
		t.Fatal(err)
	}
	for i, started := range []float64{1788883001, 1788883002} {
		if _, err := db.Exec(`INSERT INTO sessions (id, source, started_at) VALUES (?, 'cli', ?)`,
			fmt.Sprintf("active_%d", i), started); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if got := len(p.List(1)); got != 3 {
		t.Fatalf("len(List(1)) = %d, want all 3 active sessions", got)
	}
	// cron_a_1 plus the two new rows are recent. cli_b_2 and cli_c_3 have no
	// recent messages but also no ended_at, which reproduces old Hermes data.
	snapshot := p.Snapshot(1)
	if len(snapshot.Sessions) != 3 || snapshot.ActiveSessions != 3 {
		t.Fatalf("Snapshot(1) = %d rows, %d active; want all 3 active rows", len(snapshot.Sessions), snapshot.ActiveSessions)
	}
	if snapshot.Sessions[0].ID != "cron_a_1" {
		t.Fatalf("Snapshot(1) session = %q, want most recently active cron_a_1", snapshot.Sessions[0].ID)
	}
	byID := map[string]Session{}
	for _, session := range p.List(10) {
		byID[session.ID] = session
	}
	if !byID["cron_a_1"].Active {
		t.Fatal("recent unended session should render active")
	}
	if byID["cli_b_2"].Active || byID["cli_c_3"].Active {
		t.Fatal("stale unended sessions should not render active")
	}

	db, err = sql.Open("sqlite", p.mainDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sessions SET ended_at = ? WHERE id = ?`, p.now().Unix(), "active_0"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if got := p.Snapshot(1).ActiveSessions; got != 2 {
		t.Fatalf("Snapshot(1).ActiveSessions after one recent session ended = %d, want 2", got)
	}
}

func TestListKeepsOlderActiveSessionAheadOfNewerEndedHistory(t *testing.T) {
	p := newTestProvider(t)
	now := time.Now()
	p.now = func() time.Time { return now }

	db, err := sql.Open("sqlite", p.mainDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO sessions (id, source, started_at) VALUES ('active_old', 'cli', ?)`, float64(now.Add(-90*time.Second).UnixMilli())/1000); err != nil {
		t.Fatal(err)
	}
	for i := range 21 {
		started := float64(now.Add(-time.Duration(i)*time.Second).UnixMilli()) / 1000
		if _, err := db.Exec(`INSERT INTO sessions (id, source, started_at, ended_at) VALUES (?, 'cli', ?, ?)`, fmt.Sprintf("ended_%02d", i), started, started+.5); err != nil {
			t.Fatal(err)
		}
	}

	snapshot := p.Snapshot(20)
	if len(snapshot.Sessions) != 20 || snapshot.ActiveSessions != 1 {
		t.Fatalf("Snapshot(20) = %d rows, %d active; want 20 rows with one active", len(snapshot.Sessions), snapshot.ActiveSessions)
	}
	if snapshot.Sessions[0].ID != "active_old" || !snapshot.Sessions[0].Active {
		t.Fatalf("first session = %+v, want older active session", snapshot.Sessions[0])
	}
}

func TestListSurfacesRecentAssistantOutputs(t *testing.T) {
	p := newTestProvider(t)
	byID := map[string]Session{}
	for _, s := range p.List(10) {
		byID[s.ID] = s
	}

	// Newest first, capped at 2, and only visible text turns.
	rich := byID["cli_b_2"]
	if len(rich.Outputs) != 2 {
		t.Fatalf("outputs = %d, want capped at 2", len(rich.Outputs))
	}
	if rich.Outputs[0].Text != "brief 7" || rich.Outputs[1].Text != "brief 6" {
		t.Fatalf("cap window wrong: oldest kept %q, newest %q", rich.Outputs[1].Text, rich.Outputs[0].Text)
	}
	wantAt := time.UnixMilli(int64(1788882070 * 1000))
	if !rich.Outputs[0].At.Equal(wantAt) {
		t.Fatalf("newest at = %v, want %v", rich.Outputs[0].At, wantAt)
	}
	for i, o := range rich.Outputs {
		if o.Text == `{"content": "tool noise"}` {
			t.Fatalf("tool row leaked into outputs at index %d", i)
		}
	}

	// '' and [SILENT] assistant turns never count as output.
	silent := byID["cron_a_1"]
	if len(silent.Outputs) != 0 {
		t.Fatalf("silent session got outputs = %v", silent.Outputs)
	}

	quiet := byID["cli_c_3"]
	if len(quiet.Outputs) != 0 {
		t.Fatalf("empty-turn session got outputs = %v", quiet.Outputs)
	}
}
