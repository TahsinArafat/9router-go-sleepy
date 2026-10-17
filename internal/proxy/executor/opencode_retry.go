package executor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"9router/proxy/internal/providers"
	"9router/proxy/internal/proxy"
)

// The OpenCode free-tier gate rejects a small, varying share of requests that
// carry a correct fingerprint — observed at roughly one in ten — with a 403
// whose body says "FreeTierError". That is a transient refusal of the request,
// not a verdict on the account: the same body succeeds moments later, with the
// same classification, headers and shaping. Measured while investigating:
// identical requests returned 200, then 403 for several minutes, then 200 again,
// and neither the body, the session id, its age nor its counter changed the
// outcome.
//
// A single refusal therefore must not reach the caller while retries remain.
const (
	// openCodeGateMaxAttempts is the total number of upstream attempts for one
	// request: the first try plus four retries. At a 10% per-attempt refusal
	// rate, five independent tries leave a residual failure rate near 1e-5.
	openCodeGateMaxAttempts = 5

	// openCodeGateRetryBase is the first backoff interval. The refusal is
	// immediate and unrelated to load, so the wait only needs to re-draw, not to
	// let a queue drain. A short base keeps the common case — one retry, about
	// 100 ms — invisible to someone waiting on a completion.
	openCodeGateRetryBase = 100 * time.Millisecond
)

// isOpenCodeFreeTierRefusal reports whether a response is the gate's transient
// FreeTierError refusal, as opposed to a real account, quota or auth problem.
//
// Only this refusal is retried. A 403 meaning "this key is out of credit" fails
// identically forever, so retrying it five times would burn quota and delay the
// failure the caller needs to see.
func isOpenCodeFreeTierRefusal(status int, body []byte) bool {
	if status != http.StatusForbidden {
		return false
	}
	// The gate writes {"type":"error","error":{"type":"FreeTierError",...}}.
	// Match the type token, not the prose: the message text has already changed
	// once ("free tier can only be used from within OpenCode" gained a
	// "Download it at ..." tail) and will change again.
	return bytes.Contains(body, []byte("FreeTierError"))
}

// openCodeGateBackoff returns the wait before the given retry (1-based) with
// full jitter. Jitter matters here beyond the usual thundering-herd reason: the
// refusal is a per-request draw, so retrying immediately would risk repeating
// the same unlucky outcome instead of making an independent attempt.
func openCodeGateBackoff(retry int) time.Duration {
	if retry < 1 {
		retry = 1
	}
	ceiling := openCodeGateRetryBase << (retry - 1) // 100, 200, 400, 800 ms
	if half := ceiling / 2; half > 0 {
		return half + rand.N(half)
	}
	return ceiling
}

// openCodeUpstreamResult is one settled upstream attempt: either a response the
// caller may stream from, or the error to report.
type openCodeUpstreamResult struct {
	resp *http.Response
	err  error
}

// forwardOpenCodeWithRetry issues the request to the OpenCode gate and retries a
// transient FreeTierError up to openCodeGateMaxAttempts times.
//
// This is the single place OpenCode requests are dialled, so the retry applies
// uniformly to every lane (free chat, free responses, zen chat, zen responses,
// zen messages, go) instead of being reimplemented per lane.
//
// Contract:
//   - success: (resp, nil); the caller owns resp.Body.
//   - exhausted retries, or a non-retryable failure: (nil, *proxy.UpstreamError)
//     carrying the LAST attempt's status and body, so the caller reports exactly
//     what upstream said. The retry hides a blip; it does not invent a
//     different error.
//   - transport failure: (nil, err) unchanged, because that is the failover
//     path's business and retrying here would duplicate it.
func forwardOpenCodeWithRetry(ctx context.Context, client *http.Client, cfg *providers.ProviderConfig, apiKey string, body []byte, isStream bool) openCodeUpstreamResult {
	var last *proxy.UpstreamError

	for attempt := 1; attempt <= openCodeGateMaxAttempts; attempt++ {
		resp, err := proxy.ForwardOpenAI(ctx, client, cfg, apiKey, body, isStream)

		// A non-200 arrives as an *UpstreamError in the error position: the
		// request layer converts the status before returning, so the response is
		// nil there. Anything else in the error position is a transport failure,
		// which the failover path owns and which must not be retried here.
		var ue *proxy.UpstreamError
		if err != nil {
			// A transport failure can still hand back a response with a partially
			// read body; release it so the connection is not leaked.
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if !errors.As(err, &ue) {
				return openCodeUpstreamResult{err: err}
			}
		}

		// Success hands the body to the caller, who owns and closes it.
		if err == nil && resp != nil && resp.StatusCode == http.StatusOK {
			return openCodeUpstreamResult{resp: resp}
		}

		// Every path below only reports, so the body is consumed and closed
		// here — unconditionally, and before any return.
		if ue == nil {
			if resp == nil || resp.Body == nil {
				return openCodeUpstreamResult{err: &proxy.UpstreamError{
					StatusCode: http.StatusBadGateway,
					Body:       []byte("opencode gate returned no response"),
				}}
			}
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
			_ = resp.Body.Close()
			ue = &proxy.UpstreamError{StatusCode: resp.StatusCode, Body: errBody, Header: resp.Header}
		}

		last = ue
		if !isOpenCodeFreeTierRefusal(ue.StatusCode, ue.Body) {
			return openCodeUpstreamResult{err: ue}
		}
		if attempt == openCodeGateMaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			// The caller went away; report the refusal rather than sleeping
			// through retries nobody is waiting for.
			return openCodeUpstreamResult{err: ue}
		case <-time.After(openCodeGateBackoff(attempt)):
		}
	}
	if last == nil {
		last = &proxy.UpstreamError{StatusCode: http.StatusForbidden, Body: []byte("opencode gate refused every attempt")}
	}
	return openCodeUpstreamResult{err: last}
}
