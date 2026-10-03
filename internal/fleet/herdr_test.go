package fleet

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTerminalURLsRequireHTTPS(t *testing.T) {
	got, err := ParseTargets("home|http://127.0.0.1:8787|https://home.hemsoft.net/")
	if err != nil || got[0].TerminalURL != "https://home.hemsoft.net/" {
		t.Fatalf("targets=%+v err=%v", got, err)
	}
	for _, u := range []string{"javascript:alert(1)", "http://home.hemsoft.net", "https://user:password@example.com", ""} {
		if _, err := ParseTargets("home|http://127.0.0.1:8787|" + u); err == nil {
			t.Fatalf("accepted terminal URL %q", u)
		}
	}
}

func TestLiveTelemetryRejectsStaleAndMalformedData(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		available  bool
	}{
		{"fresh", fmt.Sprintf(`{"available":true,"generatedAt":%q,"sessions":[]}`, time.Now().Format(time.RFC3339Nano)), 200, true},
		{"stale", `{"available":true,"generatedAt":"2020-01-01T00:00:00Z","sessions":[]}`, 200, false},
		{"malformed", `{"available":true}`, 200, false},
		{"older-collector", `{}`, 404, false},
		{"error", `{}`, 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer a.Close()
			s := NewServer(nil, time.Second)
			got := s.fetchHerdr(context.Background(), Target{Name: "home", URL: a.URL})
			if got.Available != tc.available {
				t.Fatalf("snapshot=%+v", got)
			}
			if !tc.available && got.Error == "" {
				t.Fatal("unavailable telemetry needs an error")
			}
		})
	}
}
