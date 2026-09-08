package usage

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// The live /go page carries a units counter (`monthlyUsage:455539320` followed
// by timeMonthlyUsageUpdated) BEFORE the hydration block; each window must
// scan past non-hydration occurrences to its real data.
func TestParseGoWindowsSkipsUnitsCounter(t *testing.T) {
	page := `<html>other stuff monthlyUsage:455539320,timeMonthlyUsageUpdated:$R[30]=new Date("2026-09-08T00:18:23.000Z"),` +
		`reloadError:null,rollingUsage:$R[34]={status:"ok",resetInSec:5181,usagePercent:47.8,usage:573776370,limit:1200000000},` +
		`weeklyUsage:$R[35]={status:"ok",resetInSec:512018,usagePercent:97.6,usage:2929247763,limit:3000000000},` +
		`monthlyUsage:$R[36]={status:"ok",resetInSec:1555200,usagePercent:73.2,usage:100,limit:200}</html>`
	gauges := parseGoWindows(page)
	if gauges == nil {
		t.Fatal("parse failed")
	}
	want := []struct {
		label string
		pct   float64
	}{
		{"5h", 47.8}, {"Weekly", 97.6}, {"Monthly", 73.2},
	}
	for i, w := range want {
		if gauges[i].Label != w.label || gauges[i].UsedPercent != w.pct {
			t.Fatalf("gauge %d = %+v, want %s %.1f", i, gauges[i], w.label, w.pct)
		}
	}
}

func TestParseGoWindowsEscapedAndPlain(t *testing.T) {
	if gauges := parseGoWindows(`{\"rollingUsage\":{\"usagePercent\":5,\"resetInSec\":1000},` +
		`\"weeklyUsage\":{\"usagePercent\":6,\"resetInSec\":100000},` +
		`\"monthlyUsage\":{\"usagePercent\":7,\"resetInSec\":1000000}}`); gauges == nil || len(gauges) != 3 {
		t.Fatalf("escaped = %+v", gauges)
	}
	if gauges := parseGoWindows(`"rollingUsage":{"usagePercent":5,"resetInSec":1000},` +
		`"weeklyUsage":{"usagePercent":6,"resetInSec":100000},` +
		`"monthlyUsage":{"usagePercent":7,"resetInSec":1000000}}`); gauges == nil || len(gauges) != 3 {
		t.Fatalf("plain = %+v", gauges)
	}
}

func TestParseGoWindowsIgnoresCaseTrap(t *testing.T) {
	// timeMonthlyUsageUpdated must not satisfy the monthly window even when
	// no hydration block exists.
	if gauges := parseGoWindows(`<p>timeMonthlyUsageUpdated:never</p>`); gauges != nil {
		t.Fatalf("expected nil, got %+v", gauges)
	}
}

func TestParseCodexWindowsNullSecondary(t *testing.T) {
	body := []byte(`{"rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":604800,"reset_after_seconds":603701,"reset_at":1789435382},"secondary_window":null}}`)
	gauges, err := parseCodexWindows(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(gauges) != 1 || gauges[0].Label != "Weekly" || gauges[0].UsedPercent != 0 {
		t.Fatalf("gauges = %+v", gauges)
	}
}

func TestParseAntigravityWindows(t *testing.T) {
	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	body := []byte(`{"groups":[{"buckets":[
		{"bucketId":"gemini-5h","window":"5h","remainingFraction":0.9,"resetTime":"` + future + `"},
		{"bucketId":"gemini-weekly","window":"weekly","remainingFraction":0.72,"resetTime":"` + future + `"},
		{"bucketId":"3p-5h","window":"5h","remainingFraction":0.5,"disabled":true,"resetTime":"` + future + `"},
		{"bucketId":"3p-weekly","window":"weekly","remainingFraction":0.1,"resetTime":"` + future + `"}]}]}`)
	gauges, err := parseAntigravityWindows(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(gauges) != 3 {
		t.Fatalf("gauges = %d, want 3 (disabled bucket excluded)", len(gauges))
	}
	if gauges[0].Label != "Gemini 5h" || gauges[0].UsedPercent != 10 {
		t.Fatalf("gemini 5h = %+v", gauges[0])
	}
	if gauges[1].Label != "Gemini W" || gauges[1].UsedPercent != 28 {
		t.Fatalf("gemini W = %+v", gauges[1])
	}
	if gauges[2].Label != "3P W" || gauges[2].UsedPercent != 90 {
		t.Fatalf("3p W = %+v", gauges[2])
	}
}

func TestParseAntigravityWindowsFailsClosed(t *testing.T) {
	if _, err := parseAntigravityWindows([]byte(`{"groups":[]}`)); err == nil {
		t.Fatal("expected error when no buckets qualify")
	}
}

func TestParseZenBalanceFromPage(t *testing.T) {
	// balance is carried in 1e-8 dollar units: 13151171185 = $131.51171185.
	card := zenCardFromPage([]byte(`html "balance":13151171185 more html`), time.Now())
	if card.BalanceUSD == nil || *card.BalanceUSD < 131.5 || *card.BalanceUSD > 131.52 {
		t.Fatalf("balance = %+v", card.BalanceUSD)
	}
	broken := zenCardFromPage([]byte(`no balance on this page`), time.Now())
	if broken.Error == "" || broken.BalanceUSD != nil {
		t.Fatalf("broken page = %+v", broken)
	}
}

func TestParseMoonshotBalance(t *testing.T) {
	if v, ok := parseMoonshotBalance(any(float64(12.5))); !ok || v != 12.5 {
		t.Fatalf("number balance = %v %v", v, ok)
	}
	if v, ok := parseMoonshotBalance(any("13.75")); !ok || v != 13.75 {
		t.Fatalf("string balance = %v %v", v, ok)
	}
	if _, ok := parseMoonshotBalance(any(nil)); ok {
		t.Fatal("nil should not parse")
	}
}

func TestNormalizeOpenCodeConfig(t *testing.T) {
	if got := normalizeWorkspaceID("https://opencode.ai/workspace/ws-123/billing"); got != "ws-123" {
		t.Fatalf("workspace id = %q", got)
	}
	if got := normalizeCookie("Cookie: auth=abc123; other=1"); got != "abc123" {
		t.Fatalf("cookie = %q", got)
	}
	if got := normalizeCookie("bearer tok"); got != "tok" {
		t.Fatalf("cookie = %q", got)
	}
}

func TestCodexAccountIDFromJWT(t *testing.T) {
	header := base64URLEncode(`{}`)
	payload := base64URLEncode(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc-777"}}`)
	token := header + "." + payload + ".sig"
	if got := codexAccountIDFromJWT(token); got != "acc-777" {
		t.Fatalf("account id = %q", got)
	}
	if got := codexAccountIDFromJWT("not.a.jwt"); got != "" {
		t.Fatalf("garbage token should yield empty id, got %q", got)
	}
}

func TestAgentUsageEndpointServesCachedSnapshot(t *testing.T) {
	// The agent's /usage handler must serve the cache, not refetch per hit.
	store := NewStore(New(), "home", time.Minute)
	store.snapshot = Snapshot{Machine: "home", Cards: []Card{{Key: "xai", Label: "xAI (Grok)", Kind: KindUnknown}}}
	store.readyOnce.Do(func() { close(store.ready) })
	got := store.Snapshot()
	if got.Machine != "home" || len(got.Cards) != 1 || got.Cards[0].Kind != KindUnknown {
		t.Fatalf("usage snapshot = %+v", got)
	}
}

func TestContextCancellationPropagates(t *testing.T) {
	f := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, card := range f.FetchAll(ctx) {
		// xai never fetches; every real fetcher must report an error, never
		// panic, when the context is already cancelled.
		if card.Key == "xai" {
			continue
		}
		if card.Error == "" {
			t.Fatalf("card %s unexpectedly succeeded with cancelled context", card.Key)
		}
	}
}

func base64URLEncode(s string) string {
	return strings.TrimRight(base64.StdEncoding.EncodeToString([]byte(s)), "=")
}
