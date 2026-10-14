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

Ported the behavior of decolua/9router PR #4842 to the Go executor. The free OpenCode lane now targets /inference/v1, uses the v2 client User-Agent and session headers, omits the rejected x-opencode-request header, and declares OpenCode's exact read and shell tool fingerprints. Caller tools that conflict with those fingerprint names are aliased and restored in responses; missing fingerprint tools receive a system instruction identifying them as placeholders. The paid opencode-zen lane remains separate and retains its /zen/v1 behavior.

Focused tests:

    go test ./internal/proxy ./internal/proxy/executor ./internal/translator

The full go test ./... command may depend on host-specific process inspection; see its output on the machine running the tests.

## Syncing and reporting issues

Before syncing a Go change, inspect the corresponding upstream JavaScript implementation and tests, then port the external behavior while following the Go project's provider-isolation and engineering rules in AGENTS.md. When reporting a bug that also affects upstream, include a reproducible report there as well. Fork-only issues and changes belong in this repository.
