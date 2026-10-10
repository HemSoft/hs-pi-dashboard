package sessions

import (
	"testing"
	"time"
)

func TestClaudeDedupPreservesIndependentLiveActivityAndOutput(t *testing.T) {
	now := time.Now()
	for _, status := range []string{"idle", "stopped", "stale", "unavailable"} {
		t.Run(status, func(t *testing.T) {
			start := now.Add(-time.Hour)
			t3 := Summary{Source: "t3", ID: "t3:env:thread", Name: "T3 title", Project: "repo", Provider: "claudeAgent", ProviderSessionID: "shared", Status: status, LastActivity: start, TurnStartedAt: &start}
			native := Summary{ID: "claude-code:shared", Provider: "claude-code", Active: true, LastActivity: now, MessageCount: 9, Outputs: []Output{{Text: "fresh native output", At: now}}}
			merged := MergeSnapshots(Snapshot{Sessions: []Summary{t3}}, Snapshot{Sessions: []Summary{native}})
			if len(merged.Sessions) != 1 || merged.ActiveSessions != 1 {
				t.Fatalf("duplicates or live activity lost: %+v", merged)
			}
			row := merged.Sessions[0]
			if row.ID != t3.ID || row.Name != t3.Name || row.Status != status || row.ActivitySource != "claude-code" || row.TurnStartedAt != nil || row.LastActivity != now || row.MessageCount != 9 || len(row.Outputs) != 1 {
				t.Fatalf("merged representation: %+v", row)
			}
			native.Active = false
			merged = MergeSnapshots(Snapshot{Sessions: []Summary{native}}, Snapshot{Sessions: []Summary{t3}})
			if merged.ActiveSessions != 0 || merged.Sessions[0].ActivitySource != "" {
				t.Fatal("cached records invented liveness")
			}
		})
	}
}
