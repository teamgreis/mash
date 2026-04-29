# MASH Conformance Status (2026-04-29, post-fix)

This file is a triage snapshot of where each implementation stands against the
shared YAML conformance corpus in `mash-go/testdata/cases/` (494 unique IDs;
501 case definitions including 5 duplicates and 11 `env:multi-device` excluded
on this single-host run).

The matrix has four quadrants — `{Go runner, Python runner} × {Go device,
Python device}`. The diagonals (same-language ends) measure self-conformance;
the off-diagonals are the real cross-stack interop signal.

## TL;DR — F-1 + F-4 + F-2 + F-7 + F-6 fixes shipped today

Two sessions on 2026-04-29 shipped five named fixes:

- **Session 1 (morning):** F-1 (operational SRV-resolve canonical zone-id) and
  F-4 (Go device listener stays up). Together those moved Go-on-Go from 12.5%
  to 77.4% by eliminating 256 environmental connection-refused failures.
- **Session 2 (afternoon):** F-2 (pairing-request announce was missing the
  zone-id field), F-7 (mash-py subscribe handler outputs + save checkers),
  and F-6 (TC-E2E-001 inter-test cert leak — two cascading bugs in the
  residual zone sweep + integer-keyed-parameter regression).

After session 2: all 5 TC-PAIR-* tests no longer hit "missing required field"
(2/5 pass; 3 fail on unrelated issues now visible). TC-E2E-001 + TC-TRANS-*
pass cleanly back-to-back (was: TC-E2E-001 fails after TC-TRANS-004 due to
leaked LOCAL zone). The 75 device-side feature failures and the 3
remaining-but-not-F-2 PAIR failures are the next layer of work.

## TL;DR — earlier (F-1 + F-4) data

| | Before fixes | After fixes |
|---|---|---|
| Go-on-Go pass rate | **12.5 %** | **77.4 %** (× 6.2) |
| Connection-refused failures | 256 | **0** |
| PASE-EOF / reset failures | 27 | **0** |
| Suite-zone-disconnect failures | 18 | **0** |
| Real assertion failures | 5 | 75 (now visible — were buried in noise) |

The morning's "12.5 % pass rate" was an artifact: 256 of 314 failures were a
single environmental cluster (kernel-level `ECONNREFUSED` after the Go device
auto-stopped its listener between tests) plus runner-side zone-id confusion
that broke per-zone bookkeeping. Today's two fixes (F-1 in mash-go runner,
F-4 in mash-go device) eliminated that noise entirely. The 81 remaining
failures are real test signal — 75 feature assertion failures the device
implementation can address one by one, plus 5 F-2 pairing-request runner
bugs and 1 F-6 cert-state leak.

## Headline numbers (post-fix)

| Runner → Device | Discovered | Skipped (PICS) | Ran | Passed | Failed | Pass rate (of run) | Notes |
|---|---|---|---|---|---|---|---|
| `mash-test` → Go device     | 461 | 102 | 359 | **278** | 81  | **77.4 %** | 75 expectation failures + 5 F-2 (pairing) + 1 F-6 (cert leak). Zero environmental noise. |
| `mash-test` → Python device | *to re-run* | — | — | — | — | *unblocked* | F-1 fix means `-target <comm-port> -srv-resolve` now works correctly — operational SRV-resolve uses the canonical zone-id derived from the Zone CA cert. TC-CONN-005 verified passing against mash-py. |
| `mash-py-test` → Go device  | 134 | 0   | 134 | **2**   | 132 | **1.5 %**  | Earlier subset run. 57 fails are Python harness gaps (`unknown action: …`); 24 are PASE timeouts; 32 are genuine assertion failures (mostly subscription/read flow). Tracked as F-3 / F-7. |
| `mash-py-test` → Py device  | 490 | 0   | aborted | — | — | — | Aborted at 14 tests (~2 min in). Phase 22 baseline: **21/55 = 38 %** for the curated subset that the Python harness can execute. Tracked as F-3. |

PICS auto-discovery is only implemented in the Go runner. The Python runner
runs every YAML case unless a static `--pics` file is supplied, which is why
its "Discovered" counts are larger and its failure pile is dominated by tests
the Python harness cannot execute at all (unknown actions). Quadrants 3-4 are
not yet directly comparable to 1-2.

## Fixes shipped (afternoon session — F-2 + F-7 + F-6)

### F-6: TC-E2E-001 inter-test cert leak

**Change:** `mash-go/internal/testharness/runner/coordinator.go`,
`mash-go/internal/testharness/runner/coordinator_test.go`,
`mash-go/internal/testharness/runner/diagnostics.go`,
`mash-go/internal/testharness/runner/diagnostics_test.go`,
`mash-go/pkg/service/test_event_handler.go`. Commit `2d46a23`.

Two cascading bugs that together silently disabled the corrective
residual-zone-sweep at teardown:

1. **The sweep was gated on `-strict-lifecycle`** (a fail-on-leak
   diagnostic flag). Cleanup must run regardless of the diagnostic
   surface; silent leaks otherwise cascade into the next test's
   commission. The TestCoordTeardown_SuiteAlive_RestoresMainBeforeProbe
   fixture had to be updated to attach a framer to its "alive" suite
   conn (matching production state) so the sweep's framer-nil check
   doesn't force an unnecessary ReconnectToZone.

2. **`requestDeviceState` sent string-keyed parameters**
   (`map[string]any{"enableKey": ...}`), regressing the integer-key
   convention from Phase 11 / Issue #11. The device's command machinery
   walks required parameter IDs as `map[uint8]any` keys and rejects
   with `StatusInvalidParameter` (code 5) before reaching the
   enable-key check. The residual sweep then saw a nil snapshot and
   bailed. Switched to integer keys via the new exported constant
   `service.TestControlCmdGetTestStateParamEnableKey`.

**Result:** TC-E2E-001 + TC-TRANS-* pass back-to-back (the documented
F-6 reproducer flips from FAIL→PASS). The sweep now actually issues
RemoveZone for leaked non-suite zones, which is what the residual-sweep
code was designed to do all along.

### F-2: Pairing-request announce synthesizes a Zone ID when YAML omits it

### F-2: Pairing-request announce synthesizes a Zone ID when YAML omits it

**Change:** `mash-go/internal/testharness/runner/discovery_handlers.go`,
`mash-go/internal/testharness/runner/discovery_handlers_test.go`. Commit
`c013153`.

YAML pairing-request cases (TC-PAIR-001..005) pass `controller_id:
"{{ controller_id }}"` but the runner's `handleAnnouncePairingRequest`
read `params[KeyZoneID]` instead, and `{{ controller_id }}` was an
unset state template. The empty zone-id then failed
`PairingRequestInfo.Validate()` with `ErrMissingRequired` ("missing
required field") — that error matched all 5 PAIR tests.

New `resolveAnnounceZoneID(params, crypto)` helper falls through three
options to produce a 16-hex Zone ID:

1. Honor `params["zone_id"]` if it is exactly 16 hex chars.
2. Use `canonicalZoneIDFromCrypto(crypto)` when a Zone CA is loaded
   (post-commission case).
3. Synthesize from `controller_id` (or "mash-test-runner") via
   `SHA-256(seed)[0:8]` — the typical pre-commission pairing-request
   path. The device only matches by Discriminator per `docs/discovery.md`
   §2.4, so the requesting Zone ID just needs to be well-formed.

Same commit also enhances `handleReadMDNSTXT`: when a YAML step supplies
a `key` param, the handler now emits a canonical `value` output (previously
only `txt_<key>`) and `key_exists` reflects the per-key presence rather
than "any TXT records exist". Lookup is case-insensitive so YAML can
write either upper- or lower-case keys.

**Result:** 0/5 TC-PAIR tests hit "missing required field" (was 5/5).
2/5 now pass; the 3 still-failing tests fail on distinct issues that
were masked by F-2 (test corpus references non-spec TXT keys / has a
timing race against auto-PICS state). Those are not F-2 cases.

### F-7: mash-py subscribe handler matches Go's output keys

**Change:** `mash-py/mash_py/testharness/handlers/interaction.py`,
`mash-py/mash_py/testharness/runner.py` (+ tests). Commit `5bad714`.

Python `handle_subscribe` returned only `subscribe_success` and
`subscription_id`. The shared YAML corpus checks 6 more outputs that
cascaded as missing-key failures in quadrant 3 of the test matrix:
`subscription_id_returned`, `subscription_id_saved`,
`subscription_id_present`, `priming_received`, `priming_is_priming_flag`,
`priming_value`, `status`. Mirrored those exactly. The Subscription
object's `initial_values` dict is stashed at `state.custom["_priming_data"]`
mirroring mash-go's `_priming_data` state slot.

Also registered four checkers that mash-go exposes via
`engine.RegisterEnhancedCheckers` but mash-py was missing:
`save_subscription_id`, `save_delay_as`, `save_priming_value`,
`priming_value_different_from`. Without these the YAML cases that use
`expect: save_subscription_id: <name>` were misinterpreted by the default
equality checker.

**Result:** 763 mash-py unit tests pass. Quadrant-3 measurement on full
corpus deferred to next session (re-run with the same conditions as the
2026-04-29 morning baseline).

## Fixes shipped (morning session — F-1 + F-4)

### F-1: Operational SRV-resolve now uses the canonical zone-id

**Change:** `mash-go/internal/testharness/runner/{srv_resolving_dialer,dialer,commissioning,conn_mgr}.go`

The `Dialer.DialOperational` interface now takes a `zoneID` parameter. The
SRV-resolving wrapper uses it to filter `_mash._tcp` browse results to the
device-under-test's record (DEC-088 ZI TXT field). All call sites compute
the canonical zone-id via the new `canonicalZoneIDFromCrypto(crypto)`
helper, which derives `SHA-256(Zone-CA-cert-DER)[:8]` per DEC-088. The
runner's pre-existing `deriveZoneIDFromSecret` (PASE shared-secret hash)
remains for *internal* connection-tracking keys only — DEC-088 mandates
the cert-derived id for mDNS.

The previous bypass at `srv_resolving_dialer.go:97-108` ("operational dials
use the literal target") is removed; the half-built `resolveOperational` /
`browseFirstOperational` is now the actual code path. Operational cache is
zone-keyed so multi-zone harnesses don't hand the wrong port to a second
zone's dial.

**Tests:** `TestSRVResolvingDialer_OperationalWithZoneIDResolvesSRV`,
`TestSRVResolvingDialer_OperationalCachePerZone`,
`TestSRVResolvingDialer_TwoPortDeployment`,
`TestCanonicalZoneIDFromCrypto_MatchesDEC088`.

**Verification:** `mash-test -target 127.0.0.1:18446 -srv-resolve …` against
`mash-py-device --port 18445 --commissioning-port 18446` — TC-CONN-005
passes, log shows `srv-resolve: operational zone=<id> 127.0.0.1:18446 ->
192.168.1.x:18445`.

### F-4: Listener stays up for the service lifetime

**Change:** `mash-go/pkg/service/{device_service_lifecycle,device_service_discovery,device_service_commissioning}.go`

The Go device used to call `s.stopListener()` whenever the commissioning
window closed and `zoneCount == 0` (four call sites). That was an
"optimization" for an idle device but caused kernel-level `ECONNREFUSED`
on every reconnect attempt during the brief Exit→Enter cycle of
`TRIGGER_RESET_TEST_STATE`, and across-window timeouts in long runs. With
the test harness driving back-to-back commissioning attempts, the harness
saw `dial tcp: connect: connection refused` 256 times in the morning's
single run.

Fix: keep the listener up for the lifetime of `Start()` → `Stop()`. The
ALPN gate (`commissioningOpen.Store(false)`) and `getConfigForClient`
already enforce policy at the TLS layer; the kernel listener's presence
or absence adds no security and only creates an observable TCP-vs-TLS
asymmetry that breaks tests. `Stop()` retains its listener-cleanup path.

### Repro fix (caught while loading the corpus)

`mash-go/testdata/cases/discovery-behavior-tests.yaml:1162` was missing a
`---` separator between TC-BROWSE-002 and TC-BROWSE-004, causing
`gopkg.in/yaml.v3` to merge the two documents and fail with "mapping key
already defined". Fixed.

## How this was produced

```bash
# Quadrant 1: Go-on-Go
rm -rf /tmp/mash-go-state && \
  mash-go/bin/mash-device -reset -state-dir /tmp/mash-go-state \
    -port 18443 -discriminator 1234 -setup-code 20202021 &

mash-go/bin/mash-test \
  -target 127.0.0.1:18443 \
  -setup-code 20202021 -enable-key 00112233445566778899aabbccddeeff \
  -timeout 30s -exclude-tags env:multi-device \
  -tests mash-go/testdata/cases -json \
  > /tmp/run-go-go.json 2> /tmp/run-go-go.err

# Quadrant 2: Go-on-Py — needs DEC-082 two-port + -srv-resolve
mash-py/.venv/bin/mash-py-device --reset --state-dir /tmp/mash-py-state \
  --port 18443 --commissioning-port 18444 \
  --discriminator 1234 --setup-code 20202021 &
mash-go/bin/mash-test -target 127.0.0.1:18444 -srv-resolve \
  -discriminator 1234 -setup-code 20202021 \
  -enable-key 00112233445566778899aabbccddeeff \
  -timeout 30s -exclude-tags env:multi-device \
  -tests mash-go/testdata/cases -json \
  > /tmp/run-go-py.json 2> /tmp/run-go-py.err   # blocked, see F-1

# Quadrant 3 & 4: Python runner
PYTHONUNBUFFERED=1 mash-py/.venv/bin/mash-py-test \
  --target 127.0.0.1:<port> --setup-code 20202021 --discriminator 1234 \
  --tests mash-go/testdata/cases --exclude-tags env:multi-device \
  --timeout 15
```

Caveats:
- Single-host runs (everything on `127.0.0.1`); 11 cases tagged
  `env:multi-device` are excluded.
- Default Go `mash-device` is one-port (`-port 18443` for both commissioning
  and operational); Python `mash-py-device` is DEC-082 two-port (8443 op +
  8444 comm). The Go runner uses `-srv-resolve` against the Python device.
- Repro fix landed during this run: `discovery-behavior-tests.yaml` was missing
  a `---` separator between `TC-BROWSE-002` and `TC-BROWSE-004`, causing
  `gopkg.in/yaml.v3` to merge them and fail with "mapping key already defined."
  Fix: `mash-go/testdata/cases/discovery-behavior-tests.yaml:1162`.

---

## Quadrant 1 — `mash-test` against Go device (post F-1 + F-4)

```
Total reported       461
  ├─ skipped (PICS)  102   ← all "PICS requirements not met"
  ├─ passed          278
  └─ failed           81
```

Pass rate of attempted (excluding PICS-pruned): 278 / 359 = **77.4 %**.

Skipped categories — PICS-pruned because the EVSE PICS profile does not
declare those features. Same as before; F-1/F-4 don't change PICS filtering.

### Failure clustering (81 failures)

| Cluster | Count | Notes |
|---|---|---|
| `expectation failed` (real assertion) | **75** | Real test signal in feature implementations. Top sub-clusters: `invoke_success false` (30×), `value` mismatch (11×), `value_is_not_null` (6×), `response_contains` missing key (4×). |
| `announce pairing request: missing required field` | 5 | F-2: runner-side serializer bug for pairing-request announce. |
| `cert exchange: zone type already exists, code 10` | 1 | F-6: TC-E2E-001 cert state leak between tests. |
| connection refused / PASE EOF / suite-zone-disconnect | **0** | Eliminated by F-4 (listener stays up) and F-1 (canonical zone-id keys). |

### Failures by feature area (the genuine signal)

| Prefix | Count | Likely owner |
|---|---|---|
| ZONE | 10 | Zone management (Add/Remove/Limit) |
| GPL | 10 | Generic Power Load use case |
| EVC | 10 | EV-charging use case |
| STATE | 7 | State-machine tests |
| CTRL | 6 | EnergyControl |
| PAIR | 5 | F-2 (runner) |
| ITPCM, FAIL | 4 each | Inrush-tolerance, Failsafe |
| MEAS | 3 | Measurement feature |
| ZTYPE, STATUS, POEN, PODF, NULL, MPD, ELEC, CHRG | 2 each | Various features |
| TESTCTRL, INFLIGHT, FRAME, FI, ERR, E2E | 1 each | Edge-case tests |

These map to **device-side feature implementation gaps** and a small
runner-side cluster (PAIR-* via F-2). None are infrastructure-layer.

### Diagnostic logs (left on disk)

- Full JSON results: `/tmp/run-go-go-final.json` (461 docs)
- Device log: `/tmp/mash-go-device-final.log`
- Pre-fix baseline (for comparison): `/tmp/run-go-go.json` (461 docs, 12.5 % pass)

---

## Quadrant 2 — `mash-test` against Python device  (root-caused; workaround found)

### What the symptom looked like

The Python device commissions correctly, the Go runner installs the issued
cert, then the Go runner reconnects for operational and **TLS handshake
fails its post-handshake chain check**:

```
Error: auto-PICS discovery failed: commissioning for auto-PICS:
  operational reconnection failed: operational dial failed:
  certificate chain verification failed: x509: certificate signed by unknown authority
```

`mash-test -debug` shows:

```
[DEBUG] verifyPeerCert FAILED:
        leaf.CN=MASH-4000  leaf.FP=5eb18693  issuer.FP=1b163573
[DEBUG] verifyPeerCert:
        zoneCA.CN=MASH Zone CA - 084149f65eedd80d  zoneCA.FP=328f9d5e
```

### Initial wrong guess and why I corrected it

My first read was "Python serves the self-signed commissioning cert on its
operational listener" — `leaf.CN=MASH-<discriminator>` is the comm cert's CN.
That hypothesis is **wrong**. After looking at the cert files the Python
device persists:

```
$ openssl x509 -in /tmp/mash-py-state/operational-cert.pem -noout -subject -fingerprint -sha256
subject=CN=5823c793960e6af5
sha256 Fingerprint=EA:DB:AF:64:08:93:D2:CA:4C:72:1E:0D:D9:AF:21:35:…
$ openssl x509 -in /tmp/mash-py-state/commissioning-cert.pem -noout -subject -fingerprint -sha256
subject=CN=MASH-4000
sha256 Fingerprint=5E:B1:86:93:32:28:EC:57:F4:D5:0B:18:41:91:AE:E4:…
```

The leaf the Go runner saw (`leaf.FP=5eb18693`) matches the **commissioning**
cert. So the Python device served the right cert on the right port — but the
runner dialed the **wrong port** for operational reconnect. It hit the
commissioning listener (still up on 18446) and got the comm self-signed cert.

### Actual root cause: mash-go runner has SRV-resolve disabled for operational

`mash-go/internal/testharness/runner/srv_resolving_dialer.go:97-108`:

```go
func (d *srvResolvingDialer) DialOperational(ctx context.Context, target string,
                                              crypto CryptoState) (*tls.Conn, error) {
    // Operational SRV resolution requires a per-device disambiguator, but
    // the only stable one (zone-id) is derived from the PASE session and
    // thus unknown at dialer construction. With multiple operational
    // records visible (typical on shared LANs / CI), unfiltered browse
    // silently picks a stale record and dials the wrong device's port.
    // Until the dialer learns the post-PASE zone-id, operational dials
    // use the literal target -- which the harness has already pinned
    // to the device-under-test's operational port via the conformance
    // fixture.
    return d.base.DialOperational(ctx, target, crypto)
}
```

The comment is honest about the limitation: **operational SRV-resolve is
deliberately bypassed**. Notice that `resolveOperational` and
`browseFirstOperational` exist in the same file (lines 155, 217) but are
never called.

The assumption "the harness has already pinned `target` to the device's
operational port" holds for one-port devices (mash-go-device, where
commissioning and operational share `:18443`) but **breaks against any
DEC-082 two-port device**: when the operator passes `-target 127.0.0.1:<comm>`
(the natural choice — that's where commissioning happens), `transitionToOperational`
reuses that same `target` and dials the comm port for operational TLS.

### So to answer "how did this slip past Phase 14b/15?"

Phase 14b ("first non-Go MASH impl that can be commissioned end-to-end by a
Go controller") and Phase 15 ("operational server done") *did* validate the
mash-py device, but the integration tests they used always pointed
`mash-test` directly at the operational port (or used a single-port harness
config), not at `<commissioning-port>` with `-srv-resolve`. So the runner's
operational-dial path was always hitting the right port and the bypass in
`srv_resolving_dialer.go:97` never came into play. The interop test fixtures
sat exactly on the operator-error case: a bug only surfaces when someone
passes the commissioning port as `-target` and trusts SRV-resolve to do the
rest, the way an end-user reading the `-srv-resolve` flag's help text might.

### Workaround (verified — no code change)

Pass the **operational** port to `-target`, not the commissioning port:

```bash
mash-test -target 127.0.0.1:18445 -srv-resolve \
          -discriminator 4000 -setup-code 20202021 ...
```

`-srv-resolve` then SRV-resolves `_mash-comm._tcp` (find 18446) for
commissioning, and `transitionToOperational` reuses the literal target
(18445) for operational. Verified: TC-CONN-005 passes against mash-py-device
with this invocation. A full-corpus Go-on-Py run is in flight to populate
the Quadrant 2 row of the headline table.

### Real fix (in mash-go, not mash-py)

Two paths, in increasing scope:

1. **Thread the post-PASE zone-id into the dialer.** `transitionToOperational`
   already knows the zone-id (`deriveZoneIDFromSecret` at
   `commissioning.go:346`); pass it in so `DialOperational` can call
   `resolveOperational` with a zone-id filter (matches the operational TXT's
   `ZI=<zone-id>` field per DEC-088). The dead code at lines 153-178 is the
   half-built version of this.
2. **Document the `-target` semantics under DEC-082 two-port.** The runner's
   `-target` flag help should explicitly say "operational port; commissioning
   is reached via -srv-resolve" or similar.

The mash-py implementation is correct against the spec. No mash-py change is
needed for Quadrant 2.

---

## Quadrant 3 — `mash-py-test` against Go device

Run on the focused subset
`--files "commissioning-*,connection-*,protocol-*,read-*,subscription-*"`
(134 cases, 423 s wall time):

```
Total: 134  pass=2  fail=132  skip=0
```

The two passing tests are `TC-TLS-COMM-001` and `TC-TLS-COMM-004` (basic TLS
handshake observation; no PASE / no test-control trigger).

### Failure clustering (132 failures)

| Cluster | Count | What it means |
|---|---|---|
| harness gap: `unknown action: <X>` | **57** | Python harness has no implementation for the YAML action — protocol is irrelevant, this is missing handler code. |
| `device rejected PASE: timeout` | 24 | Python runner sends PASE-1 to Go device, no PASE-2 arrives before client deadline. Could be runner side (didn't read in time), Go device side, or test-state pollution between cases. |
| test-level `timed out after 10.0s` | 17 | Higher-level cancellation (typically waiting on a step that itself didn't time out cleanly). |
| genuine assertion failures | **32** | The runner reached the assert step — these are real protocol/runner bugs. Subscription flow dominates (8 × `subscribe_success`, 3 × `subscription_id_returned`, 2 × `subscription_id_saved`). |
| miscellaneous (`PASEBusyError` cooldown, etc.) | 2 | |

### Unknown actions (Python harness gaps — 57 failures, 18 distinct)

```
commission_with_admin                 (4x — TC-ADMIN-*)
enter_commissioning_mode              (4x — TC-COMM-WINDOW-*, TC-SEC-BACKOFF-*)
open_commissioning_connection         (7x)
ping                                  (6x — keep-alive tests)
invoke_with_disconnect                (4x)
send_pase_x                           (3x — SPAKE2 negative tests)
subscribe_multiple                    (2x)
set_commissioning_window_duration     (2x)
monitor_reconnect                     (2x)
flood_connections                     (2x — multi-host)
connect_as_zone                       (2x — multi-zone)
verify_keepalive, …                   (1x each)
```

Cheap wins (lifecycle methods already exposed in `mash-py/mash_py/cli/device.py`):
`open_commissioning_connection`, `enter_commissioning_mode`,
`set_commissioning_window_duration`, `ping`, `verify_keepalive`. Just need a
handler that calls the existing TestControl triggers.

Genuine code-needed: `send_pase_x` family (craft malformed PASE),
`subscribe_multiple` (the Phase 16 subscription path doesn't expose a multi-
attribute variant yet — see `mash-py/mash_py/interaction/`),
`monitor_reconnect`, `invoke_with_disconnect`.

Out of scope for single-host: `flood_connections`, `connect_as_zone` (multi-
zone is a future Python phase per `mash_py_phase22_followup_findings`).

### Genuine assertion failures (32, the real signal)

Subscription flow is the biggest cluster — strongly suggests the Python
runner's subscribe handler doesn't store the subscription id back into the
step `outputs` map, so subsequent expectations and steps that reference it
all cascade-fail:

| Assertion | Count |
|---|---|
| `subscribe_success: expected True, got False` | 8 |
| `subscription_id_returned: key not found in outputs` | 3 |
| `subscription_id_saved: key not found in outputs` | 2 |
| `read_success: expected True, got False` | 2 |
| `save_subscription_id`, `save_priming_value`, `priming_is_priming_flag` | 1 each |
| `error_code: expected 'DISCRIMINATOR_MISMATCH', got 'NO_DEVICES_FOUND'` | 1 |
| `tls_handshake_failed`, `window_expiry_warning`, `retries_performed_min`, `request_sent`, `message_ids_match`, `save_as`, `error_code (missing)` | 1 each |

### `device rejected PASE: timeout (error_code=2)`

Python runner sends PASE-1 to Go device, doesn't receive PASE-2 within its
client deadline, returns `error_code=2` (timeout). Phase 8 memory
(`mash_py_phase8_findings`) said live PASE interop with mash-go was green on
2026-04-26, so this is either a regression on the Python side or a Go-device
state issue (suite re-commissioning while a previous PASE is still open).
Worth a focused single-test reproduction before any code change — strongly
suggest looking at `mash-py/mash_py/commissioning/` PASE client deadline
handling first.

---

## Quadrant 4 — `mash-py-test` against Python device

Full-corpus aborted at 14 tests because the run was on track to take >2 hours
(unknown-action fails resolve in 0 ms, but `mDNS-comm-listener` waits time out
at 15 s × hundreds of tests). For Phase-22-comparable numbers, the most recent
recorded baseline is in `MEMORY.md` →
`mash_py_phase22_followup_findings.md`:

> Default Phase 22 corpus: **21/55 = 38 %** pass.
> "Cross-test PASE EOFs in suite mode": TC-PASE-001/002, TC-COMM-001/002/003
> pass isolated, time out at PASE-read in full-suite runs (~14 s test
> timeout). State accumulation suspected on the Python device.
> "No suite zone connection available for trigger" verified against the Go
> device too — same harness bug as quadrant 1.

That baseline should be re-measured on each phase change. The full-corpus
run we attempted today is not yet usable — see **F-3** below.

---

## Cross-cutting failure clusters (priority list)

These are ranked by *signal strength* (real bugs first) over *count*, since
the count is dominated by environmental issues we already know about.

### F-1 — `mash-test` operational reconnect doesn't SRV-resolve under DEC-082 two-port  ✅ FIXED

**Status:** Fixed in this session. See "Fixes shipped" above.

**Recap of the bug:** The Go runner had two different "zone-id" derivations
that were silently confused:
- `deriveZoneIDFromSecret(sessionKey)` — runner-internal bookkeeping key.
- `discovery.ZoneIDFromCertificate(zoneCACert)` — DEC-088 spec zone-id.

`srvResolvingDialer.DialOperational` had a comment saying "operational SRV
resolution requires a per-device disambiguator (zone-id) but it isn't known
at dialer construction" — true at construction, but the runner *does* know
the canonical zone-id at the *call site* once cert exchange completes
(`crypto.ZoneCA.Certificate` is populated). The bypass meant operational
reconnect always used the literal `-target`, which failed under DEC-082
two-port deployments where `-target` is the commissioning port.

**Fix:** `Dialer.DialOperational(ctx, target, zoneID, crypto)` — zone-id is
now an explicit parameter computed via `canonicalZoneIDFromCrypto(crypto)`
at all four call sites. SRV resolution is per-zone-keyed in the dialer
cache. Documented `-target`'s semantics under DEC-082 in `mash-test --help`.

### F-2 — TC-PAIR-001/002/004: pairing-request envelope missing required field  ✅ FIXED

**Status:** Fixed in the afternoon session. Commit `c013153`. See "Fixes
shipped (afternoon session — F-2 + F-7)" above.

**Recap of the bug:** YAML cases pass `controller_id` but the runner read
`zone_id` only, leaving the announcement's `ZI` TXT field empty —
`PairingRequestInfo.Validate()` rejects with "missing required field".

**Fix:** `resolveAnnounceZoneID` synthesizes a deterministic 16-hex zone
id when none is supplied; honors explicit `zone_id` when given.

**Result:** 0/5 PAIR tests hit the original error (was 5/5). 2/5 pass; 3
fail on unrelated, now-visible issues.

### F-3 — `mash-py-test` harness has 18 unimplemented actions and dominates failure noise  ⚠️ PARTIAL

**Status:** Partial fix shipped. Commit `8431b6a` adds base-PICS
auto-load: when `--pics` is not given and `<tests>/../pics/protocol-common.yaml`
exists, mash-py-test loads it and filters cases by PICS requirements
before running. On the mash-go corpus this skips 357 of 499 cases as
PICS-incompatible (cases that require device-specific feature-area PICS
items the base doesn't supply), leaving 142 cases the harness actually
attempts.

**Remaining (device-side discovery):** Go's runAutoPICS also commissions
the device, reads DeviceInfo, walks endpoints/features and reads
attributeList/commandList/featureMap, then merges those items on top of
the base. That half is not yet ported because it requires running PASE
before tests start, which restructures mash-py-test's "load cases, run,
exit" flow.

**Owner:** mash-py harness (`mash-py/mash_py/testharness/handlers/`,
`runner.py`, `cli/test.py`).
**Other suggested fixes (still open):**
1. Skip-fast on `unknown action` instead of failing; report as "harness
   not-implemented" in a separate bucket.
2. Lower the default mDNS browse timeout (`mash-py/mash_py/discovery/`)
   when a `--target host:port` is pinned.
3. Fill the cheap-win actions first (5 of them already have CLI/library
   support: `open_commissioning_connection`, `enter_commissioning_mode`,
   `set_commissioning_window_duration`, `ping`, `verify_keepalive`).
4. Add device-side discovery to close the skip-count gap with the Go
   runner (currently Python skips 357, Go skips 102 — the difference is
   the discovery half).

### F-4 — Test-stability cluster: 256 connection-refused failures in Go-on-Go  ✅ FIXED

**Status:** Fixed in this session. See "Fixes shipped" above.

**Recap of the bug:** The Go device's `ExitCommissioningMode` and the
commissioning-timeout callback called `s.stopListener()` whenever
`zoneCount == 0`. That tore down the kernel listener, so any subsequent
TCP dial got `ECONNREFUSED` even though the application would happily
have rejected the request at the TLS layer (`commissioningOpen.Store(false)`
already gates ALPN). Under the test harness's back-to-back commissioning
cadence, the device frequently spent brief windows in the "no zones,
window closed" state — and every dial during those windows died with
connection-refused.

**Fix:** The listener stays up for the lifetime of `Start()`/`Stop()`. The
ALPN gate plus `getConfigForClient` already enforce policy at TLS, so the
listener-stop optimization had no security upside and a large test-stability
downside.

**Result:** 256 environmental failures → 0 environmental failures. Pass rate
12.5 % → 77.4 %.

### F-5 — Genuine discovery/pairing/TLS assertions in Go-on-Go

| Test | What to investigate |
|---|---|
| `TC-DISC-001` | mDNS TXT validation in mash-go advertiser; spec is `docs/discovery.md`. |
| `TC-BROWSE-004` | `D` TXT field surfacing under discriminator-filter browse — likely runner-side `outputs` mapping. |
| `TC-CONN-REAP-003` | Connection-reaper edge case; could be env-related. |
| `TC-TLS-CTRL-007` | Controller accepting self-signed cert during commissioning — negative-test polarity issue. |
| `TC-MULTI-003` | TestControl trigger status 8; cross-reference triggers.yaml registry (DEC-087). |

### F-6 — Inter-test cert-state leak (zone type already exists, code 10)  ✅ FIXED

**Status:** Fixed in the afternoon session. Commit `2d46a23`. See "Fixes
shipped (afternoon session)" above.

**Recap of the bug:** A LOCAL-zone commission from a prior test (e.g.
TC-TRANS-004) was leaving the device with the zone in its `connectedZones`
map after the runner's local teardown untracked it. The next test that
commissioned a LOCAL zone hit "zone type already exists code 10". The
runner had a "residual zone sweep" designed to ask the device for its
authoritative zone list and remove non-suite zones, but two bugs disabled
it: a `-strict-lifecycle` gate kept it dormant in normal runs, and even
when run, `requestDeviceState` used pre-Phase-11 string-keyed parameters
that the device rejected with `StatusInvalidParameter`.

**Fix:** drop the gate; switch to integer-keyed parameters. Two regression
tests added: `TestRequestDeviceState_SendsIntegerKeyedParameters` and
`TestCoordTeardown_DefaultMode_RunsResidualZoneSweep`.

### F-7 — `mash-py-test` subscribe handler doesn't populate the step `outputs` map  ✅ FIXED

**Status:** Fixed in the afternoon session. Commit `5bad714`. See "Fixes
shipped (afternoon session — F-2 + F-7)" above.

**Recap of the bug:** Python `handle_subscribe` returned only
`subscribe_success` + `subscription_id`; YAML cases also check
`subscription_id_returned`, `subscription_id_saved`, `priming_received`,
`priming_is_priming_flag`, `priming_value`, `status`. Plus four
"save"-style checkers (`save_subscription_id`, `save_delay_as`,
`save_priming_value`, `priming_value_different_from`) were not
registered, so YAML using `expect: save_subscription_id: <name>` was
mis-evaluated by the default equality checker.

**Fix:** mirror Go's output keys exactly; register the missing checkers;
stash priming dict at `state.custom["_priming_data"]` mirroring mash-go.

**Result:** 763 mash-py unit tests pass. Full quadrant-3 re-measurement
deferred — to be run identically to the 2026-04-29 morning baseline.

---

## Next steps (suggested order)

1. **F-1 first** — root-caused, single-day fix, unblocks an entire quadrant.
   Once Python serves the issued operational cert, quadrant 2 immediately
   becomes measurable.
2. **F-4** — quadrant 1's "12.5 % pass" is misleading until the connection-
   refused storm is debugged; this is the biggest *signal-quality* problem
   on the Go side.
3. **F-3 + F-7** — together these unlock quadrants 3 and 4. F-3 is the
   harness-gap discount; F-7 is a tight, named handler bug. Doing them in
   order means quadrant 3's pass rate jumps mostly from F-7 and the rest
   from F-3.
4. **F-2 / F-5** — small bugs, fix in passing while debugging the larger items.
5. **F-6** — narrower, but a precondition for honest E2E numbers.

When the seven clusters are addressed, this file should be re-generated and
the headline table fully populated; expected target is ≥ 80 % pass on each
diagonal and ≥ 60 % on each cross-stack quadrant.

---

## Reproducer artifacts (left on disk for triage)

- Go-on-Go JSON results: `/tmp/run-go-go.json` (461 docs, one per test +
  summary).
- Go-on-Go failures TSV: `/tmp/go-on-go-failures.tsv` (id ⇥ error string).
- Go-on-Py debug log (cert fingerprint mismatch): `/tmp/run-go-py-debug.log`.
- Py-on-Go log: `/tmp/run-py-go.log` (134 cases; 423 s).
- Python device runtime log: `/tmp/mash-py-device.log`.
- Go device runtime log: `/tmp/mash-go-device.log`.

To regenerate this file after fixes, re-run the four commands at the top of
the "How this was produced" section.
