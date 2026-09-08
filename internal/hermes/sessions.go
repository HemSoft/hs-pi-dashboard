// Package hermes surfaces live Hermes Agent sessions from ~/.hermes/state.db
// alongside pi sessions on the fleet dashboard.
//
// Hermes keeps a single state.db for the main gateway plus one per profile
// under ~/.hermes/profiles/<name>/. Reading the same `sessions` table in each
// is the least invasive integration: it's a stable-enough schema, we only do
// SELECTs, and Hermes upgrades are additively compatible. We never write.
// Cron job names are re-read from ~/.hermes/cron/jobs.json each Snapshot so a
// human-edited job label lands without a redeploy.
package hermes

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultMainDB      = "~/.hermes/state.db"
	defaultProfilesDir = "~/.hermes/profiles"
	defaultJobsJSON    = "~/.hermes/cron/jobs.json"
)

// Session is one row out of Hermes' sessions table, projected to match the
// dashboard's shape.
type Session struct {
	ID              string     `json:"id"`
	Source          string     `json:"source"`
	Profile         string     `json:"profile,omitempty"`
	Name            string     `json:"name,omitempty"`
	Model           string     `json:"model,omitempty"`
	BillingProvider string     `json:"billingProvider,omitempty"`
	MessageCount    int        `json:"messageCount"`
	ToolCallCount   int        `json:"toolCallCount"`
	InputTokens     int64      `json:"inputTokens"`
	OutputTokens    int64      `json:"outputTokens"`
	CacheReadTokens int64      `json:"cacheReadTokens"`
	ReasoningTokens int64      `json:"reasoningTokens"`
	StartedAt       time.Time  `json:"startedAt"`
	EndedAt         *time.Time `json:"endedAt,omitempty"`
	EndReason       string     `json:"endReason,omitempty"`
	EstimatedCost   float64    `json:"estimatedCostUsd"`
	Title           string     `json:"title,omitempty"`
	Cwd             string     `json:"cwd,omitempty"`

	// Outputs are the session's most recent visible assistant texts, newest
	// first and capped at maxOutputs. '' and '[SILENT]' turns never count.
	Outputs []Output `json:"outputs,omitempty"`
}

// Output is one assistant text turn, kept as it was emitted (newlines and
// indentation intact) so the dashboard can reproduce the original shape.
type Output struct {
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

const maxOutputs = 5

// Provider scans the Hermes main DB plus every profile DB under the profiles
// dir, keeping cron-job names in memory.
type Provider struct {
	mainDB   string
	profiles string
	jobsJSON string
}

// New returns a Provider for the default locations.
func New() *Provider {
	return &Provider{
		mainDB:   expandTilde(defaultMainDB),
		profiles: expandTilde(defaultProfilesDir),
		jobsJSON: expandTilde(defaultJobsJSON),
	}
}

// Snapshot returns the current session list.
func (p *Provider) Snapshot(limit int) Snapshot {
	return Snapshot{
		GeneratedAt: time.Now(),
		Sessions:    p.List(limit),
	}
}

// Snapshot is what /api/hermes-sessions serves.
type Snapshot struct {
	GeneratedAt time.Time `json:"generatedAt"`
	Sessions    []Session `json:"sessions"`
}

type profileEntry struct {
	profile string
	path    string
}

// jobsFile is the parsed form of ~/.hermes/cron/jobs.json.
type jobsFile struct {
	Jobs []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"jobs"`
}

// List returns the most recent sessions aggregated across the main DB and
// all profile DBs, ordered by started_at(newest first) and capped at limit.
// Errors collapse to an empty list: Hermes upgrading, migrating, or the DB
// lock being momentarily held are all normal and not worth surfacing.
func (p *Provider) List(limit int) []Session {
	if limit <= 0 {
		limit = 50
	}
	out := p.queryDB("default", p.mainDB, limit)
	for _, entry := range p.profileDBs() {
		out = append(out, p.queryDB(entry.profile, entry.path, limit)...)
	}
	// newest first
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].StartedAt.After(out[i].StartedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return p.withJobNames(out)
}

// withJobNames derives a session Name from ~/.hermes/cron/jobs.json when the
// ID matches a cron's stored pattern; falls back to Source+ID fragment.
// Cron rows carry the pattern `cron_<jobid>_<timestamp>` where <jobid> is the
// short hex key in jobs.json.
func (p *Provider) withJobNames(list []Session) []Session {
	names := p.jobNames()
	for i, s := range list {
		if names[s.ID] != "" {
			list[i].Name = names[s.ID]
			continue
		}
		// Try the cron suffix: cron_<shortID>_<ts>. Skip when the ID isn't in
		// the cron shape.
		if rest, ok := strings.CutPrefix(s.ID, "cron_"); ok {
			if idx := strings.Index(rest, "_"); idx > 0 {
				if _, ok := names[rest[:idx]]; ok {
					list[i].Name = "cron: " + cronNameFor(rest[:idx], names)
					continue
				}
			}
		}
		list[i].Name = s.Source
		if s.ID != "" {
			if len(s.ID) > 8 {
				list[i].Name += ":" + s.ID[:8]
			} else {
				list[i].Name += ":" + s.ID
			}
		}
	}
	return list
}

// cronNameFor returns a short, dashboard-friendly label for a cron job. The
// profile column already carries COS/Janitor/Developer, so long names just
// repeat. If the job has no explicit entry we fall back to the raw id.
func cronNameFor(id string, names map[string]string) string {
	switch id {
	case "3234aa52438e":
		return "heartbeat"
	case "249daf42cf09":
		return "heartbeat"
	case "a431086f3580":
		return "heartbeat"
	default:
		if names[id] != "" {
			return names[id]
		}
		return id
	}
}

// jobNames returns the id→name map. It's re-loaded on every Snapshot so
// adding/removing a cron job lands without a binary redeploy.
func (p *Provider) jobNames() map[string]string {
	names := map[string]string{}
	data, err := os.ReadFile(p.jobsJSON)
	if err != nil {
		return names
	}
	var jf jobsFile
	if json.Unmarshal(data, &jf) != nil {
		return names
	}
	for _, j := range jf.Jobs {
		if j.ID != "" && j.Name != "" {
			names[j.ID] = j.Name
		}
	}
	return names
}

// profileDBs lists each ~/.hermes/profiles/<name>/state.db.
func (p *Provider) profileDBs() []profileEntry {
	entries, err := os.ReadDir(p.profiles)
	if err != nil {
		return nil
	}
	var out []profileEntry
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		path := filepath.Join(p.profiles, de.Name(), "state.db")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		out = append(out, profileEntry{profile: de.Name(), path: path})
	}
	return out
}

func (p *Provider) queryDB(profile, dbPath string, limit int) []Session {
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	dsn := "file:" + url.QueryEscape(dbPath) + "?mode=ro&_journal_mode=WAL"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT id, COALESCE(source, ''), COALESCE(model, ''), COALESCE(billing_provider, ''),
		        COALESCE(message_count, 0), COALESCE(tool_call_count, 0),
		        COALESCE(input_tokens, 0), COALESCE(output_tokens, 0),
		        COALESCE(cache_read_tokens, 0), COALESCE(reasoning_tokens, 0),
		        started_at,
		        ended_at,
		        COALESCE(end_reason, ''), COALESCE(estimated_cost_usd, 0),
		        COALESCE(title, ''), COALESCE(cwd, '')
		 FROM sessions se
		 WHERE ended_at IS NULL OR ended_at > strftime('%s','now') - 86400
		 ORDER BY started_at DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var s Session
		var started float64
		var endedNull sql.NullFloat64
		if err := rows.Scan(
			&s.ID, &s.Source, &s.Model, &s.BillingProvider,
			&s.MessageCount, &s.ToolCallCount, &s.InputTokens, &s.OutputTokens,
			&s.CacheReadTokens, &s.ReasoningTokens,
			&started, &endedNull, &s.EndReason, &s.EstimatedCost, &s.Title, &s.Cwd,
		); err != nil {
			continue
		}
		s.Profile = profile
		s.StartedAt = time.UnixMilli(int64(started * 1000))
		if endedNull.Valid {
			e := time.UnixMilli(int64(endedNull.Float64 * 1000))
			s.EndedAt = &e
		}
		out = append(out, s)
	}
	attachOutputs(db, out)
	return out
}

// attachOutputs fills each session's Outputs with its most recent visible
// assistant texts (newest first, capped). One grouped query per DB; the
// idx_messages_session index keeps it cheap. Read-only by design.
func attachOutputs(db *sql.DB, sessions []Session) {
	if len(sessions) == 0 {
		return
	}
	args := make([]any, len(sessions))
	placeholders := make([]string, len(sessions))
	for i, s := range sessions {
		args[i] = s.ID
		placeholders[i] = "?"
	}
	rows, err := db.Query(`SELECT session_id, content, timestamp FROM messages
		WHERE role = 'assistant' AND content IS NOT NULL
		  AND TRIM(content) != '' AND content != '[SILENT]'
		  AND session_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY timestamp DESC`, args...)
	if err != nil {
		return // outputs are best-effort; sessions still render without them
	}
	defer rows.Close()

	byID := make(map[string]*Session, len(sessions))
	for i := range sessions {
		byID[sessions[i].ID] = &sessions[i]
	}
	counts := make(map[string]int)
	for rows.Next() {
		var sid, content string
		var ts sql.NullFloat64
		if err := rows.Scan(&sid, &content, &ts); err != nil || !ts.Valid {
			continue
		}
		if counts[sid] >= maxOutputs {
			continue
		}
		counts[sid]++
		if s := byID[sid]; s != nil {
			s.Outputs = append(s.Outputs, Output{
				Text: capRaw(content),
				At:   time.UnixMilli(int64(ts.Float64 * 1000)),
			})
		}
	}
}

// maxOutputChars bounds each stored output; the dashboard renders them with
// original formatting, so length is capped but newlines are kept.
const maxOutputChars = 800

// capRaw trims trailing whitespace and clamps length without flattening the
// newlines.
func capRaw(text string) string {
	text = strings.TrimRight(text, " \t\r\n")
	runes := []rune(text)
	if len(runes) > maxOutputChars {
		return string(runes[:maxOutputChars]) + "…"
	}
	return text
}

var (
	defaultOnce     sync.Once
	defaultProvider *Provider
)

// Default returns the shared provider.
func Default() *Provider {
	defaultOnce.Do(func() { defaultProvider = New() })
	return defaultProvider
}

func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return strings.TrimPrefix(path, "~")
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}
