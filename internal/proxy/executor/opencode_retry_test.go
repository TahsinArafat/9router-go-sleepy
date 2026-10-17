package executor

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"9router/proxy/internal/providers"
	"9router/proxy/internal/proxy"
)

// The gate refuses a varying share of correctly-fingerprinted requests. These
// tests pin the two properties that make retrying safe and useful: it fires on
// that specific refusal, and it stops for everything else.

// freeTierRefusal is the gate's transient 403 body, verbatim.
const freeTierRefusal = `{"type":"error","error":{"type":"FreeTierError","message":"OpenCode's free tier can only be used from within OpenCode. Download it at https://opencode.ai and keep building."}}`

// gateStub answers with a scripted status/body per attempt and records how many
// calls it saw. The last script entry repeats for any further attempts.
func gateStub(t *testing.T, script ...func(w http.ResponseWriter)) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&calls, 1)) - 1
		pick := n
		if pick >= len(script) {
			pick = len(script) - 1
		}
		script[pick](w)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func refuseWith(w http.ResponseWriter, status int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func succeedWith(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
}

func testCfg(srv *httptest.Server) *providers.ProviderConfig {
	return &providers.ProviderConfig{
		BaseURL:       srv.URL,
		StaticHeaders: map[string]string{},
	}
}

// A refusal followed by a success must reach the caller as a success, and the
// caller must never see the first attempt's 403.
func TestForwardOpenCodeWithRetryRetriesRefusalThenSucceeds(t *testing.T) {
	srv, calls := gateStub(t, refuseWith(nil, http.StatusForbidden, freeTierRefusal), succeedWith)

	res := forwardOpenCodeWithRetry(context.Background(), srv.Client(), testCfg(srv), "public", []byte(`{}`), true)
	if res.err != nil {
		t.Fatalf("a transient refusal must be retried, got error: %v", res.err)
	}
	defer res.resp.Body.Close()
	if res.resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 after the retry", res.resp.StatusCode)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Errorf("upstream saw %d attempts, want 2 (one refusal, one success)", got)
	}
}

// The whole point: a run of refusals must not surface until the budget is spent.
func TestForwardOpenCodeWithRetryExhaustsAndReportsLastRefusal(t *testing.T) {
	srv, calls := gateStub(t, refuseWith(nil, http.StatusForbidden, freeTierRefusal))

	res := forwardOpenCodeWithRetry(context.Background(), srv.Client(), testCfg(srv), "public", []byte(`{}`), true)
	if res.err == nil {
		t.Fatal("every attempt was refused, so an error is required")
	}
	if got := atomic.LoadInt32(calls); got != openCodeGateMaxAttempts {
		t.Errorf("upstream saw %d attempts, want %d", got, openCodeGateMaxAttempts)
	}
	ue, ok := res.err.(*proxy.UpstreamError)
	if !ok {
		t.Fatalf("error type = %T, want an upstream error carrying the last body", res.err)
	}
	if ue.StatusCode != http.StatusForbidden {
		t.Errorf("reported status = %d, want the last attempt's 403", ue.StatusCode)
	}
	if !bytes.Contains(ue.Body, []byte("FreeTierError")) {
		t.Errorf("reported body should carry what upstream said, got %s", ue.Body)
	}
}

// A 403 that is NOT the free-tier refusal means the account or key is wrong and
// will fail identically forever. Retrying it would burn quota and delay the
// failure the caller needs to see.
func TestForwardOpenCodeWithRetryDoesNotRetryRealForbidden(t *testing.T) {
	srv, calls := gateStub(t, refuseWith(nil, http.StatusForbidden, `{"error":{"message":"insufficient credit"}}`))

	res := forwardOpenCodeWithRetry(context.Background(), srv.Client(), testCfg(srv), "public", []byte(`{}`), true)
	if res.err == nil {
		t.Fatal("a real 403 must be reported")
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("upstream saw %d attempts, want exactly 1 — a quota 403 must not be retried", got)
	}
}

// Other retryable statuses keep their existing handling (account failover,
// connection cooldown), so this layer must not add retries of its own.
//
// 502/503/504 already carry a transport-level retry inside DoRequest
// (proxy.transientRetryPolicy), so the expected count here is that policy's,
// not one: the assertion is that this wrapper adds nothing on top of it.
func TestForwardOpenCodeWithRetryPassesThroughOtherStatuses(t *testing.T) {
	cases := []struct {
		status    int
		wantCalls int32
		reason    string
	}{
		{http.StatusUnauthorized, 1, "401 is the failover path's business"},
		{http.StatusTooManyRequests, 1, "429 hands the account to failover untouched"},
		{http.StatusBadGateway, 4, "3 transport-level retries already exist, plus the first try"},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv, calls := gateStub(t, refuseWith(nil, tc.status, `{"error":{"message":"x"}}`))
			res := forwardOpenCodeWithRetry(context.Background(), srv.Client(), testCfg(srv), "public", []byte(`{}`), true)
			if res.err == nil {
				t.Fatalf("%d must be reported as an error", tc.status)
			}
			if got := atomic.LoadInt32(calls); got != tc.wantCalls {
				t.Errorf("upstream saw %d attempts, want %d — %s", got, tc.wantCalls, tc.reason)
			}
		})
	}
}

// A caller that has gone away must not be kept waiting through the backoff.
func TestForwardOpenCodeWithRetryStopsOnCancelledContext(t *testing.T) {
	srv, calls := gateStub(t, refuseWith(nil, http.StatusForbidden, freeTierRefusal))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := forwardOpenCodeWithRetry(ctx, srv.Client(), testCfg(srv), "public", []byte(`{}`), true)
	if res.err == nil {
		t.Fatal("a cancelled request must not report success")
	}
	if got := atomic.LoadInt32(calls); got > 2 {
		t.Errorf("upstream saw %d attempts after cancellation; the backoff should have stopped early", got)
	}
}

// Backoff must stay small and bounded: this hides a blip, it does not stall a
// completion behind a multi-second wait.
func TestOpenCodeGateBackoffIsBounded(t *testing.T) {
	for retry := 1; retry < openCodeGateMaxAttempts; retry++ {
		got := openCodeGateBackoff(retry)
		max := openCodeGateRetryBase << (retry - 1)
		if got <= 0 || got > max {
			t.Errorf("backoff for retry %d = %v, want within (0, %v]", retry, got, max)
		}
	}
	// Four retries of at most 800ms each keep the worst case under ~1.5s.
	var total = time.Duration(0)
	for retry := 1; retry < openCodeGateMaxAttempts; retry++ {
		total += openCodeGateRetryBase << (retry - 1)
	}
	if total > 2*time.Second {
		t.Errorf("total worst-case backoff %v is too long for an interactive request", total)
	}
}

// The classifier is the safety boundary: it must match the gate's own type
// token and nothing else.
func TestIsOpenCodeFreeTierRefusal(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"gate refusal", http.StatusForbidden, freeTierRefusal, true},
		{"refusal with no prose", http.StatusForbidden, `{"error":{"type":"FreeTierError"}}`, true},
		{"quota 403", http.StatusForbidden, `{"error":{"message":"insufficient credit"}}`, false},
		{"auth 401", http.StatusUnauthorized, freeTierRefusal, false},
		{"429 with the token", http.StatusTooManyRequests, freeTierRefusal, false},
		{"empty body", http.StatusForbidden, ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOpenCodeFreeTierRefusal(tc.status, []byte(tc.body)); got != tc.want {
				t.Errorf("isOpenCodeFreeTierRefusal(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}
