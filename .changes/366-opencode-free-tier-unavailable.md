### fix(opencode): report a closed free tier as unavailable instead of a raw 403

When the OpenCode free-tier gate refuses every retry, the caller used to
receive the upstream `403 FreeTierError` verbatim — a wall of text telling the
user to download OpenCode, mid tool-call. A 403 reads as "you are not allowed",
so an agent loop treats it as fatal and stops.

The gateway now reports the condition honestly instead of forwarding it:

- **`503` + `Retry-After: 30`** with `type: "free_tier_unavailable"` and a
  message naming the real cause and the real remedies (retry, a paid key, or
  another provider). The status says "temporarily unavailable, come back",
  which is what a closed shared free tier is; the upstream's wording is
  preserved in the server log for diagnosis but no longer reaches the client.
- **`Retry-After` is actually forwarded.** The error-to-response writer copied
  only `Content-Type` and dropped `ue.Header`, so a retry hint would have been
  silently discarded. It now forwards `Retry-After` and nothing else — copying
  the whole header map leaked the failed response's `Content-Length` and
  `Transfer-Encoding`, which made the reply advertise a body length it did not
  have and truncated it on the wire.

Two related defects were found by testing rather than reading:

- **A truncated error body erased the status.** `doRequestOnce` returned a bare
  `fmt.Errorf` when reading a non-200 body failed, discarding the code, so a
  403 with a mis-declared `Content-Length` became an opaque 502 that no retry
  or failover could classify. `UpstreamError` now carries the status plus a
  `ReadErr` field for the partial read.
- **The header-copy loop above broke an existing test** (`TestMiniMaxCodeCredits
  RefusalFailsOver`, which asserted a 429 and got a truncated body). Caught by
  the integration suite, fixed by forwarding only `Retry-After`.

Verified with `go build ./...`, `go test ./internal/proxy/...
./internal/handlers/chat/ ./internal/translator/ ./internal/providers/`,
`go test -tags=integration ./internal/integration/` (70s), and
`golangci-lint run --new-from-rev=origin/main` (0 issues). The end-to-end case
drives the real router against a fake gate that refuses every attempt *and*
declares a lying `Content-Length`: the client sees `503`, `Retry-After: 30`,
five upstream attempts, and no `FreeTierError` anywhere in the response.
