# About this fork

9router-go-sleepy is an independent personal fork maintained by Tahsin Arafat. It is based on luqman-v1/9router-go, the native Go companion to decolua/9router.

This fork is not the official repository for either upstream project. The name 9router-go-sleepy identifies this GitHub repository; the Go module remains 9router/proxy, and the built CLI remains 9router-go for compatibility.

## Attribution and license

The upstream projects and their contributors retain ownership of their work. Their notices and license are preserved in this repository; see LICENSE. Changes authored specifically for this fork are identified in commits and documented here. Please consult upstream for its current contribution, trademark, and support policies.

## How this fork relates to upstream

- luqman-v1/9router-go is the base code and source for Go-specific upstream fixes and releases.
- decolua/9router is the behavioral reference for cross-language parity. JavaScript/TypeScript changes are ported to idiomatic Go, not copied as JS.
- Upstream releases are not automatically published as releases of this fork. Verify compatibility and tests before relying on a fork build in production.
- Database compatibility follows the upstream Go project's contract and documentation. Back up important data before switching binaries.

## Fork-specific change log

### OpenCode v2 free-tier compatibility (decolua/9router#4842)

Ported the behavior of decolua/9router PR #4842 to the Go executor. The free OpenCode lane now targets /inference/v1, uses the v2 client User-Agent and session headers, omits the rejected x-opencode-request header, and declares OpenCode's exact read and shell tool fingerprints. Caller tools that conflict with those fingerprint names are aliased and restored in responses; missing fingerprint tools receive a system instruction identifying them as placeholders. Free opencode-zen models follow the same /inference/v1 prefix on their lane while paid zen models keep /zen/v1; the classification is catalog-driven (providers.IsOpencodeFreeTierModel), not suffix matching. Proposed upstream as luqman-v1/9router-go#319.

### Harness-body canonicalization for the OpenCode v2 gate (fork follow-up)

Direct API calls through the fork returned 200 while harness clients (Claude Code-style capitalised tools, forced tool_choice, schema-less tool declarations, cache_control keys, nameless prior function_call items) hit 403 FreeTierError: the v2 gate inspects more of the request than the plain fingerprint covers. The v2 pass now canonicalizes whatever the caller sends into the form the gate accepts — tools rebuilt into the canonical shape with default object schemas, tool_choice "none"/object forms collapsed to "auto", forced choices retargeted or dropped per the upstream rules, and upstream's forceAutoToolChoiceModels quirk ported for confirmed auto-only models. The goal is that the caller's harness never decides between 200 and 403.

Focused tests:

    go test ./internal/providers/... ./internal/proxy/... ./internal/translator/... -count=1

The full go test ./... command may depend on host-specific process inspection; see its output on the machine running the tests.

## Versioned container releases

The container workflow publishes two kinds of image tags:

- every push to `main` -> `latest` plus a `sha-<commit>` tag;
- every `v*` git tag -> the version without the leading `v`, e.g. tag
  `v1.9.12-sleepy.1` publishes `ghcr.io/tahsinarafat/9router-go-sleepy:1.9.12-sleepy.1`.
  A tag push never rewrites `latest`.

Fork versions use the `-sleepy.N` suffix on top of the upstream baseline
(VERSION file) they were cut from, so `1.9.12-sleepy.1` reads as "upstream
1.9.12 plus the first set of fork changes". Pin server deployments to the
versioned tag and treat `latest` as the floating convenience tag.

## Syncing and reporting issues

Before syncing a Go change, inspect the corresponding upstream JavaScript implementation and tests, then port the external behavior while following the Go project's provider-isolation and engineering rules in AGENTS.md. When reporting a bug that also affects upstream, include a reproducible report there as well. Fork-only issues and changes belong in this repository.
