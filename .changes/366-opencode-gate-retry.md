### fix(opencode): retry a transient free-tier refusal instead of surfacing a 403

The OpenCode free-tier gate refuses a small, varying share of requests that
carry a correct fingerprint — observed at roughly one in ten — with a 403 whose
body says `FreeTierError`. The same request succeeds moments later with
identical classification, headers and shaping, so the refusal is a per-request
draw, not a verdict on the account or the body.

- **Retry the same connection up to five attempts** before reporting anything,
  with short jittered backoff (100/200/400/800 ms, worst case under 1.5 s). At a
  10% per-attempt rate, five independent draws leave a residual failure rate
  near 1e-5. Implemented once in `forwardOpenCodeWithRetry` and used by every
  OpenCode lane — free chat, free responses, zen chat, zen responses, zen
  messages and go — rather than reimplemented per lane.
- **Only the `FreeTierError` refusal is retried.** A 403 meaning "insufficient
  credit" or an auth failure fails identically forever; retrying it five times
  would burn quota and delay the failure the caller needs to see. The classifier
  matches the error `type` token, not the prose, because the message text has
  already been reworded once.
- **Transport failures and other statuses are untouched.** 502/503/504 keep
  their existing transport-level retry inside `DoRequest`, and 429/401 still go
  straight to account failover.
- **A refusal no longer cools the connection down.** Once retries are exhausted,
  the fallback layer reports the refusal without locking the account or bumping
  its backoff level. The refusal says nothing about the connection, and cooling
  a healthy account down would turn a per-request lottery into an outage by
  skipping it for later requests. Scoped to the OpenCode providers, so every
  other provider's 403 handling is unchanged.

Two behaviours were verified by mutation rather than assumed: setting the
attempt budget to 1 turns `TestForwardOpenCodeWithRetryRetriesRefusalThenSucceeds`
red, and the first implementation of the helper was wrong — a non-200 arrives as
an `*UpstreamError` in the error position, so the retry silently never fired
until the helper learned to unwrap it. That defect was caught by the tests, not
by reading the code.

Verified with `go build ./...`, `go test ./internal/proxy/...
./internal/handlers/chat/ ./internal/providers/ ./internal/translator/
./internal/pricing/`, and `go test -tags=integration ./internal/integration/`.
Live refusal rate measured at 0/30 at the time of writing, which is why the
retry budget is pinned by unit tests with a scripted fake gate rather than by a
live assertion: the real rate varies with upstream availability.
