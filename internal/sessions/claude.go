package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NewClaudeScanner reads Claude Code's project transcripts, without hooks or
// access to a running process. An empty dir uses ~/.claude/projects, honoring
// CLAUDE_CONFIG_DIR. Subagent transcripts and sidechain records are excluded.
func NewClaudeScanner(dir string, activeWindow time.Duration) *Scanner {
	if strings.TrimSpace(dir) == "" {
		config := os.Getenv("CLAUDE_CONFIG_DIR")
		if config == "" {
			home, _ := os.UserHomeDir()
			config = filepath.Join(home, ".claude")
		}
		dir = filepath.Join(config, "projects")
	}
	scanner := NewScanner(dir, activeWindow)
	scanner.isClaude = true
	return scanner
}

type claudeUsage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

type claudeState struct {
	// Claude stores each assistant content block as a separate JSONL record,
	// repeating the API message id and usage. Count that response only once.
	messages  map[string]claudeUsage
	entries   map[string]bool
	outputIDs []string
}

func applyClaudeEntry(state *fileState, line []byte) {
	var entry struct {
		Type        string          `json:"type"`
		SessionID   string          `json:"sessionId"`
		UUID        string          `json:"uuid"`
		Cwd         string          `json:"cwd"`
		Timestamp   json.RawMessage `json:"timestamp"`
		IsSidechain bool            `json:"isSidechain"`
		IsMeta      bool            `json:"isMeta"`
		Effort      json.RawMessage `json:"effort"`
		Message     struct {
			ID      string          `json:"id"`
			Role    string          `json:"role"`
			Model   string          `json:"model"`
			Content json.RawMessage `json:"content"`
			Usage   claudeUsage     `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.IsSidechain || entry.SessionID == "" {
		return
	}
	switch entry.Type {
	case "user", "assistant", "system", "progress":
	default:
		return
	}
	if entry.Type == "user" || entry.Type == "assistant" {
		if entry.IsMeta || entry.Message.Role != entry.Type ||
			(entry.UUID == "" && (entry.Type != "assistant" || entry.Message.ID == "")) {
			return
		}
	}
	ts, hasTS := parseTimestamp(entry.Timestamp)
	if !hasTS {
		return
	}
	s := state.summary
	id := "claude-code:" + entry.SessionID
	if s.ID != "" && s.ID != id {
		return // mixed-session artifacts must not contaminate an existing summary
	}
	s.ID = id
	s.Provider = "claude-code"
	if s.StartedAt.IsZero() || ts.Before(s.StartedAt) {
		s.StartedAt = ts
	}
	if ts.After(s.LastActivity) {
		s.LastActivity = ts
	}
	if entry.Cwd != "" {
		s.Cwd = entry.Cwd
		s.Project = projectName(entry.Cwd)
	}
	if entry.Type != "user" && entry.Type != "assistant" {
		return
	}
	msg := entry.Message
	key := entry.Type + ":" + entry.UUID
	if entry.Type == "assistant" && msg.ID != "" {
		key = "assistant:" + msg.ID
	} else if entry.UUID == "" {
		return
	}
	if state.claude == nil {
		state.claude = &claudeState{messages: map[string]claudeUsage{}, entries: map[string]bool{}}
	}
	c := state.claude
	previous, seen := c.messages[key]
	if !seen {
		s.MessageCount++
	}
	// Usage may be repeated or grow as streaming blocks are persisted. Add
	// only the increase, including cache reads/writes in total input tokens.
	current := claudeUsage{
		Input: max(previous.Input, msg.Usage.Input), Output: max(previous.Output, msg.Usage.Output),
		CacheRead: max(previous.CacheRead, msg.Usage.CacheRead), CacheWrite: max(previous.CacheWrite, msg.Usage.CacheWrite),
	}
	inputDelta := current.Input - previous.Input + current.CacheRead - previous.CacheRead + current.CacheWrite - previous.CacheWrite
	outputDelta := current.Output - previous.Output
	s.InputTokens += inputDelta
	s.OutputTokens += outputDelta
	s.TotalTokens += inputDelta + outputDelta
	c.messages[key] = current
	if entry.Type == "assistant" {
		if msg.Model != "" && msg.Model != "<synthetic>" {
			s.Model = msg.Model
		}
		var effort string
		if json.Unmarshal(entry.Effort, &effort) != nil {
			var value struct {
				Level string `json:"level"`
			}
			if json.Unmarshal(entry.Effort, &value) == nil {
				effort = value.Level
			}
		}
		if effort != "" {
			s.ThinkingLevel = effort
		}
	}
	// Replayed records must not duplicate visible text. Usage is handled
	// above so a repeated record with newer counters can still update totals.
	if entry.UUID != "" {
		if c.entries[entry.UUID] {
			return
		}
		c.entries[entry.UUID] = true
	}
	if entry.Type == "user" {
		if s.FirstPrompt == "" {
			s.FirstPrompt = firstText(msg.Content)
		}
		return
	}
	text := capRaw(allText(msg.Content))
	if text == "" {
		return
	}
	for i, outputID := range c.outputIDs {
		if outputID == key {
			s.Outputs = append([]Output(nil), s.Outputs...)
			s.Outputs[i].Text = capRaw(s.Outputs[i].Text + "\n\n" + text)
			s.Outputs[i].At = ts
			return
		}
	}
	c.outputIDs = append([]string{key}, c.outputIDs...)
	s.Outputs = append([]Output{{Text: text, At: ts}}, s.Outputs...)
	if len(s.Outputs) > maxOutputs {
		s.Outputs = s.Outputs[:maxOutputs]
		c.outputIDs = c.outputIDs[:maxOutputs]
	}
}
