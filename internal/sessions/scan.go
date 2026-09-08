// Package sessions discovers pi session files and distills them into
// lightweight summaries suitable for a fleet dashboard.
//
// Pi persists every conversation as an append-only JSONL tree under
// ~/.pi/agent/sessions/<munged-cwd>/<file>.jsonl. Each line is one entry:
//
//	{"type":"session","version":3,"id":"...","timestamp":"...","cwd":"D:\\repo"}
//	{"type":"model_change","provider":"openai-codex","modelId":"gpt-6-astra",...}
//	{"type":"thinking_level_change","thinkingLevel":"medium",...}
//	{"type":"message","message":{"role":"assistant","usage":{"input":...,"cost":{"total":...}}},...}
//
// The scanner only ever appends to its per-file state: on each poll it reads
// the bytes that appeared since the previous pass, so scanning a directory
// with hundreds of megabytes of history stays cheap.
package sessions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultActiveWindow is how long after the last observed write a session is
// still considered active.
const DefaultActiveWindow = 2 * time.Minute

// Summary is the dashboard-facing projection of one pi session file.
type Summary struct {
	ID            string    `json:"id"`
	Name          string    `json:"name,omitempty"`
	Cwd           string    `json:"cwd,omitempty"`
	Project       string    `json:"project,omitempty"`
	StartedAt     time.Time `json:"startedAt"`
	LastActivity  time.Time `json:"lastActivity"`
	Active        bool      `json:"active"`
	MessageCount  int       `json:"messageCount"`
	Provider      string    `json:"provider,omitempty"`
	Model         string    `json:"model,omitempty"`
	ThinkingLevel string    `json:"thinkingLevel,omitempty"`
	InputTokens   int64     `json:"inputTokens"`
	OutputTokens  int64     `json:"outputTokens"`
	TotalTokens   int64     `json:"totalTokens"`
	CostUSD       float64   `json:"costUsd"`
	FirstPrompt   string    `json:"firstPrompt,omitempty"`

	// Outputs are the session's most recent visible agent texts, newest
	// first and capped at maxOutputs. Thinking and tool calls never count.
	Outputs []Output `json:"outputs,omitempty"`
}

// Output is one assistant text turn, kept as it was emitted (newlines and
// indentation intact) so the dashboard can reproduce the original shape.
type Output struct {
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

const maxOutputs = 5

// Snapshot is what the agent endpoint returns for one machine.
type Snapshot struct {
	Machine     string    `json:"machine"`
	GeneratedAt time.Time `json:"generatedAt"`
	SessionsDir string    `json:"sessionsDir"`
	Sessions    []Summary `json:"sessions"`
}

type fileState struct {
	size    int64
	summary *Summary
	partial []byte
}

// Scanner incrementally summarizes every *.jsonl file below Dir.
type Scanner struct {
	dir          string
	activeWindow time.Duration
	now          func() time.Time

	mu    sync.Mutex
	files map[string]*fileState
}

// NewScanner returns a scanner for dir. dir may be empty, in which case the
// default ~/.pi/agent/sessions is used.
func NewScanner(dir string, activeWindow time.Duration) *Scanner {
	if strings.TrimSpace(dir) == "" {
		dir = defaultSessionsDir()
	}
	if activeWindow <= 0 {
		activeWindow = DefaultActiveWindow
	}
	return &Scanner{
		dir:          dir,
		activeWindow: activeWindow,
		now:          time.Now,
		files:        map[string]*fileState{},
	}
}

// Poll rescans the directory and returns the current snapshot.
func (s *Scanner) Poll() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	seen := map[string]bool{}
	var summaries []Summary

	_ = filepath.WalkDir(s.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil //nolint:nilerr // unreadable entries are simply skipped
		}
		seen[path] = true
		summaries = append(summaries, s.pollFile(path, now))
		return nil
	})

	// Forget files that disappeared so state does not grow forever.
	for path := range s.files {
		if !seen[path] {
			delete(s.files, path)
		}
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].LastActivity.After(summaries[j].LastActivity)
	})

	return Snapshot{
		GeneratedAt: now,
		SessionsDir: s.dir,
		Sessions:    summaries,
	}
}

func (s *Scanner) pollFile(path string, now time.Time) Summary {
	info, err := os.Stat(path)
	if err != nil {
		return Summary{}
	}

	state, ok := s.files[path]
	if !ok || info.Size() < state.size {
		state = &fileState{summary: &Summary{}}
	}

	if ok && state.size == info.Size() {
		// Nothing appended; only refresh the activity window.
		state.summary.Active = now.Sub(state.summary.LastActivity) <= s.activeWindow
		return *state.summary
	}

	f, err := os.Open(path)
	if err != nil {
		return *state.summary
	}
	defer f.Close()

	if _, err := f.Seek(state.size, io.SeekStart); err != nil {
		return *state.summary
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return *state.summary
	}

	state.size = info.Size()
	lines := append(state.partial, data...)
	state.partial = nil
	for len(lines) > 0 {
		idx := bytes.IndexByte(lines, '\n')
		if idx < 0 {
			state.partial = append(state.partial, append([]byte(nil), lines...)...)
			// Cap the partial buffer so a pathological single line cannot
			// grow without bound.
			if len(state.partial) > maxPartialLine {
				state.partial = nil
			}
			break
		}
		line := bytes.TrimSpace(lines[:idx])
		lines = lines[idx+1:]
		if len(line) > 0 {
			applyEntry(state.summary, line)
		}
	}

	if state.summary.StartedAt.IsZero() {
		state.summary.StartedAt = info.ModTime()
	}
	if state.summary.LastActivity.IsZero() {
		// No parseable entry timestamps; fall back to the file mtime.
		state.summary.LastActivity = info.ModTime()
	}
	state.summary.Active = now.Sub(state.summary.LastActivity) <= s.activeWindow
	s.files[path] = state
	return *state.summary
}

// maxPartialLine bounds the carry-over buffer for a line that has not been
// fully read yet. Pi entries are single-line JSON far below this; entries
// beyond it are treated as malformed and skipped.
const maxPartialLine = 8 << 20 // 8 MiB

// applyEntry folds one JSONL entry into the running summary. Unknown entry
// types and malformed lines are ignored: parsing must never fail a scan.
func applyEntry(s *Summary, line []byte) {
	var head struct {
		Type      string          `json:"type"`
		ID        json.RawMessage `json:"id"`
		Name      string          `json:"name"`
		Cwd       string          `json:"cwd"`
		Timestamp json.RawMessage `json:"timestamp"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return
	}
	entryTS, entryHasTS := parseTimestamp(head.Timestamp)
	if entryHasTS && entryTS.After(s.LastActivity) {
		s.LastActivity = entryTS
	}

	switch head.Type {
	case "session":
		s.ID = rawString(head.ID)
		s.Name = head.Name
		s.Cwd = head.Cwd
		s.Project = filepath.Base(head.Cwd)
		if ts, ok := parseTimestamp(head.Timestamp); ok {
			s.StartedAt = ts
		}
	case "model_change":
		var mc struct {
			Provider string `json:"provider"`
			ModelID  string `json:"modelId"`
		}
		if json.Unmarshal(line, &mc) == nil {
			s.Provider = mc.Provider
			s.Model = mc.ModelID
		}
	case "thinking_level_change":
		var tc struct {
			ThinkingLevel string `json:"thinkingLevel"`
		}
		if json.Unmarshal(line, &tc) == nil {
			s.ThinkingLevel = tc.ThinkingLevel
		}
	case "message":
		s.MessageCount++
		var me struct {
			Message struct {
				Role     string          `json:"role"`
				Content  json.RawMessage `json:"content"`
				Provider string          `json:"provider"`
				Model    string          `json:"model"`
				Usage    *struct {
					Input       int64 `json:"input"`
					Output      int64 `json:"output"`
					TotalTokens int64 `json:"totalTokens"`
					Cost        *struct {
						Total float64 `json:"total"`
					} `json:"cost"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &me); err != nil {
			return
		}
		msg := me.Message
		if msg.Provider != "" {
			s.Provider = msg.Provider
		}
		if msg.Model != "" {
			s.Model = msg.Model
		}
		if msg.Usage != nil {
			s.InputTokens += msg.Usage.Input
			s.OutputTokens += msg.Usage.Output
			s.TotalTokens += msg.Usage.TotalTokens
			if msg.Usage.Cost != nil {
				s.CostUSD += msg.Usage.Cost.Total
			}
		}
		if s.FirstPrompt == "" && msg.Role == "user" {
			s.FirstPrompt = firstText(msg.Content)
		}
		// The dashboard surfaces the agent's recent visible text as extra
		// rows under the session; thinking and toolCall blocks are not
		// output, so only text blocks count.
		if msg.Role == "assistant" && entryHasTS {
			if text := capRaw(allText(msg.Content)); text != "" {
				s.Outputs = append([]Output{{Text: text, At: entryTS}}, s.Outputs...)
				if len(s.Outputs) > maxOutputs {
					s.Outputs = s.Outputs[:maxOutputs]
				}
			}
		}
	}
}

// rawString coerces a JSON id (string or number) into its string form so
// synthetic or odd ids never break entry parsing.
func rawString(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			return s
		}
		return ""
	}
	var num float64
	if json.Unmarshal(trimmed, &num) == nil {
		return fmt.Sprintf("%g", num)
	}
	return ""
}

// allText joins every text block of a message in order with blank lines,
// preserving the newlines the agent actually emitted; thinking and toolCall
// blocks are skipped.
func allText(content json.RawMessage) string {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if json.Unmarshal(trimmed, &text) == nil {
			return text
		}
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(trimmed, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// maxOutputChars bounds each stored output; the dashboard renders them with
// original formatting, so length is capped but newlines are kept.
const maxOutputChars = 800

// capRaw trims trailing whitespace and clamps length without flattening the
// newlines, unlike condense.
func capRaw(text string) string {
	text = strings.TrimRight(text, " \t\r\n")
	runes := []rune(text)
	if len(runes) > maxOutputChars {
		return string(runes[:maxOutputChars]) + "…"
	}
	return text
}

func firstText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if json.Unmarshal(trimmed, &text) == nil {
			return condense(text)
		}
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(trimmed, &blocks) != nil {
		return ""
	}
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return condense(b.Text)
		}
	}
	return ""
}

func condense(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 160 {
		text = strings.TrimSpace(text[:160]) + "…"
	}
	return text
}

// parseTimestamp accepts the shapes pi writes: RFC3339 strings and
// millisecond epoch numbers.
func parseTimestamp(raw json.RawMessage) (time.Time, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return time.Time{}, false
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return time.Time{}, false
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if ts, err := time.Parse(layout, s); err == nil {
				return ts, true
			}
		}
		return time.Time{}, false
	}
	var ms int64
	if err := json.Unmarshal(trimmed, &ms); err != nil || ms <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}

func defaultSessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pi/agent/sessions"
	}
	return filepath.Join(home, ".pi", "agent", "sessions")
}
