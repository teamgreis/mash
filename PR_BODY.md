## Summary

Three defects surfaced by a full verification pass over both implementations: a
functional decode break in mash-py, a deferred-evaluation bug in mash-go, and
non-determinism in the docs generator.

## 1. mash-py: cbor2 6.x decode break — 15 failing tests

cbor2 6.x decodes values nested inside a CBOR tag as **immutable** containers
(`frozendict` / `tuple` / `frozenset`) regardless of the `immutable=` flag,
while 5.x returned plain `dict` / `list` / `set`. DEC-083 wraps every MASH
control message in CBOR tag 77, so control frames are the common path — and
`cbor2.frozendict` is **not** a `dict` subclass (it only satisfies `Mapping`),
so every `isinstance(value, dict)` check in the decoders rejected it.

Observed failures:

- 8 control-message round-trip / cross-language vector tests failed outright
- every operational connection aborted its close handshake
  (`malformed control frame: ... got frozendict`), surfacing as ping/pong
  timeouts and 5 device-service round-trip failures

**Fix:** new `mash_py/wire/_mutable.py` normalises decode results back to plain
types at a single choke point; all 9 `cbor2.loads` call sites in `mash_py/`
route through it (`wire.codec`, `wire.peek`, `commissioning.messages`,
`commissioning.renewal_messages`).

## 2. mash-py: test bug in the goodbye test

`test_unpublish_commissionable_emits_goodbye` opened a **second** browse stream
to wait for `REMOVED`. Each `browse_commissionable()` call binds a fresh
`AsyncServiceBrowser` to its own queue (documented behaviour), so a stream
opened *after* the unpublish starts with an empty cache and can never observe a
goodbye that was already delivered. The test now consumes one stream for both
states. `browse_commissionable` / `browse_operational` return types narrowed to
`AsyncGenerator` so `aclose()` is part of the contract, and the single-stream
requirement is documented on the method.

## 3. mash-go: `go vet` finding

`coordinator.go:860` — `defer c.debugf(..., time.Since(start))` evaluates its
arguments at defer time, so teardown duration always logged ~0s. Wrapped in a
closure.

## 4. mash-docgen: non-deterministic output

`make docs-check` regenerates `docs/` and mkdocs compares the result, so the
generator must be a pure function of its YAML input. It wasn't: three runs over
identical input produced 8, 3 and 6 differing files respectively.

Four loops iterated Go maps directly and wrote the iteration order straight
into the output. Go deliberately randomises map iteration order, so every run
shuffled those sections:

- `diagrams.go` `DeviceCompositionDiagram` — feature nodes in the per-endpoint Mermaid composition graph
- `diagrams.go` `FeatureCrossRefDiagram` — feature nodes in the feature↔use-case cross-reference graph
- `generate.go` `writeEndpointConformance` — the `### <Feature>` blocks and their mandatory/recommended attribute lists on every endpoint page
- `generate.go` feature-index path (same features map)

**Fix:** added `sortedKeys[V]` (`slices.Sorted(maps.Keys(...))`) in `helpers.go`
and routed all four loops through it. Generic, so it covers both the
`map[string]map[string]*RawConformance` and `map[string][]string` shapes.

This matters practically, not just cosmetically: `docs-check` is meant to be a
CI gate, and with random output it fails intermittently — the kind of bug that
costs a week to chase.

## 5. Spec drift: closes spec issue #14

`commissioning-pase.md §6.5` mandated a 100-500 ms random delay, which is
**weaker** than what both reference implementations actually ship (a
constant-time minimum-duration floor). On ESP32-class devices the SPAKE2+
compute path exceeds the window by two orders of magnitude, so the random delay
does not close the oracle it targets. Rewritten per the recommended disposition
in `mash-py-spec-issues.md` #14: the normative MUST is now the wire-observable
property, with the constant-time floor as the recommended conformant
implementation and bounded random delay retained, scoped to device classes
where it works. No code change needed in either implementation.

Also fixed the DEC-084 TOC anchor in `decision-log.md` (double hyphen collapsed
differently than the heading slug).

## Verification

| Check | Before | After |
|---|---|---|
| mash-py unit + vectors (non-interop) | 15 failed, 824 passed | **839 passed, 0 failed** |
| mash-go `go vet ./...` | 1 finding | clean |
| mash-go `go build ./...` | OK | OK |
| mash-go `go test ./...` | green | green |
| `codegen --check` (mash-py) | 15 files matching | 15 files matching |
| mash-docgen determinism (5 runs) | 8 / 3 / 6 differing files | **0 differing files** |

Discovery suite re-run 3× after the test fix to confirm it isn't flaky.

## Notes

- `docs/generated/` is gitignored, so the docgen output is not part of this diff — only the generator change is.
- `cmd/mash-docgen/model.go` is not `gofmt`-clean, but that predates this change and is deliberately left out of scope.
