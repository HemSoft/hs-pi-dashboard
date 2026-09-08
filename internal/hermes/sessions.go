// Package hermes surfaces live Hermes Agent sessions from ~/.hermes/state.db
// alongside pi sessions on the fleet dashboard.
//
// Hermes persists all CLI, TUI, gateway, and cron sessions to a single
// SQLite database at ~/.hermes/state.db. Reading the `sessions` table is the
// least invasive integration: it's a stable-enough schema, we only do
// SELECTs, and Hermes upgrades are additively compatible. We never write to
// this DB.
package hermes

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// defaultDBPath is where Hermes keeps state.db.
const defaultDBPath = "~/.hermes/state.db"

// Session is one row out of Hermes' sessions table, projected to match the
// sessions.Summary shape the dashboard already renders.
type Session struct {
	ID              string     `json:"id"`
	Source          string     `json:"source"`
	Profile         string     `json:"profile,omitempty"`
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
}

// Provider scans a Hermes SQLite state DB for sessions. Like the sessions
// scanner, it's cheap to poll: on each call we re-query and there's no
// retained state between polls.
type Provider struct {
	dbPath string
}

// New returns a Provider for the default DB path. Missing paths return a
// Provider that always yields an empty snapshot — the dashboard just renders
// no Hermes rows in that case.
func New() *Provider {
	return &Provider{dbPath: expandTilde(defaultDBPath)}
}

// List returns the most recent sessions ordered by started_at, capped at
// limit. Errors collapse to an empty list: Hermes upgrading, migrating, or
// the DB lock being momentarily held are all normal and not worth showing in
// the panel.
func (p *Provider) List(limit int) []Session {
	if limit <= 0 {
		limit = 50
	}
	if _, err := os.Stat(p.dbPath); err != nil {
		return nil
	}
	dsn := "file:" + url.QueryEscape(p.dbPath) + "?mode=ro&_journal_mode=WAL"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT id, COALESCE(source, ''), COALESCE(profile_name, ''),
		        COALESCE(model, ''), COALESCE(billing_provider, ''),
		        COALESCE(message_count, 0), COALESCE(tool_call_count, 0),
		        COALESCE(input_tokens, 0), COALESCE(output_tokens, 0),
		        COALESCE(cache_read_tokens, 0), COALESCE(reasoning_tokens, 0),
		        started_at,
		        ended_at,
		        COALESCE(end_reason, ''), COALESCE(estimated_cost_usd, 0),
		        COALESCE(title, ''), COALESCE(cwd, '')
		 FROM sessions
		 WHERE ended_at IS NULL OR ended_at > strftime('%s','now') - 86400
		 ORDER BY started_at DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]Session, 0, limit)
	for rows.Next() {
		var s Session
		var started, ended float64
		var endedNull sql.NullFloat64
		if err := rows.Scan(
			&s.ID, &s.Source, &s.Profile, &s.Model, &s.BillingProvider,
			&s.MessageCount, &s.ToolCallCount, &s.InputTokens, &s.OutputTokens,
			&s.CacheReadTokens, &s.ReasoningTokens,
			&started, &endedNull, &s.EndReason, &s.EstimatedCost, &s.Title, &s.Cwd,
		); err != nil {
			return nil
		}
		s.StartedAt = time.UnixMilli(int64(started * 1000))
		if endedNull.Valid {
			ended = endedNull.Float64
			e := time.UnixMilli(int64(ended * 1000))
			s.EndedAt = &e
		}
		out = append(out, s)
	}
	return out
}

// Snapshot is what /api/hermes-sessions serves.
type Snapshot struct {
	GeneratedAt time.Time `json:"generatedAt"`
	Sessions    []Session `json:"sessions"`
}

// Snapshot returns the current session list.
func (p *Provider) Snapshot(limit int) Snapshot {
	return Snapshot{
		GeneratedAt: time.Now(),
		Sessions:    p.List(limit),
	}
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
