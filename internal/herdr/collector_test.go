package herdr

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLiveCollector(t *testing.T) {
	if os.Getenv("HERDR_LIVE_TEST") != "1" {
		t.Skip("set HERDR_LIVE_TEST=1 to query the local Herdr installation")
	}
	c := New("")
	c.Refresh(context.Background())
	s := c.Snapshot()
	if !s.Available || s.Error != "" {
		t.Fatalf("live collector unavailable: %s", s.Error)
	}
	for _, session := range s.Sessions {
		if session.Error != "" {
			t.Fatalf("running session could not be inspected: %s", session.Error)
		}
	}
	t.Logf("Read-only collection succeeded for %d running sessions", len(s.Sessions))
}

const livePayload = `{"result":{"snapshot":{"version":"0.9.0","panes":[{},{}],"workspaces":[{"workspace_id":"w1","label":"agent-dashboard"}],"agents":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","agent":"pi","agent_status":"blocked","cwd":"/work/dashboard"}]}}}`

func TestCollectorReadsOnlyRunningSessions(t *testing.T) {
	var calls [][]string
	c := &Collector{run: func(ctx context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{}, args...))
		if reflect.DeepEqual(args, []string{"session", "list", "--json"}) {
			return []byte(`{"sessions":[{"name":"default","running":false},{"name":"browser","running":true}]}`), nil
		}
		return []byte(livePayload), nil
	}}
	c.Refresh(context.Background())
	s := c.Snapshot()
	if !s.Available || s.Error != "" || len(s.Sessions) != 1 || s.GeneratedAt.IsZero() {
		t.Fatalf("snapshot = %+v", s)
	}
	if !reflect.DeepEqual(calls, [][]string{{"session", "list", "--json"}, {"--session", "browser", "api", "snapshot"}}) {
		t.Fatalf("non-read-only or unexpected calls = %#v", calls)
	}
	session := s.Sessions[0]
	if session.Name != "browser" || session.PaneCount != 2 || len(session.Agents) != 1 {
		t.Fatalf("session = %+v", session)
	}
	a := session.Agents[0]
	if a.State != "blocked" || a.Workspace != "agent-dashboard" || a.Agent != "pi" || a.CWD != "/work/dashboard" {
		t.Fatalf("agent = %+v", a)
	}
}

func TestCollectorFailureIsNotAnEmptyHealthyFleet(t *testing.T) {
	c := &Collector{run: func(context.Context, ...string) ([]byte, error) {
		return nil, errors.New("private diagnostic must not leak")
	}}
	c.Refresh(context.Background())
	s := c.Snapshot()
	if s.Available || s.Error == "" || len(s.Sessions) != 0 || strings.Contains(s.Error, "private") {
		t.Fatalf("snapshot = %+v", s)
	}
}

func TestCollectorDoesNotKeepStaleLiveAgents(t *testing.T) {
	failing := false
	c := &Collector{run: func(_ context.Context, args ...string) ([]byte, error) {
		if failing {
			return nil, errors.New("offline")
		}
		if args[0] == "session" {
			return []byte(`{"sessions":[{"name":"default","running":true}]}`), nil
		}
		return []byte(livePayload), nil
	}}
	c.Refresh(context.Background())
	if len(c.Snapshot().Sessions[0].Agents) != 1 {
		t.Fatal("missing live fixture")
	}
	failing = true
	c.Refresh(context.Background())
	if c.Snapshot().Available || len(c.Snapshot().Sessions) != 0 {
		t.Fatal("stale agents presented as live")
	}
}

func TestCollectorSeparatesFailedSessionFromHealthySession(t *testing.T) {
	c := &Collector{run: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "session" {
			return []byte(`{"sessions":[{"name":"broken","running":true},{"name":"healthy","running":true}]}`), nil
		}
		if args[1] == "broken" {
			return nil, errors.New("connection refused")
		}
		return []byte(`{"result":{"snapshot":{"panes":[],"agents":[]}}}`), nil
	}}
	c.Refresh(context.Background())
	s := c.Snapshot()
	if !s.Available || len(s.Sessions) != 2 || s.Sessions[0].Error == "" || s.Sessions[1].Error != "" {
		t.Fatalf("snapshot = %+v", s)
	}
}

func TestMalformedResponsesAreNotHealthy(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"sessions":null}`, `not-json`} {
		t.Run(body, func(t *testing.T) {
			c := &Collector{run: func(context.Context, ...string) ([]byte, error) { return []byte(body), nil }}
			c.Refresh(context.Background())
			if c.Snapshot().Available || c.Snapshot().Error == "" {
				t.Fatal("accepted invalid session list")
			}
		})
	}
	for _, body := range []string{`null`, `{}`, `{"result":{"snapshot":{}}}`, `not-json`} {
		if decodeSession([]byte(body), &Session{}) == nil {
			t.Fatalf("accepted invalid snapshot %s", body)
		}
	}
}

func TestUnknownStateIsNotInvented(t *testing.T) {
	var s Session
	if err := decodeSession([]byte(strings.Replace(livePayload, "blocked", "future-state", 1)), &s); err != nil {
		t.Fatal(err)
	}
	if s.Agents[0].State != "unknown" {
		t.Fatal("unrecognized state should be unknown")
	}
}

func TestCallerPaneEnvironmentIsRemoved(t *testing.T) {
	env := []string{"PATH=/bin", "HERDR_ENV=1", "HERDR_SOCKET_PATH=caller", "herdr_pane_id=w1:p1", "HOME=/home/test"}
	want := []string{"PATH=/bin", "HOME=/home/test"}
	if got := withoutCallerContext(env); !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %v", got)
	}
}

func TestCommandOutputIsBounded(t *testing.T) {
	var b limitedBuffer
	if _, err := b.Write(make([]byte, 4<<20)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte{1}); err == nil {
		t.Fatal("accepted oversized response")
	}
	if b.Len() != 4<<20 {
		t.Fatal("buffer grew beyond its bound")
	}
}
