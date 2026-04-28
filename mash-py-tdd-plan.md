# MASH Python Reference Implementation — TDD Plan

**Status:** Draft for review
**Date:** 2026-04-25
**Companion to:** `mash-go/` (Go reference implementation)

This plan covers a parallel Python implementation of the MASH protocol. Every phase is grounded in concrete file/symbol references from `mash-go`, gathered through targeted source exploration; no claims are speculative. The plan is structured so each phase ends with a *live interop check against `mash-go`*, not just unit-test green.

---

## Table of Contents

1. [Goals & Non-Goals](#1-goals--non-goals)
2. [Architectural Decisions to Make Up-Front](#2-architectural-decisions-to-make-up-front)
3. [Python Stack Architecture](#3-python-stack-architecture)
4. [Repository Layout](#4-repository-layout)
5. [TDD Methodology](#5-tdd-methodology)
6. [Cross-Language Test Vector Strategy](#6-cross-language-test-vector-strategy)
7. [Phased Build Plan](#7-phased-build-plan) — 22 phases, each with acceptance + interop verification
8. [Conformance Gate](#8-conformance-gate)
9. [CI / Tooling](#9-ci--tooling)
10. [Risks & Mitigations](#10-risks--mitigations)
11. [Open Questions for the User](#11-open-questions-for-the-user)

---

## 1. Goals & Non-Goals

### Goals

- A Python reference implementation that **interoperates byte-for-byte** with the Go reference: a Python device commissioned by a Go controller, a Go device commissioned by a Python controller, all combinations.
- Passes the existing Go conformance harness (`mash-test`) when run against a Python device.
- Passes a Python conformance harness (port subset of the Go runner) when run against a Go device — proves the harness itself is portable.
- Treats `docs/features/*.yaml` as the single source of truth: Python codegen reads the same YAML as Go codegen.
- Is suitable both for desktop/server use (CPython 3.11+) and *eventually* for ESP32-class targets (CircuitPython); the latter is opt-in, not a hard constraint on phase 1.

### Non-Goals (Phase 1)

- Replacing `mash-go` as the canonical reference. The Go implementation remains canonical; Python is a peer that must agree.
- A from-scratch SPAKE2+ implementation. We lift the algorithm core from Adafruit CircuitMatter (MIT, RFC 9383 conformant) and wrap it in MASH-specific framing.
- Backwards compatibility with non-existent earlier protocol versions.
- A graphical commissioning UI. CLI only for now.

---

## 2. Architectural Decisions to Make Up-Front

These need a one-sentence answer from the user **before phase 1 starts** because they shape the whole stack.

### Decision A: Two-Port Deployment per DEC-082 — **LOCKED**

**Background.** The Go device listens on a single TCP port (8443) and uses `tls.Config.GetConfigForClient` to inspect the ClientHello's `SupportedProtos` list and return a *different* `tls.Config` based on whether `mash-comm/1` or `mash/1` is offered (DEC-067). Python's stdlib `ssl` module has no equivalent callback — `SSLContext.sni_callback` only fires on SNI, and `set_alpn_protocols` only chooses among offered protocols.

**Resolution.** This portability gap was elevated to a spec issue. **DEC-082 (2026-04-25)** formalizes two-port deployment as a conformant alternative to DEC-067's single-port. Both patterns produce identical wire behavior; the difference is observable only as different mDNS SRV ports.

mash-py uses the two-port pattern:
- **Operational listener:** TCP port 8443. SSLContext with operational ALPN `mash/1` only, `CERT_REQUIRED` client auth, Zone CA-signed cert (selected per zone via `sni_callback` — Phase 20).
- **Commissioning listener:** TCP port 8444 (default; configurable). SSLContext with commissioning ALPN `mash-comm/1` only, `CERT_NONE` client auth, self-signed stable cert.

mDNS SRV records advertise the actual port for each service; controllers (Go or Python) dial the SRV-advertised port without caring about the deployment shape.

**Why this is now the right choice:**
- **Spec-conformant per DEC-082.** Not a deviation; the spec explicitly accepts both patterns.
- **Trivial in stdlib `ssl`.** Two `asyncio.Server` instances, two `SSLContext`s, no BIO trickery, no ClientHello parsing.
- **Same security guarantees.** Separate listeners with independent TLS configs prevent operational-cert leakage by the same mechanism single-port does — different `tls.Config` objects, just bound to different sockets.
- **No spec-portability burden.** Future C/Zig/Rust ports inherit DEC-082 directly.

**Implementation cost.** ~50–80 LOC in `mash_py/service/device.py` to manage two listeners with shared connection-cap counter (DEC-064). The dispatcher and ClientHello parser previously planned for Phase 4b are no longer needed.

**Rejected alternatives:**

| Option | Why rejected |
|---|---|
| Single-port ClientHello dispatcher | ~400 LOC of TLS-protocol code; security-sensitive parser bugs possible; not needed once DEC-082 lands. |
| PyOpenSSL with `set_tlsext_servername_callback` | Heavyweight C-extension dep; CircuitPython unsupported; pulls us off the CPython-stdlib happy path. |
| Single SSLContext + post-handshake validation | Cert mismatch — operational needs Zone CA-signed cert, commissioning needs self-signed; one SSLContext can present only one cert chain. |

**Cross-references:**
- Spec: DEC-067 (single-port, recommended for capable implementations), DEC-082 (two-port, conformant alternative — the canonical pattern for languages without dynamic TLS config callbacks).
- Tracker: `mash-py-spec-issues.md` Issue #1 (resolved by DEC-082).

### Decision B: CircuitPython Compatibility — Phase 1 Constraint or Phase 2 Stretch?

**Implication of "Phase 1 constraint":** No `cryptography` package (uses CFFI, doesn't run on CircuitPython). Must use `ecdsa` (pure-Python) and a hand-rolled AES-CCM/AES-GCM, like CircuitMatter does. Slower; more code to write; smaller library set.

**Implication of "Phase 2 stretch":** Phase 1 uses the full `cryptography` library (TLS 1.3, X.509, EC, AEAD all in one). Phase 2 swaps in CircuitPython-compatible primitives behind a thin `mash_py.crypto` interface.

**Recommended default:** Phase 2 stretch. Phase 1 should use `cryptography`; we get a working device faster and the interface boundary makes the swap mechanical later.

**Decision needed from user:** confirm phase-2 stretch.

### Decision C: Codegen Strategy

**Background.** `mash-go/cmd/mash-featgen/templates.go` contains all Go templates inline. funcMap helpers like `goTypeName`, `accessConst` are Go-specific. Adding a `--lang python` flag would conflate two languages in one tool.

**Recommended default:** Separate `mash-py/codegen/` Python tool using Jinja2 templates, reading the same YAML in `docs/features/`. The two generators must agree on schema; a small CI job will diff their outputs' "spec coverage" to catch drift.

**Decision needed from user:** confirm the separate-generator approach.

### Decision D: Async Model

**Recommended default:** asyncio throughout. The Go impl uses one goroutine per connection (read loop) plus background goroutines for accept loops, mDNS, and notification dispatch. Python `asyncio` Tasks map cleanly:
- One `asyncio.Task` per connection's read loop (mirrors Go's read-loop goroutine — `pkg/transport/connection.go:435–482`).
- One Task per accept loop.
- One Task per zone's `NotificationDispatcher` (`pkg/service/dispatch/notification_dispatcher.go:82–89`).

**Decision needed from user:** any objection to asyncio (vs. threads, trio, anyio)?

### Decision E: SPAKE2+ Sourcing

**Recommended default:** lift the SPAKE2+ algorithm core from CircuitMatter's `circuitmatter/pase.py` (MIT). Specifically the functions `Crypto_pA`, `Crypto_pB`, `Crypto_Transcript`, `Crypto_P2`, `compute_session_keys`, `compute_verification`. Re-use under MIT attribution; do not depend on the package directly because its message framing is Matter-specific. Wrap the core in a MASH-shaped module: `mash_py.commissioning.spake2plus`.

**Verification target:** the M and N points in `mash-go/pkg/commissioning/spake2plus.go:38–49` are the same RFC 9383 hex strings CircuitMatter uses. The HKDF info string differs (Go uses `"SPAKE2+-P256-SHA256"`, Matter uses Matter-specific). So Python must use the *Go* info string when targeting MASH interop.

**Decision needed from user:** confirm CircuitMatter-as-reference is acceptable (MIT license terms are simple — preserve copyright header).

---

## 3. Python Stack Architecture

### Module dependency graph

```
                ┌──────────────────────────────┐
                │   mash_py.cli                │  (mash-device, mash-controller, mash-test-py)
                └───────────────┬──────────────┘
                                │
          ┌─────────────────────┴──────────────────────┐
          ▼                                            ▼
┌────────────────────┐                       ┌────────────────────┐
│ mash_py.service    │                       │ mash_py.testharness│
│  Device + Ctrl     │                       │  runner, loader    │
│  orchestration     │                       └────────────────────┘
└──────────┬─────────┘                                  │
           │                                            │
           ▼                                            ▼
┌────────────────────┐   ┌────────────────────┐   ┌────────────────────┐
│ mash_py.zone       │   │ mash_py.usecase    │   │ mash_py.discovery  │
│ Zone state, multi- │   │ Use case defs      │   │ mDNS adv + browse  │
│ zone resolution    │   │ (generated)        │   │ (zeroconf)         │
└──────────┬─────────┘   └─────────┬──────────┘   └──────────┬─────────┘
           ▼                       ▼                          │
┌────────────────────┐   ┌────────────────────┐              │
│ mash_py.features   │◄──┤ mash_py.model      │              │
│ Feature impls      │   │ Endpoint, Feature, │              │
│ (partial gen)      │   │ Attribute (gen)    │              │
└──────────┬─────────┘   └─────────┬──────────┘              │
           │                       │                          │
           └───────────────────────┴──────────────────────────┘
                                   │
                                   ▼
                  ┌────────────────────────────────┐
                  │ mash_py.interaction            │
                  │ Read/Write/Subscribe/Invoke    │
                  │ dispatch, sub manager          │
                  └───────────────┬────────────────┘
                                  │
              ┌───────────────────┴───────────────────┐
              ▼                                        ▼
┌────────────────────────┐                ┌────────────────────────┐
│ mash_py.commissioning  │                │ mash_py.transport      │
│ PASE (SPAKE2+),        │                │ Framer, Connection,    │
│ CSR, cert install,     │                │ TLS contexts, ALPN     │
│ renewal                │                └────────────┬───────────┘
└────────────┬───────────┘                              │
             │                                          │
             ▼                                          ▼
┌────────────────────────┐                ┌────────────────────────┐
│ mash_py.crypto         │                │ mash_py.wire           │
│ Thin facade over       │                │ CBOR codec,            │
│ `cryptography` (P1)    │                │ Request/Response/      │
│ swappable for          │                │ Notification/Control,  │
│ ecdsa+pure-Python (P2) │                │ PeekMessageType        │
└────────────────────────┘                └────────────────────────┘
```

### Key design rules

- **No package depends upward.** `wire` knows nothing about `transport`. `transport` knows nothing about `service`. Tests can exercise each layer in isolation.
- **All wire-touching modules are pure functions** where possible. CBOR codecs in `mash_py.wire` take `bytes` in, return `bytes` out, no I/O.
- **All I/O is `async`**. There is no sync API at the public surface. CLI tools use `asyncio.run`.
- **`mash_py.crypto` is a facade**, not a re-export. Phase 1 implements it on top of `cryptography`; phase 2 swaps in pure-Python primitives without changing call sites.
- **Generated code lives next to handwritten code** in the same module, mirroring Go's `*_gen.go` pattern. Generated files are named `_gen.py` and have a top banner forbidding hand-edits.

### External dependencies (CPython 3.11+)

| Package | Used for | Phase |
|---|---|---|
| `cbor2` | CBOR encode/decode (canonical mode, integer keys) | 1 |
| `cryptography` | TLS, X.509, EC, AES-GCM, HKDF | 1 |
| `zeroconf` | mDNS advertise + browse | 6 |
| `pyyaml` | YAML loading for codegen | 12 |
| `jinja2` | Templates for codegen | 12 |
| `ecdsa` | (Phase 2 only) pure-Python P-256 for CircuitPython compat | 2 stretch |
| `pytest`, `pytest-asyncio` | test runner | all |
| `hypothesis` | property tests for codecs | 1 |
| `ruff`, `mypy` | lint + type-check | 1 |

No transitive heavyweights. No PyOpenSSL. No Twisted.

---

## 4. Repository Layout

```
mash-py/
├── pyproject.toml
├── README.md
├── Makefile                    # generate, test, conformance, lint
├── mash_py/
│   ├── __init__.py
│   ├── wire/                   # CBOR codec + framing primitives
│   │   ├── __init__.py
│   │   ├── codec.py            # encode/decode Request/Response/Notification/Control
│   │   ├── framing.py          # 4-byte BE length prefix
│   │   ├── peek.py             # PeekMessageType-equivalent classifier
│   │   ├── operations.py       # Operation/Status/MessageType enums
│   │   └── payloads.py         # ReadPayload, SubscribePayload, InvokePayload, ErrorPayload
│   ├── transport/              # TLS + connection lifecycle
│   │   ├── __init__.py
│   │   ├── tls.py              # SSLContext factories (operational, commissioning)
│   │   ├── connection.py       # async Connection with read loop
│   │   └── alpn.py             # ALPN constants
│   ├── crypto/                 # facade over cryptography
│   │   ├── __init__.py
│   │   ├── ec.py               # P-256 ops (point add, scalar mult, hash-to-point)
│   │   ├── kdf.py              # HKDF-SHA256
│   │   ├── mac.py              # HMAC-SHA256
│   │   └── certs.py            # X.509 generation + verification + CSR
│   ├── commissioning/
│   │   ├── __init__.py
│   │   ├── spake2plus.py       # algorithm core (lifted from CircuitMatter, adapted)
│   │   ├── messages.py         # PASERequest/Response/Confirm/Complete + CSR/CertInstall
│   │   ├── pase_client.py      # controller-side PASE state machine
│   │   ├── pase_server.py      # device-side PASE state machine
│   │   ├── renewal_messages.py # MsgTypes 30-33
│   │   └── renewal.py          # in-session renewal state machines
│   ├── interaction/
│   │   ├── __init__.py
│   │   ├── client.py           # Read/Write/Subscribe/Invoke client (matches MessageID)
│   │   ├── server.py           # request dispatcher
│   │   └── subscription.py     # subscription manager + dispatcher
│   ├── model/
│   │   ├── __init__.py
│   │   ├── feature.py          # Feature, Attribute, Command base types
│   │   ├── endpoint.py         # Endpoint
│   │   ├── device.py           # Device (collection of endpoints)
│   │   ├── _gen.py             # generated: feature type IDs, endpoint type IDs
│   │   └── shared_gen.py       # generated: shared enums (Phase, Direction, etc.)
│   ├── features/               # feature implementations (mostly generated)
│   │   ├── __init__.py
│   │   ├── device_info_gen.py
│   │   ├── measurement_gen.py
│   │   ├── electrical_gen.py
│   │   ├── energy_control_gen.py
│   │   ├── status_gen.py
│   │   ├── charging_session_gen.py
│   │   ├── tariff_gen.py
│   │   ├── signals_gen.py
│   │   ├── plan_gen.py
│   │   └── test_control.py     # handwritten — implements trigger handlers
│   ├── usecase/
│   │   ├── __init__.py
│   │   └── definitions_gen.py  # generated from docs/usecases/1.0/
│   ├── zone/
│   │   ├── __init__.py
│   │   ├── zone.py             # Zone type, MultiZoneValue, resolution
│   │   └── pool.py             # zone state container
│   ├── discovery/
│   │   ├── __init__.py
│   │   ├── types.py            # service constants, TXT record schemas
│   │   ├── advertiser.py       # async wrapper over zeroconf
│   │   └── browser.py
│   ├── service/
│   │   ├── __init__.py
│   │   ├── device.py           # DeviceService — lifecycle, accept loop
│   │   ├── controller.py       # ControllerService — discovery, commissioning, sessions
│   │   ├── zone_session.py
│   │   ├── device_session.py
│   │   ├── failsafe.py
│   │   └── cert_store.py       # in-memory + file backends
│   ├── testharness/
│   │   ├── __init__.py
│   │   ├── runner.py           # subset of mash-go's runner
│   │   ├── loader.py           # YAML test case loader
│   │   ├── pics.py
│   │   └── handlers/           # handler implementations per action verb
│   └── cli/
│       ├── __init__.py
│       ├── device.py           # `mash-py-device` entry point
│       ├── controller.py       # `mash-py-controller`
│       └── test.py             # `mash-py-test`
├── codegen/                    # NOT under mash_py — runs at build time
│   ├── __init__.py
│   ├── __main__.py             # `python -m codegen`
│   ├── specparse.py
│   ├── helpers.py              # py_type_name, py_access_const, etc.
│   └── templates/
│       ├── feature.py.j2
│       ├── enums.py.j2
│       ├── shared.py.j2
│       └── usecase.py.j2
├── tests/
│   ├── unit/                   # mirrors mash_py/ tree
│   ├── vectors/                # cross-language byte vectors (committed JSON)
│   ├── interop/                # tests that spawn or talk to mash-go binaries
│   └── conformance/            # runs Go mash-test against Python device
└── tools/
    ├── gen_vectors.go          # one-off Go program: dumps test vectors as JSON for Python
    └── run_interop.sh          # orchestrate Python<->Go interop runs
```

This layout sits **alongside `mash-go/`** in the same repo. No code is shared at language boundary; YAML in `docs/features/` is the only shared input.

---

## 5. TDD Methodology

Every phase follows the same five-step ritual:

1. **Write the test first.** Three layers, in this order:
   - **Unit:** pure Python. Encode/decode round-trip, state-machine transitions, error cases.
   - **Vector:** load a JSON file from `tests/vectors/` produced by a Go test, assert the Python code produces identical bytes (or accepts identical bytes).
   - **Interop:** spawn a Go binary (or run against an already-running one) and exchange real frames.
2. **Implement minimal code** to pass the unit + vector tests.
3. **Run the interop test.** This is the "go-it-or-not" gate. If it fails, the failure mode is one of:
   - **Wire mismatch.** Diff the bytes (Python sent vs Go expected). 90% of the time it's a CBOR integer encoding edge case or a missing key. Fix by widening or correcting the codec.
   - **State mismatch.** Read both sides' logs. Usually one side advanced its state machine while the other was still waiting. Fix the state machine.
   - **Spec ambiguity.** The Go behavior isn't what the spec says. File a `decision-log.md` entry; clarify the spec; update both.
4. **Commit.** Each commit message references the phase number and the Go file:line it ports from.
5. **Update the plan.** If the phase revealed something the plan didn't anticipate, edit this document — don't quietly absorb the surprise.

### Test naming convention

```
tests/unit/wire/test_codec.py::TestRequestRoundTrip::test_read_request
tests/vectors/wire/test_request_vectors.py::test_read_request_matches_go
tests/interop/wire/test_against_go.py::test_python_request_decoded_by_go
```

The path tells you the test layer at a glance, which directly maps to which kind of failure to expect.

### Refinement loop on interop failure

When a phase fails interop:

1. **Capture both wire logs.** Go side: pass `-protocol-log` to `mash-test` or use the device's `--protocol-log` flag. Python side: enable the always-on hex dump in `mash_py.wire.codec` (toggleable env var `MASHPY_WIRELOG=1`).
2. **Diff frame by frame.** Same length? Same first byte? CBOR has self-describing prefixes — an `0xA4` is a 4-key map; reading from there usually localises the difference fast.
3. **Write a regression test in `tests/vectors/`** with the bytes from the failing exchange. Now the bug is committed before the fix.
4. **Fix the Python side.** Almost always the Python side; the Go side has been load-tested longer.
5. **Re-run interop.** If it now passes, mark the phase complete. If not, GOTO 1 — there's a second bug.

---

## 6. Cross-Language Test Vector Strategy

This is the load-bearing piece of the plan. Without it, "Python passes its unit tests" doesn't mean "Python interoperates with Go."

### How vectors are produced

A small Go program at `mash-py/tools/gen_vectors.go` is built once per protocol-spec change. It:

1. Imports `mash-go/pkg/wire`, `pkg/commissioning`, etc.
2. Encodes a curated set of canonical messages (one per type, plus edge cases — empty payloads, max-size, all-zero IDs, boundary integer types).
3. Writes each as a JSON file: `{"name": "...", "description": "...", "go_struct_repr": "...", "cbor_hex": "A4..."}`.

Output goes to `mash-py/tests/vectors/`, committed to the repo.

### What vectors cover

| Vector category | Source Go test | Vectors produced |
|---|---|---|
| Wire codec | `pkg/wire/codec_test.go:7–662` | ~30 vectors: each message type, each operation, error responses, NaN/Infinity rejection, duplicate-key rejection |
| Framing | `pkg/transport/framing_test.go:42–327` | ~10 vectors: small/medium/max-size, boundary, truncation |
| Commissioning | `pkg/commissioning/spake2plus_test.go` + a new wire-vector test | ~8 vectors: full PASE transcript with seeded RNG, error codes, CSR/CertInstall messages |
| Cert renewal | `pkg/commissioning/renewal_messages.go` (no test vectors yet — we add them) | ~6 vectors: each MsgType 30-33, with optional fields present/absent, status codes |

For SPAKE2+, vectors include not just messages but **intermediate values** — `w0`, `w1`, `pA`, `pB`, `Z`, `V`, transcript hash, derived keys. This is essential because the protocol's randomness (random `x`, `y`) means we can't compare ephemeral pA/pB across implementations without seeded RNG.

**Implementation note:** the Go `gen_vectors` program will use a seedable `io.Reader` for randomness, expose it via a flag, so each run produces deterministic output. The Python code accepts the same seed for vector tests.

### CI enforcement

The `gen_vectors` Go program runs in CI on the Go side. If protocol changes alter encoding, the JSON vectors change. The Python side's `tests/vectors/` reads them and asserts. **Diverging encodings cannot land — both sides must update together.**

---

## 7. Phased Build Plan

22 phases. Each is small enough to complete in 1–3 days for a single developer. Each phase is independently mergeable. **No phase is "done" until its interop check passes.**

The conventions in each phase:

- **Status:** `Done (YYYY-MM-DD)` once the phase's acceptance criterion is met, or `Pending` otherwise. A phase whose primary code has landed but whose interop check is blocked on a later phase is marked `Done (YYYY-MM-DD; interop deferred to Phase N)` so the gap stays visible.
- **Goal:** what this phase delivers.
- **Depends on:** prior phases (numbers).
- **Files created/modified:** with rough size estimates.
- **Test layers:** unit / vector / interop, with concrete cases.
- **Acceptance:** the criterion that ends the phase.
- **Interop verification:** the live Go-side check.
- **Refinement contingency:** if interop fails, what's the most likely cause and how to bisect.

### Progress snapshot

| Phase | Title | Status |
|---|---|---|
| 0 | Project Scaffolding | Done (2026-04-25) |
| 1 | CBOR Wire Codec | Done (2026-04-25) |
| 2 | Length-Prefixed Framing | Done (2026-04-26) |
| 3 | Crypto Facade & Certificate Helpers | Done (2026-04-26) |
| 4 | TLS Transport (DEC-082 two-port) | Done (2026-04-26; live interop deferred to Phase 6) |
| 5 | `mash_py.wire.peek` Live Verification | Done (2026-04-26; live body deferred to Phase 8/11) |
| 6 | mDNS Discovery (Browser Only) | Done (2026-04-26) |
| 7A | SPAKE2+ Spec Rewrite + DEC-084 | Done (2026-04-26) |
| 7B | mash-test Conformance Vectors + Harness | Done (2026-04-26) |
| 7C | mash-go SPAKE2+ Update to DEC-084 | Done (2026-04-26) |
| 7D | mash-py SPAKE2+ Implementation | Done (2026-04-26) |
| 8 | PASE Message Exchange (Client Side) | Done (2026-04-26) |
| 9 | CSR & Certificate Install | Done (2026-04-27) |
| 10 | Operational Connection (Post-Commissioning) | Done (2026-04-27) |
| 11 | Interaction Model Client | Done (2026-04-27) |
| 12 | Code Generation (Python from YAML) | Done (2026-04-27) |
| 13 | Device Model | Done (2026-04-27) |
| 14 | PASE Server (Device Side) | Done (2026-04-27, PASE-only — see Phase 14b for the rest of the device commissioning flow) |
| 14b | Cert Exchange Server (Device Side) | Done (2026-04-27) |
| 15 | Operational Server (Device Side) | Done (2026-04-27) |
| 16 | Subscriptions & Notifications | Pending |
| 17 | TestControl Feature & Trigger Handlers | Pending |
| 18 | mDNS Advertiser | Pending |
| 19 | Certificate Renewal (MsgTypes 30–33) | Pending |
| 20 | Failsafe & Multi-Zone Resolution | Pending |
| 21 | Test Harness (Python Runner Subset) | Pending |
| 22 | Conformance Gate (mash-test against Python device) | Pending |

---

### Phase 0 — Project Scaffolding

**Status.** Done (2026-04-25).

**Goal.** A Python package that builds, tests, and lints; CI green on a no-op test.

**Depends on.** Nothing.

**Files created.**

- `mash-py/pyproject.toml` — packaging, deps, ruff config, mypy config
- `mash-py/Makefile` — `test`, `lint`, `generate`, `conformance` targets
- `mash-py/mash_py/__init__.py` — `__version__`
- `mash-py/tests/test_smoke.py` — imports the package
- `mash-py/.github/workflows/python.yml` (or extend existing CI)

**Test layers.**

- Unit: `tests/test_smoke.py::test_import` — `import mash_py` succeeds.

**Acceptance.** `make test lint` clean on a fresh checkout.

**Interop verification.** None — nothing to interop with yet.

**Refinement contingency.** N/A.

---

### Phase 1 — CBOR Wire Codec (Request/Response/Notification/Control)

**Status.** Done (2026-04-25).

**Goal.** Encode and decode all four MASH message classes with byte-identical output to Go's `pkg/wire`.

**Depends on.** Phase 0.

**Background from exploration.**
- Go uses `fxamacker/cbor/v2` with `SortCanonical`, `IndefLengthForbidden`, `NaNDecodeForbidden`, `InfDecodeForbidden` (`mash-go/pkg/wire/codec.go:11–48`).
- Field numbers are integer keys; `keyasint` tags enforce numeric encoding.
- See `tests/vectors/` (Phase 6 above) for the 30 canonical vectors.

**Files created/modified.**

- `mash_py/wire/operations.py` — `Operation`, `Status`, `MessageType` enums (~80 lines)
- `mash_py/wire/payloads.py` — `ReadPayload`, `SubscribePayload`, `SubscribeResponsePayload`, `UnsubscribePayload`, `InvokePayload`, `ErrorPayload`, `ControlMessage`, `Notification` dataclasses (~150 lines)
- `mash_py/wire/codec.py` — `encode_request`, `decode_request`, `encode_response`, `decode_response`, `encode_notification`, `decode_notification`, `encode_control`, `decode_control` (~200 lines)
- `mash_py/wire/peek.py` — `peek_message_type(bytes) -> MessageType` (~80 lines, ports `pkg/wire/codec.go:173–272` algorithm)
- `tools/gen_vectors.go` — Go program emitting `tests/vectors/wire/*.json`
- `tests/unit/wire/test_codec.py`, `test_peek.py` — round-trip tests, NaN/Infinity rejection, duplicate-key rejection
- `tests/vectors/wire/test_against_vectors.py` — load JSON vectors, assert encoding matches and decoding produces equivalent objects

**Test layers.**

| Layer | What |
|---|---|
| Unit | Round-trip every message variant. Reject NaN, Infinity, duplicate keys, string keys. Property test: random integer-keyed maps round-trip identically. |
| Vector | All 30 vectors from `gen_vectors`. Both directions: encode-and-compare-bytes, decode-and-compare-fields. |
| Interop | Deferred to Phase 4 — needs transport. |

**Acceptance.** All 30 vector tests pass. `peek_message_type` correctly classifies every vector without false-routing a Notification as a Response (the most subtle case — see Go classification at codec.go:208).

**Interop verification.** Deferred until phase 4 (TLS connection). At that point, send a Python-encoded `ReadRequest` to the Go device and confirm it parses; receive a Go-encoded `ReadResponse` and confirm we parse it.

**Refinement contingency.** If a vector fails:
- Look first at integer encoding. CBOR encodes integers with the shortest possible representation — `cbor2` and `fxamacker/cbor` agree on canonical mode, but check anyway.
- Look at map key sort order. `cbor2` sorts integer keys; Go's `SortCanonical` does too. Verify by encoding `{10: 1, 1: 2}` and reading the bytes — key 1 must come first.
- Look at byte-string vs text-string. CBOR distinguishes them; `bytes` in Python is a byte string, `str` is text. A field that should be `bytes` but ends up as `str` will encode differently.

---

### Phase 2 — Length-Prefixed Framing

**Status.** Done (2026-04-26).

**Goal.** Read and write framed messages on a binary stream with the same 4-byte BE length prefix and 8192-byte cap as Go.

**Depends on.** Phase 0.

**Background.**
- 4-byte big-endian length, max 8192 (DEC-069), tested at `pkg/transport/framing_test.go:42–327`.
- Reject empty messages (length=0), oversized messages (length>8192), truncated reads.

**Files created.**

- `mash_py/wire/framing.py` — `FrameWriter`, `FrameReader`, with both sync and async I/O variants (~150 lines)
- `tests/unit/wire/test_framing.py` — round-trip, boundary acceptance/rejection, truncation
- `tests/vectors/wire/test_framing_vectors.py` — same boundary cases as Go's tests

**Test layers.**

| Layer | What |
|---|---|
| Unit | `len=1` accepted, `len=8192` accepted, `len=0` rejected, `len=8193` rejected, partial length prefix rejected, partial payload rejected. Multiple back-to-back frames in one stream. |
| Vector | Frame a known CBOR message; assert the bytes match Go's framing. |
| Interop | Deferred to Phase 4. |

**Acceptance.** All boundary conditions match Go's behavior.

**Interop verification.** Deferred to Phase 4.

**Refinement contingency.** Endianness mistake is the classic — `struct.pack('>I', n)` is big-endian uint32; `'<I'` is little-endian; pick wrong and length 256 looks like length 16777216.

---

### Phase 3 — Crypto Facade & Certificate Helpers

**Status.** Done (2026-04-26).

**Goal.** A `mash_py.crypto` module that exposes everything the rest of the stack needs in language-agnostic terms.

**Depends on.** Phase 0.

**Background.** Used by transport (TLS context creation, Zone CA loading), commissioning (SPAKE2+ primitives, CSR generation), renewal (cert verification, sequence numbers).

**Files created.**

- `mash_py/crypto/ec.py` — P-256 point ops (add, scalar-mult, hash-to-point, encode/decode SEC1 compressed/uncompressed)
- `mash_py/crypto/kdf.py` — `hkdf_sha256(ikm, salt, info, length) -> bytes`
- `mash_py/crypto/mac.py` — `hmac_sha256(key, data) -> bytes`
- `mash_py/crypto/certs.py` — generate self-signed cert, generate CSR, sign CSR with CA, verify cert chain, extract `AuthorityKeyId` / `SubjectKeyId`
- `tests/unit/crypto/test_ec.py`, `test_kdf.py`, `test_mac.py`, `test_certs.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | RFC 5869 HKDF test vectors. RFC 4231 HMAC-SHA256 test vectors. P-256 test vectors (NIST). Generate a CSR; sign it; verify it. Round-trip an X.509 cert via DER. |
| Vector | RFC test vectors are the universal vectors — no MASH-specific layer needed yet. |
| Interop | Deferred until commissioning phases. |

**Acceptance.** All RFC test vectors pass.

**Interop verification.** Deferred.

**Refinement contingency.** Hash-to-point on P-256 is non-trivial; if the M and N points (from `mash-go/pkg/commissioning/spake2plus.go:38–49`) don't match RFC 9383, the implementation is wrong.

---

### Phase 4 — TLS Transport (Two-Port Deployment per DEC-082)

**Status.** Done (2026-04-26; live interop deferred to Phase 6 — needs the mDNS browser before a Python client can discover a Go device's port). Unit and TLS-loopback coverage is complete; `tests/interop/test_phase4_tls.py` is a scaffold that asserts only the Go toolchain precondition.

**Goal.** Python `Connection` class for clients (TLS 1.3, ALPN-aware) and a two-listener server pattern (commissioning port + operational port, separate SSLContexts). Wire-conformant per DEC-082; no ClientHello parsing or BIO trickery.

**Depends on.** Phases 1, 2, 3.

**Background.**
- Go's `pkg/transport/connection.go:99–509`, `pkg/transport/tls.go:54–176`.
- Two ALPN values: `mash/1` (operational), `mash-comm/1` (commissioning).
- DEC-082 deployment pattern: two listeners on distinct ports; each serves one ALPN exclusively.
- mDNS SRV records (Phase 6, 18) carry the actual port per service.

**Files created.**

- `mash_py/transport/alpn.py` — `ALPN_OPERATIONAL = "mash/1"`, `ALPN_COMMISSIONING = "mash-comm/1"`
- `mash_py/transport/tls.py` — `make_commissioning_context(self_signed_cert)`, `make_operational_context(zone_ca, op_cert, op_key)` factory functions; each `SSLContext` advertises one ALPN exclusively
- `mash_py/transport/connection.py` — `Connection` async class with `read_loop`, `send`, `close`, control-message handling (Ping/Pong/Close)
- `mash_py/service/listeners.py` — `TwoPortListener` managing both `asyncio.Server` instances with a shared connection-cap counter (DEC-064)
- `tests/unit/transport/test_tls.py`, `test_connection.py`, `test_two_port_listener.py`
- `tests/interop/test_phase4_tls.py` — connect Python client to Go device's port (via mDNS SRV), complete TLS 1.3 handshake with `mash-comm/1` ALPN, exchange `ControlMessage{Ping}` ↔ `Pong`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Loopback Python-server↔Python-client TLS handshake completes for both ALPN values; per-listener ALPN exclusivity (commissioning listener rejects `mash/1`, operational rejects `mash-comm/1`); shared cap counter blocks the (max_zones+2)nd connection regardless of which port. |
| Vector | N/A. |
| Interop | **Live test.** Run `mash-go/cmd/mash-device` with a fresh discriminator. Python client discovers its mDNS SRV port (Phase 6), connects, completes TLS 1.3 handshake with `mash-comm/1` ALPN, sends `ControlMessage{Type: Ping, Sequence: 42}`, receives `Pong`. **Then:** start the Python device with two ports. Run `mash-go/cmd/mash-controller --commission --discriminator 5678`. Go controller dials the SRV-advertised commissioning port; TLS handshake completes; PASE proceeds (Phase 8). |

**Acceptance.** Bidirectional interop ping-pong: Python client → Go device (commissioning) AND Go controller → Python device (commissioning).

**Interop verification.**

```bash
# Python-as-client direction
cd mash-go && go run ./cmd/mash-device --commissioning --discriminator 1234 &
cd mash-py && pytest tests/interop/test_phase4_tls.py::test_python_client_connects_to_go_device -v

# Python-as-device direction
cd mash-py && python -m mash_py.cli.device --discriminator 5678 --setup-code 20202021 &
cd mash-go && go run ./cmd/mash-controller commission --discriminator 5678 --setup-code 20202021
```

**Refinement contingency.** Most common failure: TLS handshake itself. Inspect with:
```bash
openssl s_client -connect localhost:8444 -tls1_3 -alpn mash-comm/1 -showcerts
```
If TLS works manually but Python fails, `cryptography` is fine — Python's `ssl` module is stricter about certificate verification. May need to pass `check_hostname=False` and a custom verify callback that matches the Go device's self-signed cert by `CommonName == discriminator`. If the Go controller fails to connect to the Python device's commissioning port, verify the mDNS SRV record advertises the correct port (use `dns-sd -L MASH-5678 _mash-comm._tcp local`).

**Harness coverage caveat.** Once Phase 4 ships, the Python device runs two listeners on different ports. `mash-go/cmd/mash-test` in its current form takes a single `-target host:port` and dials it for both ALPN values — meaning a `mash-comm/1` connection lands on the operational port, fails TLS, and every commissioning conformance case fails noisily. This is a harness gap, not a wire-protocol gap; Phase 22 owns the fix. Until that change lands, conformance against the Python device is run **with the Python device deployed in single-port mode for testing** (or skipped entirely). Document this exception in `tools/run_conformance.sh` when Phase 22 lands.

---

### Phase 5 — `mash_py.wire.peek` Live Verification

**Status.** Done (2026-04-26; live body deferred to Phase 8/11 — Go's commissioning handler rejects non-PASE first frames, so Read/Write/Subscribe traffic can't flow until the PASE client and interaction client land). The spec-driven oracle (`classify_per_spec`) plus its agreement helper (`assert_classifies_per_spec`) are implemented and self-tested in `tests/interop/wire/test_peek_against_go.py`; the live test that drives Go traffic is `pytest.skip(...)` until its dependencies arrive.

**Goal.** Confirm the Phase 1 classifier handles real Go-emitted messages without ambiguity. This is a "promote unit-test confidence to interop confidence" phase.

**Depends on.** Phase 4.

**Files created.**

- `tests/interop/wire/test_peek_against_go.py` — short script that connects to the Go device's commissioning port and watches incoming CBOR; asserts each frame is correctly classified.

**Test layers.**

- Interop only.

**Acceptance.** Run a sequence of Read/Write/Subscribe operations against a Go device, log every frame's classification. Zero misclassifications.

**Interop verification.** Same setup as Phase 4 plus a few synthetic operations.

**Refinement contingency.** If a Notification is misclassified as a Response, the most likely cause is the order of checks in `peek_message_type` — Go's `codec.go:208` checks `field1 == 0` *first*, before any other check. Ensure the Python port preserves order.

---

### Phase 6 — mDNS Discovery (Browser Only)

**Status.** Complete (2026-04-26). 35 unit tests + 2 interop tests green. Python browser resolves a live `mash-device` over real mDNS in ~2s.

**Goal.** A Python controller can browse for commissionable Go devices and resolve them to host:port.

**Depends on.** Phase 0.

**Background.**
- Service constants in `mash-go/pkg/discovery/types.go:8–47`.
- Commissioning service: `_mash-comm._tcp.local`, instance name `MASH-<discriminator>`, TXT `D, cat, serial, brand, model, DN`.
- Operational service: `_mash._tcp.local`, instance name `<DI>-<ZI>`, TXT `ZI, DI, VP, FW, EP`.

**Files created.**

- `mash_py/discovery/types.py` — service constants matching Go
- `mash_py/discovery/browser.py` — async wrapper around `zeroconf.asyncio.AsyncServiceBrowser`
- `tests/unit/discovery/test_browser.py` — uses `zeroconf` test helpers
- `tests/interop/discovery/test_browse_go.py` — start a Go device in commissioning mode, browse with Python, find it

**Test layers.**

| Layer | What |
|---|---|
| Unit | A loopback server advertises a fake `_mash-comm._tcp` record; Python browser finds it and parses TXT records correctly. |
| Vector | N/A. |
| Interop | Start `mash-device --commissioning --discriminator 1234`. Python browser finds it within 2 seconds. TXT records match expected values. |

**Acceptance.** Interop browse succeeds.

**Interop verification.** Standard.

**Refinement contingency.** If browse fails, mDNS often fails on developer machines due to firewall or multicast routing on Wi-Fi. Run with `dns-sd -B _mash-comm._tcp` (macOS) or `avahi-browse _mash-comm._tcp` (Linux) to confirm the Go device's record is on the wire.

---

### Phase 7 — SPAKE2+ Block (7A → 7B → 7C → 7D)

**Why a block.** When the original Phase 7 began (2026-04-26) the Python port surfaced a substantive gap between `docs/testing/behavior/commissioning-pase.md §2` and `mash-go/pkg/commissioning/spake2plus.go` that goes beyond stylistic divergence. The spec section is internally contradictory (M = G·H(label) "with hash-to-curve per RFC 9380" — type-mismatched) and incomplete (`K_shared` invoked but never defined; PASE_X declared `bytes[32]` but compressed P-256 is 33 bytes). The Go implementation is also flawed in ways the spec doesn't catch (no Memory-Hard KDF before HKDF — verifier theft makes the 27-bit setup code brute-forceable in seconds; transcript not length-prefixed; missing identity-element check during point validation). Both artifacts share an author and were written 4 days apart with no DEC documenting either set of choices. See `mash-py-spec-issues.md` Issue #5 for the full eight-finding catalog.

The original Phase 7 ("port spec to Python") cannot meaningfully execute against a contradictory spec, and matching Go silently would defeat the spec-portability validation that is the entire reason `mash-py` exists. The work is therefore restructured into four ordered sub-phases:

| Sub-phase | Owner | Output |
|---|---|---|
| 7A | spec | DEC-084 + rewritten `commissioning-pase.md §2`/§4.2 |
| 7B | mash-go (test harness) | Algorithmic conformance vectors + `mash-test` updates |
| 7C | mash-go (implementation) | Updated `pkg/commissioning/spake2plus.go` passing 7B's vectors |
| 7D | mash-py | Spec-conformant Python implementation passing 7B's vectors and live PASE against 7C |

**Acceptance gate for Phase 7.** All four sub-phases complete; Python and Go produce byte-identical SPAKE2+ intermediates given identical inputs; the algorithmic vectors from 7B run green against both implementations.

---

### Phase 7A — SPAKE2+ Spec Rewrite + DEC-084

**Status.** Done (2026-04-26). DEC-084 filed; `commissioning-pase.md §2` and `§4.2` rewritten; `security.md §4.4` updated to cross-reference; `mash-py-spec-issues.md` Issue #5 marked Resolved.

**Goal.** A complete, self-consistent, security-reviewed specification of MASH's SPAKE2+ handshake. After 7A, two implementers reading only `docs/` can produce byte-identical SPAKE2+ intermediates from identical inputs.

**Depends on.** Nothing (spec-only work). Replaces the broken `commissioning-pase.md §2`.

**Background.** Eight findings from the Phase 7 analysis (`mash-py-spec-issues.md` Issue #5) drive the rewrite. The DEC adopts RFC 9383 + Matter conventions where they exist, fills the spec's gaps explicitly, and adds a Memory-Hard KDF before any HKDF chain (the most security-critical change).

**Files created/modified.**

- `docs/decision-log.md` — new **DEC-084: SPAKE2+ Algorithm Specification (RFC 9383 + MHF)**. Replaces the cryptographic decisions implicit in DEC-014 (which only ratified "use SPAKE2+ with 8-digit codes") with an explicit primitive-by-primitive specification.
- `docs/testing/behavior/commissioning-pase.md` — full rewrite of §2 (Cryptographic Parameters), §4.2 (PASE wire messages — fix `bytes[32]` to 65-byte uncompressed), and a new §2.5 listing security-critical implementation requirements (point validation, identity-element rejection, MHF parameter persistence).
- `docs/security.md` §4.4 — cross-reference DEC-084; remove any text that contradicts the rewrite.
- `mash-py-spec-issues.md` — flip Issue #5 status to ✅ Resolved with DEC-084.

**Decisions the DEC must settle.**

| Decision | Recommendation | Stakes |
|---|---|---|
| MHF | PBKDF2-HMAC-SHA256, 100k iterations | ESP32-friendly (no 64 KB Argon2id memory cost); Matter precedent |
| Per-device salt | 16 bytes random, generated at first commissioning, persisted in NVS alongside discriminator | Salt distribution is the trickiest sub-decision — see §A below |
| Salt distribution | Embedded in QR code OR exchanged via new `PBKDFParameter` PASE message | Matter uses message exchange; pure-QR avoids a round trip but bloats QR |
| Verifier on device | `(w0, L, salt, iterations)` — extends current `(w0, L)` | NVS layout breaking change; tolerable since no production deployments |
| M, N points | RFC 9383 §4 fixed P-256 constants verbatim (literal hex) | Matches Go's current values, matches Matter, ecosystem-standard |
| Transcript layout | RFC 9383 §3.3 length-prefixed: 8-byte LE length per field; A‖B‖M‖N‖pA‖pB‖Z‖V‖w0 | Standard; prevents canonicalization attacks |
| Identity binding | In transcript (RFC 9383 standard), not in HKDF salt | Identities = controller's commissioning-cert SKI + device's discriminator-derived ID |
| K_shared definition | `K_shared = SHA-256(TT)` | Standard SPAKE2+ |
| Session-key chain | Keep spec's K_main / K_confirm / K_encrypt naming and three-key structure (genuinely better than Go's two-key); MASH-specific HKDF info strings | Naming is one of the few good choices in the current spec |
| Confirmation MAC | RFC 9383 §3.4: `KcA‖KcB = HKDF-Expand(K_confirm, "ConfirmationKeys", 32)` then `confirmA = MAC(KcA, pB)`, `confirmB = MAC(KcB, pA)` | Standard mutual authentication |
| Point validation | Mandatory: on-curve check, identity-element rejection, subgroup check (for future curve agility) | Both implementations currently miss the identity-element check |
| Wire size for pA / pB | 65 bytes uncompressed (`0x04 || X || Y`) | Resolves §4.2 inconsistency; fits in single TLS record |

**Test layers.** None — spec phase. Acceptance is review-driven, not test-driven.

**Acceptance.**

1. The DEC is filed with explicit rationale per decision and explicit MHF parameter choice with security analysis.
2. The rewritten `§2` is reviewed end-to-end and contains no type mismatches, no undefined variables, and no contradictions with cited RFCs.
3. A naive implementer (or AI) reading only the spec can derive byte-identical w0, w1, pA, pB, Z, V, K_shared, K_main, K_confirm, K_encrypt, confirmA, confirmB given fixed inputs (setup code, salt, ephemeral scalars, identities). This is verified mechanically in 7B.
4. Spec-issues Issue #5 is closed with the resolution pointing to DEC-084.

**Interop verification.** None — no code yet.

**Refinement contingency.** If the salt-distribution decision (pure-QR vs. PBKDFParameter exchange) proves contentious, the DEC may need a follow-up addendum. Both options are wire-compatible at the SPAKE2+ algorithmic layer; the choice affects PASE message flow (which 7C/7D handle).

---

### Phase 7B — mash-test Conformance Vectors + Harness

**Status.** Done (2026-04-26). 13 algorithmic vectors emitted by `tools/spake2plus_oracle/gen_vectors.py` (third independent implementation, stdlib-only) and stored at `mash-go/testdata/spake2plus_vectors.json`. Vectors cover MHF, w0/w1 split, L computation, client/server public values, transcript, K_shared, three-key chain, confirmation keys, confirmation MACs, an end-to-end `full_handshake`, and both negative `point_validation_*` cases. The Go runner (`mash-test`) and the Python runner (`tests/vectors/commissioning/test_spake2plus_vectors.py`) both report 13 pass / 0 fail / 0 skip.

**Goal.** A set of algorithmic conformance vectors (deterministic-RNG SPAKE2+ traces) plus a runner that exercises any candidate implementation against them, treating mash-test as the executable spec for SPAKE2+. Both 7C and 7D pass these vectors before they are accepted.

**Depends on.** 7A (need stable spec to derive vectors against).

**Background.** Without algorithmic vectors derived independently of either implementation, "Go passes its own tests" and "Python passes its own tests" don't actually prove byte-compatibility. The Phase 7 audit found that mash-go's existing SPAKE2+ tests are self-consistent but don't catch the divergences from RFC 9383 — the test corpus shipped alongside the implementation rather than from the spec. 7B fixes this.

**Files created/modified.**

- `tools/spake2plus_oracle/` — a new top-level Python script that implements DEC-084's algorithm purely by reading the spec text. It is intentionally a third independent implementation (separate from mash-py and mash-go) so the vectors it produces don't accidentally track either runtime's quirks. ~300 LOC. Lives outside `mash-py/` and `mash-go/` to avoid implying it is itself canonical — it is a vector generator, not a reference implementation.
- `mash-go/testdata/spake2plus_vectors.json` — generated by `tools/spake2plus_oracle`. Ships in the repo; regenerated only when DEC-084 changes.
- `mash-go/cmd/mash-test/spake2plus_conformance.go` (new file) or YAML test cases in `mash-go/testdata/cases/` — runs the vectors against an implementation under test.
- `mash-go/cmd/mash-test/cmd_run.go` (modify) — add `-srv-resolve` flag to fix `mash-py-spec-issues.md` Issue #3 (single-port-shaped harness gap that has been deferred to Phase 22; bringing it forward avoids blocking the live-PASE step in 7D and 7C's interop tests against `mash-py`).

**Vectors required (each input/output recorded in JSON).**

| Vector | Inputs | Recorded outputs |
|---|---|---|
| `mhf_basic` | setup_code="20202021", salt=0x00…16 zeros, iterations=100000 | mhf_output (32 bytes) |
| `derive_w0_w1` | mhf_output | w0, w1 (each 32 bytes) |
| `compute_L` | w1 | L (65-byte uncompressed point) |
| `client_init_fixed_x` | w0, x=0x42…32 bytes | pA (65 bytes) |
| `server_init_fixed_y` | w0, L, y=0x84…32 bytes | pB (65 bytes) |
| `transcript` | controller_id, device_id, M, N, pA, pB, Z, V, w0 | TT (variable length), TT_hex |
| `k_shared` | TT | K_shared (32 bytes) |
| `key_chain` | K_shared | K_main, K_confirm, K_encrypt |
| `confirmation_keys` | K_confirm | KcA, KcB |
| `confirm_macs` | KcA, KcB, pA, pB | confirmA, confirmB |
| `full_handshake` | setup_code, salt, identities, x, y | every intermediate above |
| `point_validation` | pA = identity element | expected: rejection |
| `point_validation_off_curve` | pA off curve | expected: rejection |

**Test layers.**

| Layer | What |
|---|---|
| Vector | `tools/spake2plus_oracle/test_self.py` — the oracle's own self-tests; round-trip client+server reach same K_shared on multiple seeds. |
| Conformance | `mash-go/cmd/mash-test`'s new vector runner reads the JSON, invokes the implementation under test (initially mash-go's `spake2plus.go`), and asserts every recorded output matches byte-for-byte. |
| Negative | Point-validation vectors must produce specific errors (not just "any error"). |

**Acceptance.**

1. `tools/spake2plus_oracle` builds and self-tests pass.
2. `mash-go/testdata/spake2plus_vectors.json` exists, ≥12 vectors, each with full intermediates.
3. `mash-test`'s vector runner exists. Initially it fails against unmodified mash-go (since 7C hasn't shipped yet) — that failure is the expected forcing function for 7C.
4. The `-srv-resolve` flag is implemented so 7C and 7D can drive `mash-test` against either single-port or two-port deployments.

**Interop verification.** N/A in 7B; the oracle generates the vectors and self-validates.

**Refinement contingency.** If the oracle's choice of "third implementation" is questioned (e.g., "what if the oracle has the same bug as mash-py?"), supplement with vectors from CircuitMatter or a non-MASH reference. Risk is low because the oracle implements only the algorithmic core (no MASH-specific message handling), and the spec text is mechanically translatable after 7A.

---

### Phase 7C — mash-go SPAKE2+ Update to DEC-084

**Status.** Done (2026-04-26). `pkg/commissioning/spake2plus.go` rewritten to DEC-084 (RFC 9383 fixed M/N, PBKDF2-HMAC-SHA256 100k MHF, derived 16-byte salt, length-prefixed RFC 9383 §3.3 transcript, K_main/K_confirm/K_encrypt three-key chain, KcA/KcB confirmation MAC keys with constant-time compare, mandatory identity-element rejection). Verifier struct extended with `Discriminator`, `Salt`, `Iterations` (CBOR-keyed). PASE wire pA/pB are 65 bytes; ClientIdentity validated to `b"mash-controller"` per §4.2. The pre-DEC-084 stub-conformance file `pkg/commissioning/conformance.go` was deleted (its primitives are now exported from `spake2plus.go`). API breaking: `GenerateVerifier`, `NewSPAKE2PlusClient`, `NewSPAKE2PlusServer`, `NewPASEClientSession`, `NewPASEServerSession` all dropped identity-string parameters; `GenerateVerifier` and `NewSPAKE2PlusClient`/`NewPASEClientSession` now take `discriminator uint16`. Runner Config dropped the now-defunct `ClientIdentity`/`ServerIdentity` fields and the `--client-identity`/`--server-identity` CLI flags. All 22 default `go test ./...` packages clean. The Phase 7B vector runner reports `13 pass, 0 fail, 0 skip`. End-to-end `TestE2E_CommissioningHandshake` passes on the new 65-byte-point wire format.

**Deferred to 7C.1 (now done, 2026-04-26).** `go test -tags=integration ./pkg/service/...` was red on three pre-existing drifts: (a) `discovery.CommissionerInfo` references stranded by DEC-081 (commit `40b4bcf`), (b) cert stores mandatory at commissioning since commit `11889f4` but never wired in the E2E tests, (c) DEC-066 (commit `79b696f`) made operational reconnect an explicit `ControllerService.Reconnect()` call. A fourth post-7C drift was the four PASE-fail tests whose `discoveryService` literals lacked `Discriminator` — harmless pre-DEC-084 but salt-affecting now. All four addressed in commit `ade3281`. Result: 339/344 (98.5%) integration tests pass. The 5 remaining failures (`TestE2E_MaxZonesStopsPairingRequestListening`, `TestE2E_PairingRequestResumesAfterZoneRemoval`, `TestE2E_MultipleCommissioning`, `TestE2E_ReconnectionAfterDisconnect`, `TestE2E_RenewalOverTLS`) are all unrelated to commissioning / SPAKE2+ / cert exchange / operational reconnect.

**Cert-store migration.** The plan called for "a one-line migration that drops pre-DEC-084 verifiers" in `pkg/cert/store.go`. Inspection during 7C showed the Go cert store does not persist verifiers — devices regenerate the verifier from `(setup_code, discriminator)` at every boot via `device_service_lifecycle.go`. The "migration" is therefore a no-op: there is no on-disk verifier to drop. DEC-084 already explicitly accepts this case ("No production deployments yet, so no migration required.").

**Goal (achieved).** `mash-go/pkg/commissioning/` matches the DEC-084 spec and passes every vector from 7B. Existing PASE integration tests pass with updated expectations.

**Depends on.** 7A (spec), 7B (vectors).

**Background.** The current mash-go SPAKE2+ implementation diverges from the new spec in eight places (Issue #5). Most changes are mechanical (new HKDF info strings, new key naming) but two are substantive:

1. Adding the MHF wrapper around `derive_w0_w1`. Go has `golang.org/x/crypto/pbkdf2` — single-import.
2. Restructuring the transcript builder to produce length-prefixed bytes per RFC 9383 §3.3.

A third change touches the verifier storage format: `(w0, L)` becomes `(w0, L, salt, iterations)`. This is a breaking change to the device's NVS layout, but tolerable since there are no production deployments.

**Files created/modified.**

- `mash-go/pkg/commissioning/spake2plus.go` — substantive rewrite. ~50 LOC delta net (most of the changes are 1-line swaps; the MHF and length-prefixed transcript add ~50 lines of new code and remove ~30 lines of the current ad-hoc transcript).
- `mash-go/pkg/commissioning/spake2plus_test.go` — update expected values; add tests for new identity-element check, MHF roundtrip, transcript canonicalization.
- `mash-go/pkg/commissioning/messages.go` — extend `Verifier` struct with `Salt`/`Iterations`; PASE message wire size adjustment if §4.2 wire size changed (currently `bytes[32]` → 65-byte uncompressed).
- `mash-go/pkg/cert/store.go` — verifier-storage breaking change (NVS layout). Add a one-line migration that drops pre-DEC-084 verifiers; document in DEC-084.
- `mash-go/pkg/service/device_service_commissioning.go`, `device_session.go`, `controller_renewal.go` — call-site updates for renamed keys (`sharedSecret` → `K_main`, `confirmKey` → `K_confirm`).
- `mash-go/internal/testharness/runner/security_handlers.go` — call-site updates if the runner peeks at SPAKE2+ intermediates.
- `mash-go/cmd/mash-test/spake2plus_conformance.go` — vector runner from 7B should now pass against mash-go.

**Test layers.**

| Layer | What |
|---|---|
| Unit | All existing `spake2plus_test.go` tests pass with updated expected values; new tests for MHF roundtrip, identity-element rejection, length-prefixed transcript canonicalization. |
| Vector | 7B's vector runner produces zero failures. |
| Integration | `pkg/service/`'s integration-tagged tests (the ones that complete a full PASE) pass with updated cert-store fixtures. |
| End-to-end | `mash-controller` commissions `mash-device` end-to-end; both build, both interop. |

**Acceptance.**

1. `go test ./...` clean across all 22 packages.
2. `go test -tags=integration ./pkg/service/...` clean (this is currently broken on a separate axis — the cert-store mandatory-at-commissioning issue noted in MEMORY.md must be resolved as part of 7C since it touches the same code).
3. 7B's vector runner produces zero failures against mash-go.
4. End-to-end commissioning of `mash-device` from `mash-controller` completes successfully on the new wire format.

**Interop verification.** Deferred — there is no second implementation to interop against until 7D ships. The vector-runner in 7B serves as a partial interop check.

**Refinement contingency.** The integration-tagged test failures predate this work (commit 11889f4 made cert stores mandatory at commissioning, leaving ~14 E2E tests stranded). 7C is the natural place to fix them since the same files are being touched. If scope creep becomes a problem, those test fixes can be split into a 7C.1 follow-up — but they should not be deferred past 7D, since 7D's live-PASE interop test requires a working `mash-device` binary.

---

### Phase 7D — mash-py SPAKE2+ Implementation

**Status.** Done (2026-04-26). `mash_py/commissioning/{__init__,mhf,spake2plus}.py` implement DEC-084 (RFC 9383 fixed M/N, PBKDF2-HMAC-SHA256 100k MHF, derived 16-byte salt, length-prefixed RFC 9383 §3.3 transcript, three-key chain, KcA/KcB confirmation MAC keys, mandatory identity-element rejection). `Verifier` is a frozen dataclass with the same CBOR field numbering as mash-go's struct. State machines (`SPAKE2PlusClient`, `SPAKE2PlusServer`) accept an optional fixed ephemeral so vector tests drive deterministic outputs while production callers always draw fresh `secrets.randbelow`. All 13 Phase 7B conformance vectors pass byte-identically (including the end-to-end `full_handshake` vector and both negative `point_validation_*` cases). 31 round-trip / negative-case unit tests, plus an interop scaffold that runs a minimal `tools/print_verifier` Go helper and asserts byte-equality of `(salt, w0, L)` against mash-go's `commissioning.GenerateVerifier` for two distinct setup codes. Live PASE message exchange is deferred to Phase 8 per plan (single skip-marked sentinel test documents the boundary). Ruff and mypy clean across `mash_py/commissioning/` and the new test packages. Full mash-py suite stays at 360 passing tests (no regressions).

**Goal.** A spec-conformant Python implementation of DEC-084's SPAKE2+ that passes 7B's algorithmic vectors and completes a live PASE handshake against the 7C-updated `mash-device`.

**Depends on.** 7A (spec), 7B (vectors), 7C (Go reference for live interop), Phase 3 (crypto facade), Phase 4 (TLS transport).

**Background.** With 7A–7C done, 7D becomes mechanical: implement the algorithm per spec, test against the algorithmic vectors, then drive the wire-level handshake in Phase 8. The earlier draft of `mash_py/commissioning/spake2plus.py` (uncommitted at the time of this restructure) already encodes most of the structure (client/server state machines, transcript composition, key derivation chain) — much of it carries over with the spec-fix swaps applied.

**Files created.**

- `mash_py/commissioning/__init__.py` — exports.
- `mash_py/commissioning/spake2plus.py` — DEC-084 implementation. ~500 LOC.
- `mash_py/commissioning/mhf.py` — Memory-Hard KDF wrapper (PBKDF2 by default per DEC-084; pluggable for future Argon2id support).
- `tests/unit/commissioning/test_spake2plus.py` — round-trip handshake; identity-element rejection; MHF roundtrip.
- `tests/vectors/commissioning/test_spake2plus_vectors.py` — load 7B's vectors and assert byte-identical outputs.
- `tests/interop/commissioning/test_pase_against_go_device.py` — scaffold (live PASE message exchange ships in Phase 8; this file asserts only the algorithmic layer here).

**Test layers.**

| Layer | What |
|---|---|
| Unit | Round-trip client + server reach same K_shared for multiple seeds. Negative cases: identity-element pA, off-curve pA, wrong setup code, MAC mismatch. |
| Vector | All vectors from 7B pass byte-identically. |
| Interop | Run the algorithmic core only (no PASE messages yet — those are Phase 8): instantiate Python's verifier from setup code 20202021 + salt; verify Go's `mash-device` produces the same `(w0, L)` via debug RPC or log inspection. Live PASE message exchange remains Phase 8. |

**Acceptance.**

1. All vectors from 7B pass.
2. Mutual round-trip handshake succeeds in Python (both parties Python).
3. Static (w0, L) verifier value matches between Python and Go for shared inputs.
4. Ruff and mypy clean across `mash_py/commissioning/`.
5. Phase 7 progress-snapshot row (4 sub-rows) all marked Done.

**Interop verification.**

```bash
# 1. Build Go device with DEC-084
cd mash-go && go install ./cmd/mash-device

# 2. Generate the (w0, L) verifier on Go side
mash-device --print-verifier --setup-code 20202021 --salt-hex 00...16 --iterations 100000

# 3. Generate the same in Python
cd mash-py && python -m mash_py.commissioning.cli print-verifier --setup-code 20202021 --salt-hex 00...16 --iterations 100000

# 4. byte-compare the outputs
diff <(...) <(...)
```

**Refinement contingency.** If a single vector mismatches:
1. The vector's failed-output field localises the divergence (e.g., w0 mismatches → MHF or w0/w1 derivation).
2. The 7B vectors carry intermediate values for every step, so the bisection is at most 5 levels deep.
3. Compare against the `tools/spake2plus_oracle` (third independent implementation) to determine whether mash-py or mash-go is wrong.

---

### Phase 8 — PASE Message Exchange (Client Side)

**Status.** Done (2026-04-26). 24 unit tests + 1 live-interop test green; mash-py controller drives a complete PASE handshake against `mash-go/cmd/mash-device` in commissioning mode and derives a 32-byte `K_main`. Spec-strict on the failure envelope — surfaces mash-go Issue #6 (PASE failures sent as `CommissioningError` instead of `PASEComplete`) as `PASEProtocolError` rather than absorbing the bug.

**Goal.** A Python controller can drive a complete PASE handshake with a Go device.

**Depends on.** Phases 4, 7D (which itself depends on 7A–7C).

**Background.**
- Message types 1–4 in `mash-go/pkg/commissioning/messages.go:8–37`.
- Sequence: PASERequest (C→D, MsgType 1) → PASEResponse (D→C, 2) → PASEConfirm (C→D, 3) → PASEComplete (D→C, 4).

**Files created.**

- `mash_py/commissioning/messages.py` — dataclasses + CBOR codecs for MsgTypes 1–4
- `mash_py/commissioning/pase_client.py` — async state machine
- `tests/unit/commissioning/test_pase_messages.py`
- `tests/interop/commissioning/test_pase_against_go_device.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Encode/decode each message; round-trip via codec. State machine: send Request → expect Response → send Confirm → expect Complete. Error handling: ErrorCode != 0 in Complete aborts cleanly. |
| Vector | Load Go-emitted PASE message bytes; decode; assert fields. |
| Interop | **Live PASE.** Start `mash-device --commissioning --discriminator 1234 --setup-code 20202021`. Python: discover via mDNS (Phase 6), connect TLS (Phase 4), drive PASE to completion. Assert shared secret length is 32 bytes. |

**Acceptance.** PASE completes with a Go device. Both sides log "PASE complete."

**Interop verification.**

```bash
cd mash-go && go run ./cmd/mash-device --commissioning --discriminator 1234 --setup-code 20202021 &
cd mash-py && pytest tests/interop/commissioning/test_pase_against_go_device.py -v
```

**Refinement contingency.** If PASE reaches confirmation but Complete has ErrorCode 1 (AuthFailed), the confirmation MAC computation differs. Bisect by:
1. Capture `pA` and `pB` bytes from both sides.
2. Compute `Z` and `V` on both sides; compare.
3. Compute transcript hash on both sides; compare.
4. The first divergence localises the bug.

---

### Phase 9 — CSR & Certificate Install

**Status.** ✅ Complete (2026-04-27). Codified by DEC-085. Surfaced and resolved Issue #7 (three-doc envelope divergence between `message-types.md` and `security.md`/`stack-architecture.md`/`commissioning-pase.md`/mash-go). End-to-end interop test green: Python controller commissions live mash-go device using spec-strict MsgTypes 10/11/12/13/20 on `mash-comm/1`; device emits `CertInstallResponse` + `CommissioningComplete`, closes commissioning conn, and re-advertises on `_mash._tcp`. Renewal stays separately on `mash/1` with MsgTypes 30–33.

**Goal.** Python controller can issue a Zone CA-signed operational cert to a Go device after PASE.

**Depends on.** Phases 3, 8.

**Background.**
- MsgTypes 10 (CSRRequest, C→D), 11 (CSRResponse, D→C), 12 (CertInstall, C→D), 13 (CertInstallResponse, D→C).
- Cert exchange happens immediately after PASE on the *same* commissioning TLS connection (`mash-go/pkg/service/device_service_commissioning.go:471`).

**Files created.**

- `mash_py/commissioning/messages.py` — extend with CSR/CertInstall messages
- `mash_py/commissioning/cert_exchange.py` — controller-side flow
- `mash_py/commissioning/cert_store.py` (controller side: Zone CA private key)
- `tests/unit/commissioning/test_cert_exchange.py`
- `tests/interop/commissioning/test_full_commissioning_against_go.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Generate a Zone CA; sign a self-generated CSR; verify the resulting cert chain. CertInstallResponse with non-zero ErrorCode aborts cleanly. |
| Vector | Decode Go-emitted CSRRequest/Response/CertInstall messages. |
| Interop | **End-to-end commissioning.** Python controller commissions a Go device. After Complete, controller sends CSRRequest with random nonce; device returns CSR; controller signs with Zone CA; device installs cert; sends CertInstallResponse{Status: 0}; sends CommissioningComplete (MsgType 20). Device's cert store now contains the Zone CA. |

**Acceptance.** Go device transitions to operational state and starts advertising `_mash._tcp` with the new ZoneID.

**Interop verification.**

```bash
cd mash-go && go run ./cmd/mash-device --commissioning --discriminator 1234 --setup-code 20202021 &
cd mash-py && python -m mash_py.cli.controller commission --discriminator 1234 --setup-code 20202021
# Then verify mDNS:
dns-sd -B _mash._tcp
# Should see: <DI>-<ZI>
```

**Refinement contingency.** If CSR signing fails, the most common cause is X.509 extension mismatch. The device expects:
- Subject CN: device's ID
- AuthorityKeyId: matches Zone CA's SubjectKeyId
- KeyUsage: `digitalSignature, keyAgreement`
- ExtKeyUsage: `clientAuth, serverAuth`
Check `mash-go/pkg/cert/generate.go` for the exact extension set.

---

### Phase 10 — Operational Connection (Post-Commissioning)

**Status.** ✅ Complete (2026-04-27). Spec issue surfaced and pending resolution: see `mash-py-spec-issues.md` Issue #9 (``device_id`` overloaded between cert-CN and ``DeviceInfo.deviceId`` attribute).

**Goal.** After commissioning, Python controller reconnects to the Go device on the operational port with mTLS using the issued cert, and exchanges a successful Read on DeviceInfo.

**Depends on.** Phases 4, 9.

**Background.**
- The commissioning TLS connection closes after CertInstallResponse (`device_service_commissioning.go:626`).
- A new TLS connection is opened with `mash/1` ALPN, presenting the operational cert. The device verifies the cert against its stored Zone CA.

**Files created.**

- `mash_py/transport/tls.py` — extend with operational context factory
- `mash_py/service/controller.py` — `Commission` + `Reconnect operational` flow
- `tests/interop/transport/test_operational_connection.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Operational SSLContext loads cert + key + CA correctly. |
| Vector | N/A. |
| Interop | Commission the Go device; immediately reconnect on the operational port; send `Request{MessageID: 1, Operation: Read, EndpointID: 0, FeatureID: 0x01}` (DeviceInfo); receive `Response{MessageID: 1, Status: Success, Payload: {... DeviceInfo attributes ...}}`. |

**Acceptance.** Read succeeds; payload contains expected vendor/product/serial fields.

**Interop verification.** As above.

**Refinement contingency.** If mTLS handshake fails, Python is verifying the device's cert against system CAs. Pass the Zone CA to the SSLContext via `load_verify_locations` and disable hostname check — the device's cert CN is its DeviceID, not a hostname.

---

### Phase 11 — Interaction Model Client (Read/Write/Subscribe/Invoke)

**Status.** ✅ Complete (2026-04-27). All four operations interop end-to-end against mash-go. Issues #10 (messageId off-by-one) and #11 (string-keyed Invoke parameters) both filed and resolved same-day in mash-go: integer-keyed Invoke params + responses per spec §7.1/§7.2 with codegen producing `<Cmd>Param<Name>` / `<Cmd>Resp<Name>` `uint8` constants, single `wire.ToUint8Map` helper for CBOR narrowing.

**Goal.** Full request/response correlation for the controller side, with subscription notification dispatch.

**Depends on.** Phases 1, 2, 10.

**Background.**
- Go's `pkg/interaction/` package handles MessageID matching for pending calls.
- Notifications (MessageID = 0) are routed by SubscriptionID to registered handlers.

**Files created.**

- `mash_py/interaction/client.py` — `InteractionClient` with `read`, `write`, `subscribe`, `invoke` methods returning awaitables
- `tests/unit/interaction/test_client.py`
- `tests/interop/interaction/test_against_go_device.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Pending call map: send Request, receive Response, future resolves. Timeout: pending call with no Response after deadline raises. Notification routing: register subscription handler; receive Notification with matching ID; handler called. |
| Vector | N/A. |
| Interop | Read DeviceInfo; Write DiscriminatorRevocation flag; Subscribe to Status.OperatingState; Invoke `RemoveZone`; verify all responses come back correctly correlated. |

**Acceptance.** All four operations work against a Go device with various endpoint/feature combinations.

**Interop verification.** Standard.

**Refinement contingency.** If Notifications are dropped, check that the Python side is *reading* continuously — Go sends notifications proactively, not in response to a request. The read loop must be running concurrently with the request/response flow.

---

### Phase 12 — Code Generation (Python from YAML)

**Status.** ✅ Complete (2026-04-27). Jinja2 generator under `mash-py/codegen/` emits 15 `*_gen.py` files (10 features, 4 model enums, 1 use-case definitions) into `mash_py/features/` and `mash_py/model/`. `make generate` is deterministic; `make gen-check` is the CI drift gate. Symbol parity with Go's Measurement gen verified (every attribute / command / parameter id has a Python counterpart). YAML 1.1/1.2 bool-coercion trap caught and pinned in `tests/unit/codegen/test_specparse.py`.

**Goal.** A `codegen` tool that reads `docs/features/*.yaml` and emits Python files in `mash_py/features/` and `mash_py/model/`.

**Depends on.** Phase 0 (and YAML discipline; not on prior runtime phases).

**Background.**
- YAML schema in `docs/features/<feature>/1.0.yaml` (see exploration report Phase 4 part 1).
- Go generator templates: `mash-go/cmd/mash-featgen/templates.go`.
- Determinism: iterate `ver.FeatureTypes` slice (not `featureVersions` map). Same discipline applies in Python.
- Per Decision C: separate generator, Jinja2.

**Files created.**

- `codegen/__main__.py` — CLI: `python -m codegen --features-dir docs/features --output mash_py/`
- `codegen/specparse.py` — YAML loaders for `features/*.yaml`, `protocol-versions.yaml`, `_shared/1.0.yaml`, `usecases/1.0/*.yaml`
- `codegen/helpers.py` — `py_type_name`, `py_enum_const`, `py_attribute_id`, etc.
- `codegen/templates/feature.py.j2`, `enums.py.j2`, `shared.py.j2`, `usecase.py.j2`
- `mash_py/features/*_gen.py` (output, committed)
- `mash_py/model/_gen.py`, `mash_py/model/shared_gen.py` (output, committed)
- `mash_py/usecase/definitions_gen.py` (output, committed)
- `tests/unit/codegen/test_specparse.py`, `test_generate.py`
- A CI job that runs `python -m codegen` and fails if the diff is non-empty (catches drift).

**Test layers.**

| Layer | What |
|---|---|
| Unit | Parse a feature YAML; emit Python; the result imports cleanly. Property test: every attribute in YAML produces an attribute constant + getter+setter pair in the generated module. |
| Vector | Generate `measurement_gen.py`; assert it contains `MEASUREMENT_ATTR_AC_ACTIVE_POWER = 1`, `class Measurement(Feature):`, etc. |
| Interop | N/A — codegen is a build-time tool. |

**Acceptance.** Generated Python imports cleanly, has stable output (running twice produces identical files), and matches Go's symbol coverage (every attribute in `measurement_gen.go` has a counterpart in `measurement_gen.py`).

**Interop verification.** N/A directly. Indirect: Phase 13 uses generated code for live Read/Write tests.

**Refinement contingency.** If generation is non-deterministic, the cause is iterating dict-keyed structures without a sort. Mirror Go's lesson — iterate the *list of features* declared in `protocol-versions.yaml`, not the dict that maps name→version.

---

### Phase 13 — Device Model (Endpoints, Features, Attributes)

**Status.** ✅ Complete (2026-04-27). 42 new model unit tests under `tests/unit/model/`; full unit suite is 517 green. `Endpoint` and `Device` containers under `mash_py/model/`; `Feature` split into access-checked `write_attribute()` (external) and `set_attribute_internal()` (telemetry); validation covers type/range/nullable; observers fire on actual change only. Codegen template re-routes read-only setters through `set_attribute_internal` (matches Go's `SetAttributeInternal` pattern); 10 `*_gen.py` files regenerated, `make gen-check` clean. Two spec-text divergences filed as **Issue #12** (`EndpointInfo.featureMap` field 5 vs DEC-074) and **Issue #13** (device-info.md prose feature-ID example vs `protocol-versions.yaml` registry). Both resolve with spec-only edits; see "Spec sweep before Phase 14" below for the disposition checklist. mash-py runtime exposes `Feature.feature_map` / `set_feature_map()` (DEC-074-aligned per-feature) but deliberately *no* `Endpoint.feature_map` aggregator.

**Goal.** A Python device can hold an endpoint hierarchy with features and attributes, with values readable and writable through the in-memory API.

**Depends on.** Phase 12.

**Files created.**

- `mash_py/model/feature.py` — `Feature`, `Attribute`, `Command` base classes
- `mash_py/model/endpoint.py` — `Endpoint`
- `mash_py/model/device.py` — `Device`
- Generated files used as-is.
- `tests/unit/model/test_feature.py`, `test_device.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Construct a Device with Endpoint 0 (DeviceInfo) and Endpoint 1 (EVCharger w/ Measurement, EnergyControl, Status). Read attributes in-memory; write attributes; observers fire. |
| Vector | N/A. |
| Interop | Deferred to Phase 14. |

**Acceptance.** All generated feature classes instantiate correctly. In-memory R/W works.

**Interop verification.** Deferred.

**Refinement contingency.** If a generated feature crashes on instantiation, the codegen template is wrong — fix in Jinja2, regenerate, retest.

---

### Spec sweep before Phase 14 (2026-04-27)

**Status.** Pending. Spec-text only — no implementation code changes in either mash-py or mash-go.

**Why this is its own step.** Phase 13's spec audit surfaced two doc divergences (Issues #12 and #13 in `mash-py-spec-issues.md`) that resolve with spec edits, not code. Disposing of them now keeps Phase 15 (operational server) from emitting an `EndpointInfo` shape that contradicts itself across the spec, the Go reference impl, and the Python port.

**Concrete edits.** Detailed file-and-line patches are tracked in `docs-fix-plan.md` items **H8** (Issue #12) and **H9** (Issue #13). High-level summary:

1. **Issue #12 — `EndpointInfo.featureMap` field 5.** Drop field 5 from the `EndpointInfo` shape and example payload in `docs/features/device-info.md`. DEC-074 (commit `2ccc270`, 2026-04-23) reframed featureMap bits as per-feature variants and dropped the device-level FM key from mDNS TXT for the same cross-feature-aggregation reason; field 5 is the same kind of aggregation. mash-go is already DEC-074-aligned (no field 5 emitted); the spec text is stale.
2. **Issue #13 — feature-ID example/registry drift in `device-info.md`.** Re-do the example endpoint payload with the actual registry IDs from `protocol-versions.yaml`, and replace the embedded "Feature ID Registry" prose table with either a pointer to the YAML or a `mash-docgen`-generated section (same pattern commit `58e9a4b` used elsewhere). Pure documentation; no implementation reads the prose example.
3. **DEC-074 implementation-notes addendum.** One line in `docs/decision-log.md` recording that `EndpointInfo` field 5 is dropped (the mDNS TXT precedent generalizes).

**Acceptance.**
- `mkdocs build --strict` green after the edits.
- `mash-py-spec-issues.md` Issues #12 and #13 marked Resolved with the spec PR commit hash.
- No code change in `mash-py/` or `mash-go/`.

**Depends on.** Phase 13 (which produced the divergence reports).

**Then proceed to.** Phase 14.

---

### Phase 14 — PASE Server (Device Side)

**Status.** ✅ Complete (2026-04-27). `mash_py.commissioning.PASEServer` shipped — async device-side state machine mirroring `PASEClient`'s shape. 12 new unit tests under `tests/unit/commissioning/test_pase_server.py` (PASEClient ↔ PASEServer loopback + hand-crafted bad-frame paths); 2 new live-interop tests under `tests/interop/commissioning/test_pase_server_with_go_controller.py` against a focused Go helper (`tools/run_pase_client/`) that drives `commissioning.PASEClientSession` over TLS commissioning channel. Live byte-equality verified: Python server K_main = Go client K_main = `a4f522e5…01d9` for setup_code=20202021, discriminator=3840. Spec-strict on the failure envelope: every in-PASE failure (wrong identity, invalid pA, MAC mismatch) emits `PASEComplete{ErrCodeAuthFailed}` with zero-padded confirmation — `CommissioningError` is never sent from inside the handshake (Issue #6 closed on both sides). Issue #14 filed (`mash-py-spec-issues.md`) proposing §6.5 spec edit to allow constant-time minimum-duration floor as a conformant alternative to random 100-500 ms delay. Total suite: 459 unit + 536 full (excluding `slow`); mypy + ruff clean.

**Scope correction (2026-04-27).** Phase 14's original Acceptance line claimed "Bidirectional commissioning works (Python↔Go in both directions)." That was overspecified — the Goal/Files/Interop sections all describe PASE-only work, and only PASE shipped here. Full bidirectional commissioning needs the device-side cert-exchange handler (CSRRequest→CSRResponse→CertInstall→CertInstallResponse→CommissioningComplete) to land first; that is split out as the explicit **Phase 14b — Cert Exchange Server (Device Side)** below. Phase 14's Acceptance is corrected accordingly.

**Goal.** A Python device can receive PASE handshake from a Go controller and complete it.

**Depends on.** Phases 4, 7, 13.

**Files created.**

- `mash_py/commissioning/pase_server.py` — async state machine
- `tests/interop/commissioning/test_pase_server_with_go_controller.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Round-trip Python client + Python server (loopback); shared secret matches. |
| Vector | Replay Go-emitted PASE messages; verify Python server response is byte-identical to what Go server would send (using the seeded RNG). |
| Interop | **Reverse PASE.** Start Python device on a kernel-assigned port with discriminator 3840 and setup code 20202021. Run a focused mash-go helper (`mash-py/tools/run_pase_client/`) that drives `commissioning.PASEClientSession.Handshake` over a TLS commissioning channel. Helper exits 0 with derived `K_main_hex`; assert byte-equality with the Python server's `shared_secret`. |

**Acceptance.** Python `PASEServer` accepts a Go `PASEClientSession` over a TLS commissioning channel and both sides derive byte-identical `K_main`. Spec-strict failure envelope: every in-PASE failure path emits `PASEComplete{ErrCodeAuthFailed}` with zero-padded confirmation. (Full bidirectional *commissioning* — including post-PASE cert exchange — is gated on **Phase 14b** below.)

**Interop verification.** As above. Live K_main `a4f522e5…01d9` confirmed for `(20202021, 3840)`.

**Refinement contingency.** Same SPAKE2+ debug technique as Phase 8.

---

### Phase 14b — Cert Exchange Server (Device Side)

**Status.** ✅ Complete (2026-04-27). `mash_py.commissioning.CertExchangeServer` shipped — async post-PASE state machine mirroring Phase 9's `CertExchangeClient` in reverse. 13 new unit tests under `tests/unit/commissioning/test_cert_exchange_server.py` (round-trip with `CertExchangeClient` + chain mismatch + public-key binding mismatch + malformed cert + zone rejection paths + protocol errors); 1 new live-interop test under `tests/interop/commissioning/test_cert_exchange_server_with_go_controller.py` against a new Go helper (`tools/run_commission_client/`) that drives the full commissioning flow (PASE + cert install) against a Python `PASEServer + CertExchangeServer` listener. **Milestone bidirectional acceptance:** mash-py is the first non-Go MASH implementation that can be commissioned end-to-end by a Go controller. Live byte-equality verified across the boundary: Python server's `operational_cert` DER == Go helper's signed cert DER; Python server's `zone_ca_cert` DER == Go helper's CA DER; persisted private key's public-key DER == operational cert's `SubjectPublicKeyInfo`. Spec-strict: every in-cert-exchange failure emits `CertInstallResponse{error_code != 0}` (never `CommissioningError`); zone-policy callback supports both sync and async; out-of-set codes normalised to `ErrCodeCertInstallFailed`. Total suite: 472 unit + 549 full (excluding `slow`); mypy + ruff clean.

**Goal.** A Python device can complete the post-PASE cert-install flow with a Go controller: receive `CSRRequest`, generate a fresh P-256 zone key, return `CSRResponse` with a PKCS#10 CSR, receive `CertInstall`, validate the chain, persist the operational cert + key, ack with `CertInstallResponse`, and emit `CommissioningComplete`.

**Why split from Phase 14.** The original Phase 14 acceptance text claimed "Bidirectional commissioning works" but the Goal / Files / Interop sections all described PASE-only work. Splitting honors the actual scope of each PR: Phase 14 is the SPAKE2+ wire contract, Phase 14b is the cert-install wire contract that follows it on the same commissioning channel.

**Depends on.** Phase 14 (PASE establishes the secure channel this runs over), Phase 9 (`CertExchangeClient` is the controller-side mirror; same dataclasses + state machine shape, reversed direction), Phase 3 (cert helpers — CSR creation, chain validation).

**Files created.**

- `mash_py/commissioning/cert_exchange_server.py` — async device-side state machine. Single-use class consuming the `(reader, writer)` pair handed off by `PASEServer` after a successful handshake. Returns the persisted operational material `(operational_cert, operational_key, ca_cert, zone_type, zone_priority)` on success.
- `tests/unit/commissioning/test_cert_exchange_server.py` — round-trip with `CertExchangeClient` (Phase 9), failure paths (chain mismatch, public-key binding mismatch, non-CSR first frame, malformed CertInstall, etc.).
- `tests/interop/commissioning/test_cert_exchange_server_with_go_controller.py` — full PASE+cert-install flow against a Go helper.
- Either extend `mash-py/tools/run_pase_client/` to also run cert exchange (rename to `run_commission_client`) **or** ship a parallel `mash-py/tools/run_commission_client/` helper. Decide during implementation; cohesive single-flow helper is preferred.

**Test layers.**

| Layer | What |
|---|---|
| Unit | `CertExchangeClient` ↔ `CertExchangeServer` loopback; both sides hold matching operational cert public key after install. Failure paths enforce spec wire contracts. |
| Vector | N/A directly. Phase 9 already shipped frozen `cert_install_vectors.json` testing the controller side; the same vectors exercise the server side via loopback. |
| Interop | Spawn `go run ./run_commission_client --target host:port --setup-code 20202021 --discriminator 3840` against a Python `PASEServer` + `CertExchangeServer` listener. Helper drives PASE then cert install via mash-go's `commissioning` package APIs; emits the installed-on-device fingerprint as JSON. Assert byte-equal cert public-key fingerprint between Python device's persisted op-cert and helper's view. |

**Acceptance.**

- Python device persists operational cert + key after the Go-driven flow completes.
- Operational cert chain validates against the controller-supplied Zone CA.
- Operational cert's `SubjectPublicKeyInfo` matches the public key of the device-generated key pair.
- `CommissioningComplete` reaches the wire before the device closes its side.
- Phase 15 (operational server) can dial the persisted cert and authenticate as expected.

**Interop verification.** As above. With Phase 14 + Phase 14b shipped, mash-py is the *first non-Go MASH implementation that can be commissioned by a Go controller end-to-end* — that's the milestone bidirectional acceptance Phase 14 originally aimed at.

**Refinement contingency.** Cert-chain validation in `cryptography` differs subtly from Go's `x509.Verify` defaults around AKI/SKI tolerance; if interop fails on chain validation, check the spec-vs-Go SKI hash divergence flagged in `mash-py-spec-issues.md` Issue #4. Phase 9 already navigated this on the controller side; the device-side validator should reuse the same lenient-where-spec-says-so primitives.

---

### Phase 15 — Operational Server (Device Side)

**Status.** Done (2026-04-27).

**Goal.** Python device responds correctly to Read/Write/Subscribe/Invoke from a Go controller after commissioning.

**Depends on.** Phases 11, 13, 14, 14b.

**Files shipped.**

- `mash_py/interaction/server.py` — `OperationalServer` request dispatcher: routes Read/Write/Invoke to the appropriate Endpoint/Feature; Subscribe returns `UNSUPPORTED` until Phase 16. `CommandError` exception lets handlers pick a specific `Status` code (mirrors Go's `wire.CommandError`).
- `mash_py/service/device.py` — `DeviceService` operational accept loop. mTLS listener (TLS 1.3, ALPN `mash/1`, CERT_REQUIRED, Zone-CA-pinned) per-connection drives a `_RequestHandler` that decodes inbound Requests and ships back encoded Responses. Stop-event signal makes graceful shutdown safe even with in-flight handshakes.
- `tools/run_operational_client/` — focused Go helper: dials operational ALPN with mTLS, drives the four ops, reports JSON. Uses pre-minted certs (commissioning is exercised in Phase 14b's interop test).
- `tests/unit/interaction/test_server.py` — 22 dispatcher unit tests (all Status branches, spec-strict bare-array Read, Write validation precedence, Invoke integer-keyed params, Subscribe placeholder).
- `tests/unit/service/test_device.py` — 6 service-level tests (Read/Write/Invoke round-trip via in-process Python InteractionClient against a live `DeviceService`, plus stop-while-active idempotency).
- `tests/interop/test_full_device_lifecycle.py` — 1 cross-impl test: Go `run_operational_client` performs all four ops against a Python `DeviceService`; integer-keyed Invoke params verified arrived intact.

**Test layers.**

| Layer | What |
|---|---|
| Unit | 22 dispatcher tests + 6 DeviceService tests covering routing, all Status codes, payload-shape validation, stop semantics. |
| Vector | N/A. |
| Interop | Go controller (focused helper) does Read DeviceInfo / Write label / Invoke removeZone / Subscribe → UNSUPPORTED against a Python DeviceService. Verified end-to-end. |

**Acceptance.** Met. Phase 15's primary gate — "controller reads / writes / invokes against the Python device" — passes both in-process and cross-impl.

**Spec-vs-Go decisions made (2026-04-27, user-approved).**

- (A) Read payload encoding: spec §4.1 bare-array form is canonical. mash-go's `ExtractReadAttributeIDs` was patched to also accept the bare-array form (it was previously buggy: Go's own controller emits the bare array, but the device-side extractor only matched `{1: [...]}`, so Read-with-IDs silently degraded to Read-all). mash-py device accepts both for forward-compat.
- (B) Unsubscribe gating: spec §6.4 requires both `endpointId == 0` AND `featureId == 0`. mash-go's `handleSubscribe` was tightened (was `featureId == 0` alone). mash-py inherits the spec-strict shape via Phase 16's plan.
- (C) Partial Read with unknown attribute IDs: matches Go's strict-per-ID first-failure-wins (returns `INVALID_ATTRIBUTE`). Spec §4.2 was silent; the choice is now explicit in `mash_py/interaction/server.py`.

mash-go fixes landed in the same workstream: `pkg/wire/message.go::ExtractReadAttributeIDs` (added bare-array + `[]uint16` short-circuit branches) and `pkg/service/dispatch/protocol_handler.go::handleSubscribe` (spec-strict gate). Tests added: `pkg/wire/message_test.go` (6 cases) and `protocol_handler_test.go::TestProtocolHandler_HandleRead_BareArrayWireForm` + `TestProtocolHandler_UnsubscribeRequiresBothEndpointAndFeatureZero`. Full Go suite green.

**Spec note (per DEC-074, see "Spec sweep before Phase 14").** When emitting `DeviceInfo.endpoints` (attribute id 20), each `EndpointInfo` is **four-field** (`{1: id, 2: type, 3: label, 4: features}`). The aggregated per-endpoint `featureMap` slot has no DEC-074-consistent semantics; capability variants are discovered by reading the per-feature global attribute `featureMap` (0xFFFC). `mash_py.model.Endpoint` deliberately exposes no `feature_map` aggregator for this reason.

---

### Phase 16 — Subscriptions & Notifications

**Status.** Done (2026-04-27). DEC-086 ratified seven spec gaps; mash-go updated in lockstep.

**Goal.** Python device emits notifications when subscribed attributes change. Python controller receives and routes them.

**Depends on.** Phase 15.

**Files created.**

- `mash_py/interaction/subscription.py` — `SubscriptionManager` (per device), `NotificationDispatcher` (per zone session)
- Per Go's `pkg/service/dispatch/notification_dispatcher.go:82–89`: a 100ms polling loop checking the model for changed attributes, batching Notifications.
- `tests/interop/interaction/test_subscriptions.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Subscribe to attributes; mutate them; expect notifications within MaxInterval. Coalescing: rapid changes within MinInterval coalesce to one notification. |
| Vector | N/A. |
| Interop | Go controller subscribes to Status.OperatingState on Python device. Python device transitions OperatingState. Go controller receives Notification. |

**Acceptance.** Notifications flow in both directions (Python device → Go controller, Go device → Python controller from Phase 11).

**Interop verification.** Standard.

**Refinement contingency.** Watch for the "write-before-notify" ordering rule (DEC-071): when handling a Write, the device must dispatch notifications *before* sending the Write response. Implement as: write → notify subscribers synchronously → send response. Otherwise the controller can race and receive the response before the notification, which violates ordering invariants.

---

### Phase 17 — TestControl Feature & Trigger Handlers

**Status.** Done (2026-04-28).
- `mash_py/features/test_control.py` — handwritten companion to `test_control_gen.py`. Defines the full trigger opcode catalogue (parity with mash-go), `TestControlFeature` subclass with enable-key gate + dispatcher hooks (`set_trigger_handler`, `set_duration_handler`), TEST-zone gate via callback accessor, clamped `setCommissioningWindowDuration` (3–10800s).
- `tests/unit/features/test_test_control.py` — 21 unit tests; cover key-match/mismatch, async dispatcher awaiting, TEST-zone gate (allow/deny/unknown), clamp, missing-handler `UNSUPPORTED`, missing/non-string parameters → `INVALID_PARAMETER`, `testEventTriggersEnabled` reflects key validity (32-hex, non-zero).
- `tests/interop/interaction/test_trigger_test_event.py` — 3 interop tests (all green): Go controller end-to-end invokes `triggerTestEvent` for the canonical Enter-commissioning opcode, the wrong-enable-key path, and a `CommandError`-raising handler.
- `tools/run_operational_client/main.go` — extended with `--trigger-opcode`, `--trigger-enable-key`, `--skip-default-ops` so the helper can drive trigger-only invokes without running Phase 15's Read/Write/RemoveZone/Subscribe sequence.
- Three spec-vs-Go gaps surfaced as **Issue #15** in `mash-py-spec-issues.md` (A: error envelope on enable-key mismatch, B: TEST-zone-only enforcement, C: full opcode catalogue absent from spec).
- mDNS verification in the interop test was deferred to Phase 18 (mash-py has no commissioning advertiser yet); Phase 17's interop instead asserts the registered callback observed the spec opcode and the wire response carried `{1: True}`.

**Goal.** Python device implements TestControl (feature 0x0A on endpoint 0) so that `mash-test`'s trigger commands work.

**Depends on.** Phase 15.

**Background.**
- TestControl spec in `mash-go/pkg/features/test_control.go` (handwritten — *not* generated).
- Trigger opcodes in the same file: domain (16 bits) + 16 bits zero + 16 bits zero + trigger (16 bits).
- enableKey: 128-bit hex string default `00112233445566778899aabbccddeeff`.

**Files created.**

- `mash_py/features/test_control.py` — handwritten implementation
- `tests/interop/test_trigger_test_event.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | TriggerTestEvent invocation with correct/incorrect enableKey returns success/error. Each opcode fires the right effect (e.g., `TriggerEnterCommissioningMode` opens commissioning window). |
| Vector | N/A. |
| Interop | Go controller invokes `TriggerTestEvent(enableKey, opcode=TriggerEnterCommissioning)` on Python device. Python device opens commissioning window. Go controller verifies via mDNS that `_mash-comm._tcp` is now advertised. |

**Acceptance.** All opcodes the test suite uses (per the trigger table in `pkg/features/test_control.go:9–66`) work.

**Interop verification.** Standard.

**Refinement contingency.** If a trigger has no observable effect, the device's state machine is wired wrong — fixing the trigger handler should be a one-line change in most cases.

---

### Phase 18 — mDNS Advertiser

**Status.** Done (2026-04-28). Spec divergence resolved by DEC-088; advertiser landed; all five discovery interop tests green (Python↔Go both directions, including commissioning→operational transition and multi-zone).

**Goal.** Python device advertises commissioning + operational mDNS records correctly.

**Depends on.** Phase 6 (browser uses same primitives).

**Spec divergence (surfaced 2026-04-28, resolved by DEC-088).**

Porting Phase 18 surfaced three internal contradictions in the operational mDNS schema:

1. **Operational TXT `ZI` field.** `docs/discovery.md` §2.3 omits `ZI` and gives instance name `<device-id>._mash._tcp.local`; `docs/testing/behavior/discovery.md` §3.3 makes `ZI` required and uses instance `<zone-id>-<device-id>`; mash-go follows the behavior doc. The high-level §2.3 phrasing is structurally incompatible with multi-zone (DEC-082) — a single-instance name has no room for per-(device, zone) advertising.
2. **Operational TXT `FM` field.** Both discovery docs still list `FM` (hex feature map); DEC-074 explicitly removed it ("MUST NOT advertise"). mash-go is DEC-074-compliant via `TestOperationalTXT_OmitsFeatureMap`.
3. **Commissionable instance-name padding.** Spec is silent; mash-go zero-pads to 4 digits (`MASH-NNNN`). Cosmetic but worth normative-izing for golden-test fixtures.

**Resolution.** Treat DEC-074 + the behavior doc as canonical; rewrite the high-level prose to match. New decision-log entry **DEC-088** "Operational mDNS TXT canonicalization" makes this normative.

**Files created.**

- `mash_py/discovery/advertiser.py`
- `tests/interop/discovery/test_advertise.py`

**Files modified (spec alignment, must land before / with implementation).**

- `docs/decision-log.md` — add DEC-088 (operational TXT keys + instance-name format normative; supersedes stale §2.3 prose).
- `docs/discovery.md` — rewrite §2.3 operational TXT (add `ZI` required, drop `FM` per DEC-074, change instance name to `<zone-id>-<device-id>`); add §2.2 padding rule (`MASH-NNNN`, 4-digit zero-padded).
- `docs/testing/behavior/discovery.md` — drop the `FM` row from §3.3 and the `FM` row from §3.5 (point at DEC-074).
- mash-go: no code changes required (DEC-074 already enforced; instance name + ZI already canonical). Verified via `go test ./pkg/discovery/ -run TestOperationalTXT`.

**Test layers.**

| Layer | What |
|---|---|
| Unit | Advertiser publishes a record; loopback browser finds it; TXT records parse correctly; commissioning → operational transition publishes the new instance and goodbyes the old. |
| Vector | N/A. |
| Interop | Python device advertises commissioning record; Go controller browses and finds it. After commissioning, Python device transitions to operational record; Go controller finds the new record. |

**Acceptance.** All of:
1. DEC-088 landed; `docs/discovery.md` §2.2/§2.3 + `docs/testing/behavior/discovery.md` §3.3/§3.5 reconciled.
2. mash-go discovery tests still green (no Go changes expected; verify).
3. Python advertiser publishes commissioning record discoverable by mash-go controller within ~2 s; transition to operational record discoverable likewise.
4. Bidirectional interop: Python controller (Phase 6 browser) finds Go device's commissioning + operational records.

**Interop verification.** Standard.

**Refinement contingency.** Per DEC-088 (post-resolution): commissioning instance is `MASH-NNNN` (4-digit zero-pad); operational instance is `<zone-id>-<device-id>` (16+1+16=33 chars). If mDNS browse finds the record but the harness can't parse the instance name, the canonical format moved during this phase — re-read DEC-088.

---

### Phase 19 — Certificate Renewal (MsgTypes 30–33)

**Status.** Done (2026-04-28).

**Goal.** Both directions of in-session cert renewal work.

**Depends on.** Phases 9, 11, 15.

**Background.**
- MsgTypes 30 (CertRenewalRequest, C→D), 31 (CertRenewalCSR, D→C), 32 (CertRenewalInstall, C→D), 33 (CertRenewalAck, D→C).
- Nonce binding (DEC-047): device returns SHA256(nonce)[0:16] in the CSR message.
- 7-day renewal window (DEC-077).

**Pre-implementation cross-repo alignment (audit findings).**

Auditing spec vs mash-go before porting surfaced three small drifts. Decisions taken (entry into Phase 19):

1. `CertRenewalRequest.ZoneCA` field — present in `mash-go/pkg/commissioning/renewal_messages.go` as `omitempty`, **NOT** in any spec doc. Pre-DEC-085 leftover (renewal envelope was once reused for commissioning cert install). **Decision: spec is correct — drop from Go and stay out of Python.** Patch landed: `mash-go/pkg/commissioning/renewal_messages.go` field removed; no consumer assigned to it.
2. `CertRenewalCSR.NonceHash` — spec (`security.md §11.3`) declares it required and exactly 16 bytes; Go used `cbor:"3,keyasint,omitempty"`, so the wire shape allowed omission even though the device-side handler always populated it. **Decision: spec is correct — required on encode and on decode in both impls.** Patch landed: removed `omitempty`, decoder now rejects missing/wrong-length NonceHash, two new Go regression tests (`TestDecodeRenewalMessage > CertRenewalCSR_RejectsMissingNonceHash`, `…_RejectsWrongLengthNonceHash`).
3. `CertRenewalAck.status` codes 1–3 (`CSRFailed`, `InstallFailed`, `InvalidCert`) — present in Go since DEC-047, undocumented in `security.md §11.3` (only code 4 was listed). **Decision: Go's set is operationally useful, spec is missing the rows.** Patch landed: `security.md §11.3` table now lists 0–4 with descriptions; both impls keep the same numeric values.

**Files created.**

- `mash_py/commissioning/renewal_messages.py`
- `mash_py/commissioning/renewal.py` — controller-side and device-side state machines
- `tests/unit/commissioning/test_renewal_messages.py`
- `tests/unit/commissioning/test_renewal.py`
- `tests/interop/commissioning/test_cert_renewal.py`

**Files modified (cross-repo alignment).**

- `docs/security.md §11.3` — declared NonceHash required + 16 bytes; documented status codes 0–4; spelled out CertRenewalRequest envelope (no ZoneCA).
- `mash-go/pkg/commissioning/renewal_messages.go` — removed ZoneCA field; required NonceHash on decode.
- `mash-go/pkg/commissioning/renewal_messages_test.go` — updated round-trip tests, added two reject-cases for NonceHash.

**Test layers.**

| Layer | What |
|---|---|
| Unit | Round-trip every MsgType. Nonce hash validation (incl. constant-time compare via `hmac.compare_digest`). Decoder rejects CSR without NonceHash and with wrong-length NonceHash. Sequence number monotonicity. |
| Vector | N/A — vectors generated in Phase 6. |
| Interop | Commission Python↔Go in either direction; controller initiates renewal; device returns CSR with nonce hash; controller signs and installs; device acks. After renewal, operational connection survives (no TLS reconnection per DEC-076). |

**Acceptance.** Renewal completes both directions; mash-go full test suite green after the renewal_messages cleanup.

**Interop verification.** Standard.

**Refinement contingency.**

- Nonce hash format: SHA256 of nonce, *truncated to 16 bytes*, not full 32. Ref: `mash-go/pkg/commissioning/renewal_messages.go:14–36`.
- Renewal-vs-data-plane disambiguation on `mash/1`: renewal frames are flat CBOR maps with key 1 = `uint8` ∈ {30..33} and never carry key 4. Python reuses the same heuristic Go uses (`pkg/service/zone_session.go::isRenewalMessage`).

---

### Phase 20 — Failsafe & Multi-Zone Resolution

**Status.** Pending.

**Goal.** Python device supports multiple zones (max 1 GRID + 1 LOCAL + 1 TEST per DEC-043), with limit/setpoint resolution, failsafe gating, and **per-zone TLS server cert selection**.

**Depends on.** Phase 15, Phase 4 (two-port listeners).

**Background.**
- Zone resolution rules: `mash-go/pkg/zone/zone.go:135–232`.
- Failsafe gate: DEC-073.
- **Per-zone server cert selection.** A device commissioned to multiple zones holds one operational cert per Zone CA. When a controller from Zone A reconnects, the device must present Zone A's cert. Go uses `tls.Config.GetCertificate` driven by SNI (`mash-go/pkg/service/device_service_lifecycle.go:242–255`); controllers send the device ID as SNI; the device looks up the cert by SNI match. Post-handshake, the device identifies the zone cryptographically by matching the peer cert's `AuthorityKeyId` against each Zone CA's `SubjectKeyId` (`device_service_zone.go:34–52`).

**Python equivalent.** `SSLContext.sni_callback(ssl_socket, server_name, ssl_context)` fires after SNI is parsed; we swap `ssl_socket.context` to a per-zone `SSLContext` whose cert chain matches the requested device ID. This is stdlib-supported; no BIO trickery needed.

**Files created.**

- `mash_py/zone/zone.py` — `MultiZoneValue`, `ResolveLimits`, `ResolveSetpoints`
- `mash_py/zone/pool.py` — zone state container
- `mash_py/service/failsafe.py` — failsafe timer per zone
- `mash_py/transport/tls.py` — extend `make_operational_context` to accept a per-zone cert dict and register an `sni_callback` for per-zone cert selection (controller sends device ID as SNI; device looks up the matching zone's operational cert)
- `mash_py/service/device.py` — extend dispatch path to identify zone by `AuthorityKeyId` post-handshake (mirrors `device_service_zone.go:34–52`)
- `tests/interop/test_multi_zone.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Most-restrictive limit resolution: GRID limit 1000W, LOCAL limit 500W → effective 500W. Highest-priority setpoint: GRID setpoint 0W, LOCAL 1000W → effective 0W. TEST zone observer-only. |
| Vector | N/A. |
| Interop | Commission Python device with two Go controllers (GRID and LOCAL). Both write limits. Python device's effective limit matches resolution rules. |

**Acceptance.** Resolution matches Go behavior.

**Interop verification.** Requires running two Go controllers; coordinate via different setup-code/discriminator pairs.

**Refinement contingency.** Sign convention is the trap (consumption +, production -). Watch limit signs.

---

### Phase 21 — Test Harness (Python Runner Subset)

**Status.** Pending.

**Goal.** A Python `mash-py-test` CLI that loads YAML test cases from `mash-go/testdata/cases/`, drives a Go device, and reports pass/fail. Ports the *minimum useful subset* of `mash-go/internal/testharness/runner/`.

**Depends on.** Phases 6, 11, 18.

**Files created.**

- `mash_py/testharness/loader.py` — port of YAML schema parsing
- `mash_py/testharness/runner.py` — subset: discover, commission, run steps, teardown
- `mash_py/testharness/handlers/` — handler implementations for `connect`, `commission`, `read`, `write`, `invoke`, `subscribe`, `wait_notification`, `trigger_test_event`, `verify_commissioning_state`, `compare`, `evaluate`
- `mash_py/testharness/pics.py`
- `mash_py/cli/test.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Each handler implementation tested in isolation against a mock device. |
| Vector | N/A. |
| Interop | Run a small subset of `mash-go/testdata/cases/commissioning-*.yaml` against a Go device using `mash-py-test`. Compare results to running `mash-go/cmd/mash-test` against the same device. |

**Acceptance.** At least 5 test cases pass identically under Go and Python harnesses.

**Interop verification.** Standard.

**Refinement contingency.** Handler implementations may diverge in subtle ways from Go's runner. Compare wire logs side-by-side.

---

### Phase 22 — Conformance Gate (mash-test against Python device)

**Status.** Pending.

**Goal.** The Go `mash-test` binary, run against a Python device, passes a target subset of conformance tests.

**Depends on.** Phases 14–20.

**Prerequisite (mash-go harness change — DEC-082 compliance).**

Before Phase 22 acceptance can be reached, `mash-go/cmd/mash-test` must become deployment-shape-agnostic. Today the runner takes a single `-target host:port` and dials it for both ALPN values (`internal/testharness/runner/runner.go:825-831`). This works against a single-port device (mash-go) by accident — both ALPNs share the same TCP port — but bypasses DEC-082's "mDNS SRV port is authoritative" contract. Against a two-port device (mash-py from Phase 4 onward), commissioning connections would dial the operational port and every commissioning conformance case would fail.

The fix is a small mash-go PR landed *as part of Phase 22's first commit*:

- Add `-srv-resolve` flag (or a `--target mdns:<instance-name>` form) that opts the runner into per-ALPN mDNS SRV resolution.
- When opted in, the runner browses `_mash._tcp` for `mash/1` connections and `_mash-comm._tcp` for `mash-comm/1` connections, then dials the SRV-advertised host:port per service.
- Default behaviour stays as today (`-target host:port` for both ALPNs) so existing CI invocations don't break.
- Phase 22's `tools/run_conformance.sh` invokes mash-test with the SRV-resolve flag enabled.

This change covers single-port and two-port deployments with one harness path, validates DEC-082's discovery contract end-to-end, and creates no per-implementation harness branching. mash-go's own conformance run can also flip on the flag once mDNS reliability on the CI environment is acceptable; that flip is out of scope for this phase.

**Target subset (defined explicitly to set scope):**
- All `commissioning-*.yaml` cases
- All `base-protocol-*.yaml` cases
- All `interaction-*.yaml` cases (Read/Write/Subscribe/Invoke)
- All cases tagged `mvp` or untagged (default)

**Excluded (Phase 22 scope):**
- `env:multi-device` cases (require multi-device orchestration)
- Any `slow` cases (CI gate would balloon)

**Files created (mash-py).**

- `tests/conformance/test_against_go_runner.py` — pytest wrapper that invokes `mash-go/cmd/mash-test` against a freshly started Python device
- `tools/run_conformance.sh`

**Files modified (mash-go).**

- `internal/testharness/runner/runner.go` — add `-srv-resolve` flag and per-ALPN SRV resolution path (~80 LOC)
- `cmd/mash-test/main.go` — wire the flag through
- `internal/testharness/runner/runner_test.go` — unit-test the SRV-resolve path against a mock browser

**Test layers.**

- Conformance only.

**Acceptance.** Go's `mash-test --target mdns:MASH-1234 --srv-resolve` passes the target subset against a Python device deployed in two-port mode.

**Interop verification.**

```bash
# Terminal 1: Python device in two-port mode (operational 8443, commissioning 8444)
cd mash-py && python -m mash_py.cli.device --discriminator 1234

# Terminal 2
cd mash-go && go run ./cmd/mash-test \
  --target mdns:MASH-1234 --srv-resolve \
  --setup-code 20202021 --filter "TC-COMM-*,TC-IM-*"
```

**Refinement contingency.** Each failing test points to a specific protocol gap. Address by re-opening the relevant phase. Don't add ad-hoc patches in Phase 22 — fix at the layer that owns the gap.

If `-srv-resolve` itself misbehaves (mDNS browse times out, wrong port resolved), the failure surface is mash-go's harness, not mash-py's protocol. Reproduce with `mash-go/cmd/mash-test` against a fresh `mash-go` device first to isolate; mash-go is single-port, so SRV resolution must yield the same port for both ALPNs and any divergence is a harness bug.

---

## 8. Conformance Gate

**The conformance criterion is binary and lives at `mash-go`'s mash-test.** A Python implementation that doesn't pass `mash-test` is not done, regardless of how green its own unit suite is.

**Three conformance tiers:**

| Tier | Definition | Phase complete when |
|---|---|---|
| **MVP** | Single-zone commissioning + Read/Write on DeviceInfo | Phase 17 done |
| **Standard** | All non-multi-device, non-slow cases pass | Phase 22 done |
| **Full** | Including multi-device and slow cases | Stretch beyond this plan |

The plan covers MVP and Standard. Full is explicitly out of scope until Standard is rock-solid.

---

## 9. CI / Tooling

A new GitHub Actions workflow at `.github/workflows/python.yml`:

```yaml
jobs:
  unit:
    # Just Python unit tests on each push.
  vectors:
    needs: [generate-vectors]  # Go side regenerates JSON vectors
    # Python loads vectors and asserts.
  interop:
    needs: [unit]
    # Build mash-go binaries; run Python tests that spawn them.
  conformance:
    needs: [interop]
    # Phase 22 — full mash-test run against Python device.
    timeout-minutes: 15
```

`generate-vectors` runs the Go-side `tools/gen_vectors.go` and commits any changes to `tests/vectors/` via a bot — drift becomes visible in PRs.

`make conformance` is the developer-side equivalent for local runs.

---

## 10. Risks & Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Two-port deployment violates a Go-spec convention | None (resolved by DEC-082) | — | Was a concern under DEC-067-only; DEC-082 makes two-port first-class conformant. |
| Shared connection-cap counter (DEC-064) bug between two listeners | Low | Medium | Phase 4 unit tests assert (max_zones+2)nd connection rejected regardless of which port it lands on; covers race with `asyncio.Lock` around the counter. |
| SPAKE2+ port has subtle deviation that only surfaces under specific seeds | Low (after Phase 7 restructure) | High | Phase 7B vectors derived from a third-party oracle (`tools/spake2plus_oracle`) independent of both runtimes; full intermediate values per vector enable bisection. Risk dropped Medium → Low because 7A's spec rewrite removes the underspecification that previously enabled silent divergence. |
| Codegen drift between Go and Python emitters | Low (with CI gate) | High | CI job that diffs spec coverage |
| `mash-test` discovery via mDNS fails in CI environment | Medium | Medium | Allow `--target host:port` bypass for CI; default to mDNS for dev |
| `cryptography` package can't be installed in some target environments | Low | High | Phase-2 swap to pure-Python primitives (Decision B) is pre-planned |
| Asyncio race conditions in the read loop | Medium | Medium | Mirror Go's lock discipline (capture references under lock; release before slow operations) — see CLAUDE.md project memory on DeviceService lock discipline |
| Python performance on ESP32 (CircuitPython) prohibits real workloads | Low (Phase 1) | Low (Phase 2 stretch) | Defer; benchmark when phase 2 begins |

---

## 11. Open Questions for the User

These need answers before phase 1 starts:

1. **Decision A (ALPN routing).** ✅ Resolved by DEC-082 (2026-04-25): mash-py uses two-port deployment per the new spec-conformant pattern.
2. **Decision B (CircuitPython).** Confirm "phase 1 uses `cryptography`, phase 2 swaps for CircuitPython" approach?
3. **Decision C (codegen).** Confirm separate Python+Jinja2 generator (rejecting the `--lang python` flag idea)?
4. **Decision D (asyncio).** Any objection to asyncio (vs. threads, trio)?
5. **Decision E (SPAKE2+ source).** Confirm CircuitMatter-as-reference (MIT, attribution preserved)?
6. **Scope of phase 22.** Is the "Standard" tier (all non-multi-device, non-slow) the right MVP target, or should phase 22 aim narrower (only commissioning + IM)?
7. **Repository discipline.** New `mash-py/` peer to `mash-go/` in the same repo? Or a separate `mash-python` repo? The plan assumes the former.
8. **Performance bar.** Is there a target throughput (e.g., requests/sec) the Python implementation must hit, or is "works correctly" enough for now?

---

## Approval Trail

This plan is the contract. Once approved:

- Each phase is implemented top-to-bottom with TDD discipline.
- Each phase ends with the documented interop verification before the next begins.
- Plan changes during implementation are made by editing this file in the same PR as the implementation, not by silently absorbing surprises.

---

*End of plan.*
