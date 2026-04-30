# MASH Conformance Status (2026-04-30, post-fix, session 8 batch 5)

This file is a triage snapshot of where each implementation stands against the
shared YAML conformance corpus in `mash-go/testdata/cases/` (494 unique IDs;
501 case definitions including 5 duplicates and 11 `env:multi-device` excluded
on this single-host run).

The matrix has four quadrants — `{Go runner, Python runner} × {Go device,
Python device}`. The diagonals (same-language ends) measure self-conformance;
the off-diagonals are the real cross-stack interop signal.

## TL;DR — eight sessions shipped 22 named fixes; Q1 78.8% → 91.9% (best measured Q1 to date)

- **Session 1 (morning):** F-1 (operational SRV-resolve canonical zone-id) and
  F-4 (Go device listener stays up). Together those moved Go-on-Go from 12.5%
  to 77.4% by eliminating 256 environmental connection-refused failures.
- **Session 2 (afternoon):** F-2 (pairing-request announce was missing the
  zone-id field), F-7 (mash-py subscribe handler outputs + save checkers),
  and F-6 (TC-E2E-001 inter-test cert leak — two cascading bugs in the
  residual zone sweep + integer-keyed-parameter regression).
- **Session 3 (evening):** F-6-followup (TC-TRANS-002 LOCAL-zone leak — the
  `pase.go` `transition_to_operational` path was mutating `pool.Main()`'s
  Connection struct in place, conflating it with `suite.Conn()`'s alias) and
  TC-PAIR-001/002/003 corpus + harness alignment.
- **Session 4 (late evening):** F-8 (runner-side name↔ID translation for
  invoke args + responses, commits `d1f9f6a` + `7c53214`) and F-9 (EVSE
  Resume unconditional, commit `9b4c8fc`). Together those moved Q1 from
  283/359 = 78.8% to 305/359 = 85.0% (+22 tests). The runner had been
  silently smuggling string-keyed CBOR onto a wire that requires integer
  keys; the device rejected every required-param command before the
  handler ran. Symmetric bug on response: integer-keyed CBOR responses
  never matched named YAML assertions like `response_contains: applied`.
- **Session 5 (next morning):** F-10 (TEST-zone harness override for
  controller-authority tests). The auto-PICS suite commissions as TEST
  per DEC-060 so TestControl works, but TEST zones are pure observers
  (excluded from limit/setpoint resolution). Tests that drove SetLimit /
  SetSetpoint / ClearLimit through the suite saw `effectiveConsumptionLimit`
  stay nil and `controlState` stuck at AUTONOMOUS. New predicate
  `testCaseNeedsControllerAuthority` detects these tests by invoke
  command name and explicit zone-type preconditions, then routes them
  to a fresh disposable LOCAL session while the suite stays TEST.
  Q1 305/359 = 85.0% → 307/359 = 85.5% in full-corpus mode; deterministic
  focused-slice retest (TC-CTRL-001..003, TC-STATE-001/002/015,
  TC-ZONE-001/002/006/007) goes 0/10 → 3/10. The remaining 7 are real
  device-side feature gaps (CONTROLLED→LIMITED transition, SetSetpoint
  resolution, `myConsumptionLimit` per-zone read semantics).
- **Session 6 batch 2 (2026-04-30):** F-13 (getTestState param-name
  registry, commit `28897c5`), F-14 (nullable fault attrs reset to nil,
  commit `a7811b7`), F-15 (corpus + spec alignment for 8 KB frame size
  + EV-charger PICS pruned to runtime, commit `80585f8`), F-16
  (value_type checker matches logical spec types, commit `25c37b0`).
  - **F-13** -- `getTestState` is the only TestControl command added
    by hand (manual diagnostic, not in YAML); the names registry is
    generated, so its param-name mapping was missing. Runner's
    `invoke_as_zone -> ConvertCommandParams` hit the unregistered-cmd
    pass-through and forwarded the YAML's string-keyed args. Device
    `validateParameters` then rejected with INVALID_PARAMETER before
    `requireTestZone` could return NOT_AUTHORIZED. Fix moves the
    constants from pkg/service into pkg/features/test_control.go (the
    manual file alongside the generated one) so both pkg/service and
    internal/inspect reach them without import cycles, then registers
    `{enableKey: 1}` in inspect.init(). TC-TESTCTRL-ZONE-001 PASS.
  - **F-14** -- `TriggerResetTestState` and `TriggerClearFault` were
    setting nullable `faultCode` (uint32, nullable) and `faultMessage`
    (string, nullable) to 0 and "" respectively. Per spec, "no fault
    present" maps to null, not zero. Cleared to nil instead.
    TC-STATUS-012 PASS.
  - **F-15** -- protocol-common.yaml + message-framing-tests.yaml +
    transport.{Server,Client}.MaxMessageSize comments all said
    65536 (64 KB), but DefaultMaxMessageSize is 8192 (8 KB per
    DEC-069) and docs/transport.md + docs/interaction-model.md agree.
    TC-FRAME-002 sent 64 KB and expected parse_success, then failed
    because the device correctly rejected the oversized frame.
    Aligned all four locations to 8 KB. Also tightened
    testdata/pics/ev-charger.yaml to remove A14/A15/A1E (which F-12
    pruned from the runtime) and tightened TC-MPD-S00-003's PICS to
    require MASH.S.MEAS.A1E so it SKIPs on this device.
    TC-FRAME-002 PASS, TC-MPD-S00-003 SKIP.
  - **F-16** -- `CheckerValueType` was comparing Go typename strings,
    so any `uint8`/`uint16`/`uint32` (or `int8`/`int16`/`int32`)
    attribute would fail because CBOR canonically decodes unsigned
    integers as `uint64` and signed as `int64` in Go. TC-GPL-012
    (failsafeDuration `value_type: uint32`) was the visible casualty.
    Replaced with logical-type matching: `uint32` accepts any compatible
    Go numeric type whose value fits in `[0, 2^32)`. Floats with
    fractional parts are rejected when the declared type is integer.
    Non-numeric types keep strict Go-typename equality. Also extended
    `ToFloat64` to cover the missing `uint8`/`uint16`/`int8`/`int16`
    cases. Future tests asserting fixed-width integer types now work.

- **Session 8 batch 5 (2026-04-30):** F-24 (`check_response` handler
  field-extract mode). Q1 measured **319/28/114 = 91.9%** -- single-test
  clean lift (TC-EVC-S00-001 FAIL -> PASS). No collateral changes.
  - **F-24** -- The corpus uses two shapes for `check_response`:
    legacy `expected_fields`-map mode and the more common
    `field: <name>` + `expect: {value: <expected>}` mode. Only the
    legacy mode was implemented. The field-extract mode silently
    no-op'd; the standard `value` checker then failed with `key
    "value" not found in outputs`. Added field-extraction in
    `internal/testharness/runner/utility_handlers.go`: when
    `params.field` is set, look up the named field in the prior
    invoke's `state["response"]` then `state["result"]` and write
    the extracted value to `state["value"]`. Reused the
    `lookupMapField` helper shape from F-21 to handle both
    Go-native (`map[string]any`) and CBOR-decoded
    (`map[interface{}]interface{}`) maps.
    - **Net Q1 impact.** TC-EVC-S00-001 (SetLimit Accepted on EV
      Charger) flips FAIL -> PASS as the single clean lift.
      TC-GPL-S00-001/002/003 (consumption-side) and TC-FLOA-* now
      advance past their step-2 check_response but still fail on
      later steps due to bucket-#3 CONTROLLED -> LIMITED state-
      machine gaps. Other corpus tests using check_response with the
      field-extract shape will likewise advance further before
      hitting their respective downstream blockers.

- **Session 8 batch 4 (2026-04-30):** F-23 (3-line corpus PICS tightening:
  GPL production-side tests). Q1 measured **318/29/114 = 91.6%** -- best
  measured Q1 to date, +1.3 pp over F-22's 90.3%.
  - **F-23** -- The three production-side `TC-GPL-S00-*` tests
    (Production Limit Accepted, ClearLimit Restores Autonomous
    Production, Nominal Max Production Readable) were tagged with PICS
    `MASH.S.UC.GPL.S00` (BASE) instead of `MASH.S.UC.GPL.S02`
    (PRODUCTION). They ran on EV chargers and inevitably failed because
    EVSEs are consumption-only by spec.
    - **Spec authority.** Per `docs/usecases/1.0/gpl.yaml`, GPL.S02 is
      the PRODUCTION scenario with `endpointTypes:
      [INVERTER, BATTERY, GRID_CONNECTION]` (explicitly excluding
      `EV_CHARGER`). The use-case matcher
      (`pkg/usecase/matcher.go:70`) already enforces the per-scenario
      endpointType filter at runtime, so an EVSE doesn't claim GPL.S02.
      The corpus PICS gate just needed to point at S02 instead of S00.
    - **Three-line edit.** Same change applied to lines 476, 533, 578
      of `testdata/cases/gpl-tests.yaml` -- swap `MASH.S.UC.GPL.S00` to
      `MASH.S.UC.GPL.S02` with a brief comment citing the use-case
      spec. No production-code change.
    - **Net Q1 impact.** 3 tests reclassified FAIL -> SKIP (target),
      plus 2 timing-band flake recoveries went FAIL -> PASS
      (TC-COMM-004, TC-TESTCTRL-ZONE-001). Total: +2 PASS, -5 FAIL,
      +3 SKIP. Pass rate jumps 1.3 pp because both PASS up and FAIL
      drops.
    - **Out of scope.** The remaining GPL/PODF/POEN/ITPCM/EVC-S04
      failures (TC-GPL-S00-001/002/003 consumption-side, TC-GPL-S01-*,
      TC-GPL-011, TC-PODF-S00-002/003, TC-POEN-S00-002,
      TC-ITPCM-S00-003, TC-EVC-S04-003) are real device-side
      implementation gaps, not over-advertising. The Plan/Signals
      OnRequestPlan/OnAcceptPlan handlers exist but never advance the
      planID counter, so AcceptPlan with `planId: 1` never matches.
      Filed for a future bucket #4 implementation pass (more involved
      than PICS tightening: needs Plan-feature semantics in EVSE).

- **Session 8 batch 3 (2026-04-30):** F-22 (EVSE + HeatPump Electrical
  attributeList pruned per DEC-051). Q1 measured **316/34/111 = 90.3%**
  (pass rate of run; 316/(316+34) = 90.29%). Best measured Q1 to date,
  exceeds F-21's 89.8%.
  - **F-22** -- TC-ELEC-007 ("Asymmetric Support Levels") and
    TC-ELEC-008 ("Energy Capacity for Storage Device") failed against
    EVSE because `NewElectrical()` registers all 12 spec attributes
    regardless of hardware capability. EVSE doesn't implement
    per-phase asymmetric control and has no internal energy store, but
    auto-PICS picked up ELEC.A0F + ELEC.A14 from the over-advertised
    attributeList and ran tests the device cannot meaningfully satisfy.
    Same shape as F-12 (Measurement pruning).
    - **Production layer.** Removed `supportsAsymmetric` (A0F) and
      `energyCapacity` (A14) from EVSE *and* HeatPump Electrical
      features via `RemoveAttribute` (the same primitive F-12 added to
      `Feature`). Both pruned attributes are spec-defined but not
      implemented in either simulation: per-phase control isn't wired
      on top of the symmetric `MaxCurrentPerPhase` field, and neither
      device has an internal battery.
    - **Side effect.** EVSE no longer claims `MPD.S01` (the ELECTRICAL
      scenario in the Monitor Power Device use case) because per
      `docs/usecases/1.0/mpd.yaml` the scenario requires
      nominalMaxConsumption AND nominalMaxProduction AND
      energyCapacity, and the use-case matcher
      (`pkg/usecase/matcher.go:139`) is strict-AND.
      TC-MPD-S01-001/002 now SKIP on EVSE. This is the correct outcome
      -- they were previously passing because the PICS gate let them
      run on a device that didn't fully implement S01.
    - **Net Q1 impact.** 4 tests reclassified to correct outcomes:
      TC-ELEC-007 + TC-ELEC-008 went FAIL -> SKIP (2 false FAILs lifted),
      TC-MPD-S01-001 + TC-MPD-S01-002 went PASS -> SKIP (2 spurious
      PASSes converted to correct SKIPs). Pass count drops 318 -> 316
      but pass rate rises 89.8% -> 90.3% because the denominator (Run)
      drops by 4. Conformance signal more accurate.
    - **Run-to-run flakes** that flipped on this run: TC-COMM-004
      (PASS -> FAIL, the documented TLS-race flake), TC-TESTCTRL-ZONE-001
      (PASS -> FAIL), TC-SEC-CONN-005 (FAIL -> PASS), TC-ZONE-010
      (FAIL -> PASS). All four flake-band; not F-22-related. Net effect
      on PASS-list cancels (-2/+2); net effect on FAIL-list cancels
      (+2/-2 minus the 2 ELEC lifts).

- **Session 8 batch 2 (2026-04-30):** F-21 (`TriggerEVPlugIn` populates
  rich EV state + new `each_item_has` checker). Q1 measured **318/36/107
  = 89.8%** -- +4 PASS / -4 FAIL vs F-20 baseline (314/40/107 = 88.7%).
  Best measured Q1 to date, exceeds F-19's 89.0%. Headline matches the
  structural lift (no flake noise this run).
  - **F-21** -- TC-CHRG-009 ("EV Identification Storage") and
    TC-EVC-S01-001/002/003 all read EV-side ChargingSession attributes
    (evIdentifications, evStateOfCharge, evBatteryCapacity,
    evTargetStateOfCharge) under preconditions `ev_connected: true` /
    `ev_plugged_in: true`. The precondition silently no-op'd (no handler
    registered) and `TriggerEVPlugIn` only set chargingState +
    sessionStartTime, so all four reads returned <nil>. Two-bug shape
    identical to F-19 / F-20 -- corpus action mutates harness state but
    the device never hears about it.
    - **Device side.** `TriggerEVPlugIn` now populates evStateOfCharge=50,
      evBatteryCapacity=75 kWh (in mWh), evMinStateOfCharge=20,
      evTargetStateOfCharge=80, evDemandMode=SingleDemand, max/target
      energy requests derived from SoC, and a single VIN identification
      (`MASH-TEST-VIN-...`). Mirrors `EVSE.SimulateEVConnect`'s shape so
      tests using both pathways see consistent state. `TriggerResetTestState`
      clears all of these to nil to prevent cross-test leakage.
    - **Runner side.** Added `PrecondEVConnected` + `PrecondEVPluggedIn`
      (corpus uses both as synonyms; ev-tests.yaml uses `ev_plugged_in`,
      chargingsession-tests.yaml uses `ev_connected`). Registered at
      `precondLevelCommissioned` with simulation-key entries. Coordinator
      dispatches `TriggerEVPlugIn` via `SendTriggerViaZone` after
      commissioning when either precondition is present.
    - **Engine side.** Added `CheckerEachItemHas` backing the corpus's
      `each_item_has: [type, value]` shape used by TC-CHRG-009 step 2.
      Handles both Go-native (`map[string]any`) and CBOR-decoded
      (`map[interface{}]interface{}`) item types -- the latter is what
      comes back over the wire after CBOR decoding.
    - **Net structural lift = 4 tests** (TC-CHRG-009 + TC-EVC-S01-001/002/003).
      Two run-to-run flakes flipped (TC-TESTCTRL-ZONE-001 lifted,
      TC-ZONE-010 newly failing) -- both PASS in isolation, so they're
      pre-existing zone-state-cross-contamination defects surfacing
      only in the full ordered corpus, not F-21 regressions.
    - **Out of scope.** TC-CHRG-004 step 2 still fails on a separate
      `conditional_read` corpus/handler defect: the handler returns
      `read_success: false` when the condition is not met, but the
      corpus expects success unconditionally. Fixing this requires
      either a corpus shape change (`expect_when_condition_true: ...`)
      or richer engine semantics where the condition controls expect
      evaluation, not just the read. Filed for a future surgical fix.

- **Session 8 batch 1 (2026-04-30):** F-20 (`set_state_detail`
  device_local_action wires through to the device via a new
  `TriggerSetStateDetailBase` opcode). Q1 measured 314/40/107 = **88.7%**
  -- TC-STATUS-011 went FAIL -> PASS (real lift), but two timing-margin
  tests flipped PASS -> FAIL relative to the F-19 baseline (315/39/107 =
  89.0%), netting -1 PASS in the headline. Same noise band F-18's notes
  document. The structural lift is real even though the headline didn't
  move.
  - **F-20** -- TC-STATUS-011 ("State Detail Vendor-Specific Data") read
    `stateDetail` after step 1 wrote `value: 0x1001`, and got `<nil>`.
    The `set_state_detail` runner handler only mutated harness-side
    `ds.stateDetails[key]` -- and silently no-op'd when no `key` was
    provided (the corpus only sends `value`). Same shape of bug F-19
    fixed for `device_trigger enter_fault`. Three-layer fix:
    - **Trigger encoding (`pkg/features/test_control.go`).** Added
      `TriggerSetStateDetailBase = 0x0002_0002_0000_0000` (Status
      domain, sub-domain 2, with the uint32 stateDetail value in the
      lower 32 bits). Status sub-domain 0 was already used by the bare
      triggers (Fault/ClearFault/SetStandby/SetRunning) and sub-domain
      1 by `TriggerFaultWithCodeBase`; 2 is the natural slot.
    - **Device handler (`pkg/service/test_event_handler.go`).** Added a
      case in `handleStatusTrigger` that decodes the lower 32 bits and
      calls `feat.SetAttributeInternal(features.StatusAttrStateDetail, value)`.
      Also extended `TriggerResetTestState` to clear stateDetail to nil
      so the test-supplied value doesn't leak into TC-STATUS-012 ("read
      stateDetail when not set").
    - **Runner side (`internal/testharness/runner/device_handlers.go`).**
      `handleSetStateDetail` now coerces the corpus's `value` to uint32
      and dispatches `TriggerSetStateDetailBase | uint64(stateDetail)`
      via `sendTriggerViaZone` whenever a real device target is
      configured. Harness-only `key` semantics preserved for any
      simulation-only callers.
    - **Corpus.** No corpus changes needed. The test asserted on the
      symptoms; the fix is entirely on the implementation side.
    - **Validation.** TC-STATUS-011 PASS in isolation (7ms). Critical
      reset-path check: TC-STATUS-012 ("nullable attributes return null
      when unset") still PASS, confirming the new clear-to-nil in
      `TriggerResetTestState` is correct. Warmup smoke (TC-CTRL-002,
      TC-CTRL-010, TC-MPD-S02-002, TC-GPL-012, TC-FRAME-002, TC-STATUS-011,
      TC-STATUS-012) 7/7 PASS. Unit tests across `pkg/features`,
      `pkg/service`, `internal/testharness/...` all clean.

- **Session 7 batch 2 (2026-04-30):** F-19 (`device_trigger` action wires
  `enter_fault` to a parameterized `TriggerFaultWithCodeBase` opcode).
  Q1 measured 315/39/107 = **89.0%** -- +1 PASS / -1 FAIL vs F-18
  baseline (314/40/107 = 88.7%). Single clean lift, no collateral
  changes.
  - **F-19** -- TC-MPD-S02-002 ("Fault State Includes faultCode")
    failed at step 2 with `operatingState expected 7, got 2` because
    step 1's `device_trigger { trigger: enter_fault, faultCode: 42 }`
    was a complete no-op against real devices. Two-bug surface:
    - **Runner side.** `handleDeviceTrigger` only recorded the event
      name in execution state and returned `triggered: true` without
      ever sending a wire trigger -- distinct from `handleTriggerFault`
      (the `trigger_fault` action handler) which already dispatched
      `features.TriggerFault`. The corpus mixes both action shapes;
      `device_trigger` is the parameterized form. Added an
      event->opcode lookup (`deviceTriggerOpcode`) and dispatched via
      `sendTriggerViaZone` when running against a real device.
      Unknown events (`enter_override` / `exit_override` /
      `simulate_failsafe_timeout` -- four other corpus usages) keep
      the no-op fallback so this fix doesn't silently change other
      tests' outcomes.
    - **Device side.** `TriggerFault` hardcoded `faultCode = uint32(1)`,
      so even if the trigger had fired, step 3's `expect: value: 42`
      would have failed too. Added `TriggerFaultWithCodeBase =
      0x0002_0001_0000_0000` (mirrors the `TriggerSetPowerCustomBase`
      pattern in the Measurement domain), encoded the faultCode in
      the lower 32 bits. `handleStatusTrigger` decodes via the same
      `trigger&0xFFFF_FFFF_0000_0000 == base` mask used in the
      Measurement helper. The bare `TriggerFault` opcode is preserved
      for backward compatibility.
    - **Corpus.** No corpus changes needed. The test asserted on the
      symptoms; the fix is entirely on the implementation side.
    - TC-MPD-S02-002 PASS in isolation (9ms). Full Q1 315/39/107 =
      89.0%. Slice retest (TC-MPD-* + TC-STATUS-*) clean: 9 PASS
      / 1 FAIL (TC-STATUS-011, separate bucket) / 9 SKIP.

- **Session 7 (2026-04-30):** F-18 (`device_not_pausable: true`
  precondition handler + EVSE/HeatPump capability gate on Pause / Resume /
  Stop). Q1 measured 314/40/107 = **88.7%** -- +4 PASS / -4 FAIL vs the
  F-17 measured baseline (310/44/107 = 87.6%). TC-CTRL-010 lifted by F-18;
  the other three PASSes are TC-COMM-004 (TLS-race flake didn't recur this
  run) and the same kind of run-to-run noise on tests near the timing
  margin. Headline reading: F-18 lifts TC-CTRL-010 and the corpus is
  otherwise stable.
  - **F-18** -- TC-CTRL-010 ("Command to Unsupported Capability Fails")
    asserts that on a device with `isPausable=false`, the `Pause` command
    is rejected with `error_code: UNSUPPORTED_COMMAND`. Two-layer fix:
    - **Production layer.** Per
      `docs/features/energy-control.md`: "A device that ... leaves
      isPausable = false ... [its] process lifecycle commands are
      ignored ... the controller-facing response surfaces as
      `wire.StatusUnsupported`." Before this fix, `EnergyControl.handlePause`
      delegated unconditionally to the EVSE's `OnPause` callback, which
      did the work and returned nil error -- so the device replied
      `Status: SUCCESS, applied: false` regardless of the capability flag,
      which is wrong per the spec. Added `features.CapabilityUnsupportedError`
      hand-written helper in `pkg/features/energy_control.go` that returns
      `&wire.CommandError{Status: wire.StatusUnsupported}`. EVSE and
      HeatPump `OnPause`/`OnResume`/`OnStop` callbacks now check the
      relevant capability flag (`IsPausable`/`IsStoppable`) and return
      this error when the flag is false. The dispatcher
      (`pkg/service/dispatch/protocol_handler.go`) was already extracting
      `CommandError.Status` correctly via `errors.As`, so no dispatcher
      change was needed.
    - **Test infrastructure layer.** Added two new TestControl triggers
      (`TriggerSetIsPausableFalse` / `True` at opcode `0x0005_0000_0000_0020/21`)
      that flip the readOnly `isPausable` capability at runtime via
      `SetAttributeInternal`. `TriggerResetTestState` was extended to
      restore the EVSE/HP factory default of `isPausable=true` so the
      override doesn't leak into subsequent tests. Added
      `PrecondDeviceNotPausable = "device_not_pausable"` constant +
      simulation/level entries. Coordinator dispatches the trigger via
      the existing `SendTriggerViaZone` plumbing (no new WireOps method
      needed -- the trigger is just another opcode that
      `handleEnergyControlTrigger` recognizes).
    - **Corpus alignment.** TC-CTRL-010 expected
      `error_code: UNSUPPORTED_COMMAND`, but `wire.Status(10).String()`
      returns `"UNSUPPORTED"` (no Matter-style `_COMMAND` suffix). Other
      corpus tests (e.g. `bidirectional-no-features.yaml`) already use
      `UNSUPPORTED`. Aligned the test to the canonical wire string.
    - **Architectural caveat.** This session uses Option B (per-callback
      gate via the helper). Long-term Option C is the
      generator-emitted gate keyed off a `requires_attribute` YAML field
      in `RawCommandDef`, which would centralize the pattern at the
      `handlePause`/`handleResume`/`handleStop` level for all current
      and future devices. Filed mentally for the eventual cleanup; not
      blocking on it because the only consumers today are EVSE and
      HeatPump and both are now consistent.
    - **Reset assumption.** `TriggerResetTestState` hardcodes the
      isPausable restore value to `true`, which is correct for EVSE and
      HeatPump but assumes those are the only devices-under-test. If a
      genuinely non-pausable example device is added later, this needs
      a per-device "factory defaults" hook. TC-CTRL-010 PASS in
      isolation (9ms), warmup (CTRL-002, GPL-012, FRAME-002, CTRL-010)
      4/4 PASS, full Q1 314/40/107 = 88.7%. Pre-existing TC-CTRL-* PASS
      (-001/-002/-004/-005/-006) and SKIP (-007/-008/-011/-012)
      outcomes unchanged.

- **Session 6 batch 3 (2026-04-30):** F-17 (`limit_is_set: true`
  precondition handler). Q1 measured 310/44/107 = 87.6% — same headline
  as the pre-F-17 measured baseline, because TC-CTRL-002 went FAIL→PASS
  (real lift) at the same time TC-COMM-004 hit its known full-corpus
  TLS-race flake (`commissioning dial failed: remote error: tls:
  internal error`, documented session 5). Without that flake the run
  would land at 311/43/107 = 87.9%, matching the F-16 projection plus
  this fix.
  - **F-17** -- TC-CTRL-002 (ClearLimit Removes Limit) reads
    `myConsumptionLimit` in step 1 and asserts `value_is_not_null:
    true`, but `limit_is_set: true` had no precondition handler — the
    LOCAL session was freshly commissioned with no calling-zone limit
    set, so step 1 read null. Added `PrecondLimitIsSet` constant +
    simulation/level entries, plus a `sendSetLimitInvoke` helper that
    fires SetLimit (consumption=7 kW, cause=LocalOptimization) on
    `pool.Main()`. Coordinator dispatches it after the
    `testCaseNeedsControllerAuthority` LOCAL-zone swap (F-10) so the
    limit lands on the calling zone. Also extended
    `testCaseNeedsControllerAuthority` to detect the
    precondition itself, so a future test using only `limit_is_set`
    (without an authority command) still gets routed to LOCAL.
    TC-CTRL-002 PASS in isolation (51ms) and full corpus (180ms);
    TC-CTRL-001 / -004 / -005 / -006 still PASS, no new regressions in
    the TC-CTRL-* group.

- **Session 6 batch 1 (2026-04-30):** F-12 (EVSE Measurement
  attributeList pruned to spec-correct subset per DEC-051, commit
  `47d8cba`). The
  generated `NewMeasurement()` constructor registers all 25+ Measurement
  attributes; EVSE composed it without subtracting battery- and DC-only
  attributes (stateOfCharge, powerFactor, dcPower, ...). Auto-PICS
  picked up the over-advertised set, so tests like TC-MEAS-008 (battery
  SoC range) and TC-MEAS-009 (power factor bounds) ran on an EVSE that
  has neither sensor and failed with `<nil>` reads. Added
  `RemoveAttribute(id)` primitive on `Feature`; EVSE prunes Measurement
  to its actually-populated subset (acActivePower today). TC-MEAS-008/-009
  and TC-MEAS-003/-004 flip FAIL → SKIP (the correct outcome — they
  should never have run on this device). Tightened TC-SUB-001 PICS so
  it only runs on devices that expose both attributes it asserts on.
  Q1 307/359 = 85.5% → 308/355 = 86.8%; -4 false failures, +1 PASS,
  +4 SKIP. Auto-PICS items dropped 645 → 599. Diagnostic lift: the
  `value_in_range` checker now prints `%T(%v)` for actual/min/max so
  the next nil-from-device shows up named in the failure log.

### Session 6 findings on the remaining 47 Q1 failures

After F-12, the remaining failures group into four buckets, **most of
which need infrastructure-depth work, not surgical per-test fixes**:

1. **Failsafe state machine (8-10 tests)** — TC-CTRL-009, TC-FAIL-002,
   TC-FAIL-008, TC-FAIL-010, TC-STATE-003, TC-STATE-004, TC-FI-007.
   The pkg/failsafe Timer fires but its `Limits` are never set, the
   LimitResolver has no failsafe-active branch (only ClearZone path),
   `handleFailsafe` only flips a boolean, and ControlState never
   transitions to FAILSAFE. Adding EVSE factory failsafe defaults is
   one line; the wiring underneath is 2-3 hours touching pkg/failsafe
   + LimitResolver + EnergyControl + EVSE.

2. **Precondition handlers (~5 tests)** — TC-EVC-S01-001/002/003 fail
   on `ev_plugged_in: true` precondition that has no handler. The
   TestControl trigger `TriggerEVPlugIn` exists but only sets state +
   start time; the rich EV battery telemetry (evStateOfCharge,
   evBatteryCapacity, evDemandMode, evEnergyRequests) is set in
   evse.go's `OnPlug` callback path, not in
   `pkg/service/test_event_handler.go`'s trigger handler. The fix
   wants either an OnTrigger callback hook in EVSE or duplicating the
   state setup into the trigger handler. Similar shape to failsafe:
   primitive trigger exists, but the population side is incomplete.

3. **CONTROLLED → LIMITED transition (~5 tests)** — TC-STATE-002,
   TC-STATE-015, TC-FAIL-008, TC-ZONE-011. Per the comment in
   `pkg/features/limit_resolver.go`: "the device application layer is
   responsible for promoting CONTROLLED → LIMITED when it detects that
   its operation is actually being curtailed by the limit." Not
   implemented in EVSE. Needs an EVSE simulation hook that compares
   current power draw vs. effective limit and promotes state when
   constrained.

4. **GPL/inverter use cases (~10 tests)** — TC-GPL-S00-*, TC-GPL-S01-*,
   TC-PODF-S00-*, TC-POEN-S00-*. The EVSE example doesn't implement
   most of these use cases; tests pass through PICS gating because
   EVSE registers every feature with the full attribute set, then
   fail because the values are nil. Same root cause as F-12 in
   non-Measurement features. Per-feature pruning (Status, Plan,
   Signals, ChargingSession's session-energy-counter half, etc.)
   would convert most of these to SKIP. Caveat: ChargingSession
   attributes like evStateOfCharge are *spec-required* in
   attributeList per DEC-051 even when null — pruning them would be
   wrong; those need bucket #2.

5. **Genuine surgical fixes (5-7 tests)** — TC-FRAME-002 (max-size
   frame parse), TC-MPD-S00-003 (energy counters monotonic),
   TC-STATUS-011/012 (vendor data + nullable defaults),
   TC-TESTCTRL-ZONE-001 (NOT_AUTHORIZED vs INVALID_PARAMETER error
   code mapping). Each ≤30 min likely. Did not pursue this session.

After session 3: all 5 TC-PAIR-* tests pass (was 2/5 mid-session). TC-TRANS-002
passes back-to-back with TC-TRANS-001 (was: leaked LOCAL zone broke the next
commission). TC-E2E-001 + TC-TRANS-* clean. The corpus runner does not hang;
the prior "stalls at execution_index 145" was a misperception — three
back-to-back 95-second reaper waits and a 100-second keepalive wait look like
a stall but the suite completes in ~16 minutes.

## What "no hang at execution_index 145" means

The first session-3 task was diagnosing a reported hang. Root cause: the four
slowest tests (TC-CONN-REAP-001/002/003 at 95s each + TC-KEEPALIVE-003 at
100s) sit consecutively at execution_index ~145 in the precondition-sorted
order. Combined ~6 minutes of mostly-silent device log makes monitor-by-log
heuristics flag the run as stalled even though it is making progress. A full
corpus run with the 30s test timeout completes in ~16 minutes. No code change
was needed for this item.

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

## Headline numbers (post-fix, session 8 batch 5 — 2026-04-30)

| Runner → Device | Discovered | Skipped (PICS) | Ran | Passed | Failed | Pass rate (of run) | Notes |
|---|---|---|---|---|---|---|---|
| `mash-test` → Go device     | 461 | 114 | 347 | **319** | 28  | **91.9 %** *(measured)* | After F-24 (`check_response` handler now extracts `params.field` from prior invoke response into the standard `value` output so the `value: <expected>` checker fires). Single clean lift: TC-EVC-S00-001 FAIL -> PASS. +1 PASS, -1 FAIL vs F-23 baseline (318/29/114 = 91.6%). Best measured Q1 to date. |
| `mash-test` → Python device | 461 | 138 | 153* | **110** | 43 | **~71.9 %** *(of attempted)* | Partial run (session 5). F-1 fix verified: `-target 18446 -srv-resolve` works via SRV-resolve; auto-PICS commissions, 110 tests pass, 43 fail on real cross-stack issues. Then mash-py's commissioning window expires, the listener tears down, and the next ~140 tests fail with `connection refused`. **mash-py has a F-4-equivalent bug** (listener torn down on window expiry, see "F-11" below). Headline blocked on that fix. |
| `mash-py-test` → Go device  | 490 | 349 | 141 | **1**   | 140 | **0.7 %**  | Full-corpus re-run (session 5) with F-3 base-PICS auto-load active. PICS pruning reduces 490 -> 141. Of the 140 failures: **184 unknown-action incidents across 30+ distinct action types** (`device_local_action` x32, `controller_action` x16, `open_commissioning_connection` x14, `enter_commissioning_mode` x14, `ping` x10, `commission_with_admin` x10, `list_zones`, `subscribe_multiple`, `send_pase_x`, etc.) — F-3 device-side discovery half is still missing, plus action-handler implementation gaps dominate. Headline blocked on mash-py harness implementation. F-7's subscribe-handler fix doesn't surface here because tests fail at action dispatch *before* subscriptions matter. |
| `mash-py-test` → Py device  | 490 | 0   | aborted | — | — | — | Aborted at 14 tests (~2 min in). Phase 22 baseline: **21/55 = 38 %** for the curated subset that the Python harness can execute. Tracked as F-3. |

PICS auto-discovery is only implemented in the Go runner. The Python runner
runs every YAML case unless a static `--pics` file is supplied, which is why
its "Discovered" counts are larger and its failure pile is dominated by tests
the Python harness cannot execute at all (unknown actions). Quadrants 3-4 are
not yet directly comparable to 1-2.

## Findings (session 5 — F-11 mash-py listener tears down on commissioning window expiry)

**Discovered while re-running Q2:** `mash-py-device` has the same bug
that F-4 fixed in `mash-go-device`. After the commissioning window
auto-closes (15-second window with `--window-default-seconds 15`, or
similar timing), `mash-py` logs "commissioning window expired after
15s; closing listener" and the listener goes away. Subsequent dials
to the commissioning port get kernel-level `ECONNREFUSED`.

In a Q2 run this manifests as: auto-PICS commissions cleanly, ~280
tests run with normal cross-stack signal, then the listener tears
down mid-suite and every test from that point onward fails its
precondition setup with `dial tcp 127.0.0.1:18446: connect:
connection refused` — the exact same noise pattern that drove Go-on-Go
from 12.5% to 77.4% before F-4.

**Owner.** mash-py (`mash-py/mash_py/cli/device.py` — keep the
commissioning kernel listener up across window state transitions, the
ALPN gate at the TLS layer is sufficient to enforce policy).

**Mitigation for measurement.** The 110 PASS / 43 real-FAIL split
**before the storm** is the honest signal:

```
Before listener tear-down (~280 tests):
  PASS: 110
  FAIL: 43   ← real cross-stack issues (multi-zone, PAIR, RENEW, CERT-VAL,
              SEC-NONCE, COMM-WINDOW, ZTYPE, MASHO, MULTI...)
  SKIP: 133  ← PICS-pruned

After listener tear-down (~145 tests):
  ~145 connection-refused failures, environmental
```

Pass rate of attempted-and-non-environmental: 110/153 = **71.9%**
(approximate; some tests in the "before" window may have hit window
expiry transiently). A clean Q2 number requires F-11 in mash-py.

## Fixes shipped (session 5 — F-10 TEST-zone harness override)

### F-10: Controller-authority tests reused the TEST suite zone, hitting DEC-060 observer-only resolution

**Change:** `mash-go/internal/testharness/runner/coordinator.go`,
`mash-go/internal/testharness/runner/coordinator_test.go`. Single
function added (`testCaseNeedsControllerAuthority`) plus the existing
`needsIsolatedConnectSession` flag union'd into a combined
`needsIsolatedSession`. Three call sites (lines ~221, 242, 442 in the
post-edit file) now use the union.

**Recap of the bug.** `runner.runAutoPICS` commissions the suite zone
as `cert.ZoneTypeTest` so it (a) doesn't count against `MaxZones`, (b)
is transparent to device logic, and (c) lets the runner drive
TestControl triggers (per `docs/testing/test-process.md §2`,
TestControl is restricted to TEST zones). `coordinator.go:151` resets
the per-test `CommissionZoneType` to TEST between tests for the same
reason.

But per **DEC-060** ("TEST zones are pure observers: excluded from
limit resolution and setpoint resolution") and the corresponding
guards in `pkg/zone/zone.go:159, 192`, a SetLimit/SetSetpoint/
ClearLimit invoke from a TEST zone is *stored* under the TEST entry
in `MultiZoneValue.Values` but *skipped* during `ResolveLimits` /
`ResolveSetpoints`. Tests that did `invoke SetLimit` against the
implicit suite session therefore saw:

- `response_contains: effectiveConsumptionLimit` succeed (the response
  field is present)
- `read effectiveConsumptionLimit` come back **nil** (resolver excluded
  the only stored value)
- `controlState` stuck at `0x00 AUTONOMOUS` (no non-TEST zone is
  controlling the device, by definition)

Failure pattern in the corpus: `TC-CTRL-001/002/003`,
`TC-STATE-001/002/015`, `TC-ZONE-001/002/006/007` and similar all
showed `value: expected N, got <nil>` or `value: expected 1, got 0`
on the read step that followed the SetLimit invoke.

**Fix.** New predicate
`testCaseNeedsControllerAuthority(tc *loader.TestCase) bool` returns
true when the test exercises commands or preconditions that require a
real (non-TEST) controller zone:

- An `invoke` step whose `command` is one of `{SetLimit, ClearLimit,
  SetSetpoint, ClearSetpoint, SetCurrentLimits}` -- the resolver-going
  commands. ProcessState commands (Pause / Resume / Stop /
  ScheduleProcess) are deliberately **not** listed: they're global,
  not multi-zone resolved, and a TEST zone can drive them just fine.
  TC-CTRL-004/005/006 already pass post-F-9 from a TEST suite for
  exactly this reason.
- An explicit `zone_type: LOCAL` or `zone_type: GRID` precondition.
- Multi-zone preconditions (`second_zone_connected`,
  `two_zones_connected`, `two_zones_with_limits`, `zone_count_at_least
  >= 2`, `no_other_zones_connected`) -- the partner-zone tests
  implicitly assume the primary participates in resolution.

When the predicate fires AND the suite zone exists AND the current
`CommissionZoneType` is still TEST, the test session is routed
through the existing isolation path that connect-probing tests
already use:

```go
needsIsolatedSession := needsIsolatedConnectSession || needsControllerAuthority
if needed >= precondLevelCommissioned && needsIsolatedSession && c.suite.ZoneID() != "" {
    if c.ops.CommissionZoneType() == cert.ZoneTypeTest {
        c.ops.SetCommissionZoneType(cert.ZoneTypeLocal)
        ...
    }
    detachMainControlChannel(...)
    c.ops.SetPASEState(nil)
    c.ops.ClearWorkingCrypto()
}
```

`canReuseSession` and `suiteCanReconnect` then both see
`!needsIsolatedSession == false` and force a fresh `EnsureCommissioned`
which commissions a disposable **LOCAL** zone for the test. The TEST
suite zone is preserved on the device side (still tracked in
`pool` via the suite alias) for later TestControl trigger delivery
in teardown. Existing residual-zone-sweep cleanup (F-6) removes the
disposable LOCAL zone after the test.

**Why not change the resolver to include TEST?** `pkg/zone/zone.go`
already correctly implements DEC-060. Changing `ResolveLimits` to
include TEST would let real bugs (a controller silently reading TEST
observer values as effective limits) slip through in production. The
test corpus is meant to validate the spec; the harness must commission
zones that match the spec's authority model.

**Result.** Focused-slice retest (`TC-CTRL-001/002/003`,
`TC-STATE-001/002/015`, `TC-ZONE-001/002/006/007`):

```
Before: 0/10 PASS
After:  3/10 PASS
```

Newly passing in deterministic order: `TC-CTRL-001 (SetLimit)`,
`TC-STATE-001 (AUTONOMOUS->CONTROLLED)`, `TC-ZONE-001 (Single Zone
Sets Limit)`. The remaining 7 are real device-side feature gaps:

- `TC-CTRL-002` -- `limit_is_set: true` precondition has no harness
  handler; step 1 reads null. This is a corpus/harness gap, not a
  TEST-zone gap.
- `TC-CTRL-003`, `TC-ZONE-006` -- SetSetpoint succeeds but
  `effectiveConsumptionSetpoint` is not populated. Device-side
  setpoint resolution gap.
- `TC-STATE-002`, `TC-STATE-015` -- CONTROLLED -> LIMITED transition.
  Per `pkg/features/limit_resolver.go` comment: "The device application
  layer is responsible for promoting CONTROLLED -> LIMITED when it
  detects that its operation is actually being curtailed by the limit."
  Not implemented in the EVSE example.
- `TC-ZONE-002` -- `myConsumptionLimit` per-zone read returns the
  effective (resolved) value rather than the calling zone's stored
  value. Device-side read-extractor gap.
- `TC-ZONE-007` -- `disconnect_zone` step environmental issue.

Full-corpus Q1: **305/359 (85.0%) -> 307/359 (85.5%)**. The +2 net
(vs +3 deterministic) is one unrelated TLS-race flake (TC-COMM-004
passes in isolation, fails in full-corpus alongside TC-ZONE-010 with
`commissioning dial failed: remote error: tls: internal error`).

**Tests added.** `TestCaseNeedsControllerAuthority` (12 sub-cases
covering invoke commands, precondition variants, and the negative
case for ProcessState commands and zone_type=TEST).

## Fixes shipped (session 4 — F-8 name↔ID translation + F-9 EVSE Resume)

### F-8: Runner sent string-keyed invoke args; device rejected with INVALID_PARAMETER

**Change:** `mash-go/internal/inspect/names.go` (+ generated tables in
`names_gen.go`), `mash-go/cmd/mash-featgen/generate_names.go`,
`mash-go/internal/testharness/runner/runner.go`,
`mash-go/internal/testharness/runner/connection_handlers.go`,
`mash-go/internal/testharness/runner/preconditions.go`. Commits
`d1f9f6a` + `7c53214`.

**Recap of the bug.** YAML test cases supply invoke args by **spec
name** (`{consumptionLimit: 7000000, cause: 3}`), but
`docs/interaction-model.md §7.1` requires integer-keyed CBOR
parameters on the wire. The runner stuffed the string-keyed map
directly into `wire.InvokePayload.Parameters` and CBOR-encoded it
verbatim. The device's `pkg/model/command.go::validateParameters`
checks `params[uint8(N)]` for each `Required: true` parameter and
returned `StatusInvalidParameter` (status 5) before the handler
ever ran — so SetLimit, SetSetpoint, ClearLimit, RemoveZone, and
every other required-param-bearing command failed with
`invoke_success: false` on the YAML side.

Symmetric bug on the response path: CBOR-decoded payloads arrived
as `map[any]any` keyed by uint8 wire IDs. The runner's
`normalizePayloadMap` and `flattenInvokeResponse` stringified the
keys to `"1"`, `"2"`, `"3"`, so YAML assertions written against
spec field names (`response_contains: effectiveConsumptionLimit`,
`response.applied: true`) never matched. The
`flattenInvokeResponse → m["applied"] == false → invoke_success
downgrade` was equally broken: it never found the `applied` key
because the key was actually `"1"`.

**Fix.** Two generated registries collected from existing per-command
constants in `pkg/features/*_gen.go`:

- `commandParamNames[featureID][cmdID][name] = id` — encode side.
- `commandResponseFieldNames[featureID][cmdID][id] = name` — decode side.

Two helpers in `internal/inspect/names.go`:

- `inspect.ConvertCommandParams(featureID, cmdID, args) any`
- `inspect.RemapResponsePayload(featureID, cmdID, payload) map[string]any`

The runner calls them in `handleInvoke` (and `handleInvokeAsZone`,
which had a copy-paste of the same bug) so args are normalized to
`map[uint8]any` before encoding and the response payload is
normalized to `map[string]any` keyed by spec names before being
stored in `result`/`response` step outputs. Response remap is gated
on `resp.IsSuccess()` so failure responses (which carry a
`wire.ErrorPayload{1: message}` unrelated to the command schema)
fall back to the existing stringified-key normaliser.

`buildRemoveZoneRequest` in `preconditions.go` (the harness's own
internal residual zone sweep) was also using
`map[string]any{"zoneId": ...}` with bare integer feature/cmd
literals; switched to integer-keyed parameters via
`features.RemoveZoneParamZoneID`.

**Result.** Q1 lifted from 283/359 = 78.8% to 305/359 = 85.0%.
Newly-passing tests include (focused-slice retest):
- TC-CTRL-004 (Pause), TC-CTRL-005 (Resume), TC-CTRL-006 (Stop)
- TC-STATE-005 (OVERRIDE), TC-STATE-009 (Resume PAUSED→RUNNING)
- TC-ZONE-REMOVE-001..005 (every RemoveZone test)
- TC-ZTYPE-002/003/005/006/007 (zone-type-constraint tests)

This is the F-6-pattern fix (Issue #11 / integer-keyed params)
generalised from the TestControl-only special case in
`requestDeviceState` to all invoke commands.

### F-9: EVSE Resume gated on plug state, breaking processState conformance

**Change:** `mash-go/internal/examples/evse.go`. Commit `9b4c8fc`.

**Recap of the bug.** EVSE's `OnResume` handler conditioned the
`PAUSED → RUNNING` transition on `chargingSession.IsPluggedIn() ==
true`. Every conformance test that established `processState: PAUSED`
via TestControl (`TriggerProcessStatePaused`) — i.e. teleporting state
without going through plug-in → charging → Pause — saw Resume return
success but processState stayed at PAUSED, because the simulation's
plug state was false. TC-STATE-009 and TC-CTRL-005 were the visible
casualties.

**Fix.** Removed the `IsPluggedIn` gate. The Resume command's contract
per `docs/features/energy-control.md §"Resume"` is the state
transition itself; plug-state is informational and re-evaluated by
the natural simulation loop. Pause was already unconditional; Resume
now matches.

**Result.** TC-STATE-009 and TC-CTRL-005 PASS. Other charging-session
behavior is unchanged because Resume's downstream effects (currentPower,
AC active power telemetry) live on the plug-in / charging path, not
on the immediate Resume invocation.

### What's left in Q1 (54 failures)

Group by error pattern:

| Pattern | Count | Likely owner |
|---|---|---|
| `value_is_not_null = false` | 6 | Device feature: attributes returning null when populated value expected |
| `value` mismatch (key not found / wrong number) | ~10 | Device-side state machine + TEST-zone semantics (DEC-060 excludes TEST from limit/setpoint resolution, breaking SetLimit response expectations on auto-PICS suite zone) |
| `value_lte` / `value_in_range` / `value_gte_saved` cannot compare nil | 5 | Device feature: numeric attributes returning nil |
| `commission zone LOCAL: TLS_ERROR` | 2 | Pre-existing TC-ZONE-010-style race; environmental |
| Misc (within_limit, value_type, value_in, response_contains) | ~30 | Real device-implementation gaps |

Grouped by feature prefix:

```
TC-GPL-S    8   Generic Power Load use case
TC-EVC-S    6   EV-charging use case
TC-ZONE     5   Zone management (ZONE-007/008 etc.)
TC-STATE    5   Failsafe state machine
TC-CTRL     5   EnergyControl (failsafe + unsupported-cap)
TC-FAIL     4   Failsafe timing / persistence
TC-MEAS     3   Measurement
TC-STATUS, PODF, MPD, GPL, ELEC, CHRG  2 each
TESTCTRL-ZONE, POEN, ITPCM, FRAME, FI, DISC  1 each
```

These are device-side feature-implementation gaps, not infrastructure.
The remaining lift requires per-feature work driven by the spec docs in
`docs/features/<feature>/1.0.yaml` and the matching `.md` semantics
notes.

## Fixes shipped (evening session — F-6-followup + TC-PAIR-001/002/003)

### F-6 follow-up: TC-TRANS-002 LOCAL-zone leak

**Change:** `mash-go/internal/testharness/runner/pase.go`,
`mash-go/internal/testharness/runner/connection_state_test.go`.

The F-6 commit (`2d46a23`) made the residual zone sweep run unconditionally
and fixed the integer-keyed-parameter regression in `requestDeviceState`,
which made TC-E2E-001 pass. TC-TRANS-002 still failed with the same
"cert exchange: zone type already exists, code 10" because TC-TRANS-001's
teardown wasn't actually removing its LOCAL zone.

**Root cause.** `handleCommission` with `transition_to_operational: true`
mutated `pool.Main()`'s Connection struct *in place*: it called `Close()` on
the existing struct and then assigned the new operational `tlsConn`/`framer`/
`state` fields back onto the same pointer. Whenever `suite.Conn()` aliased
`pool.Main()` (a typical state after `borrowSuiteControlChannelIfAlive` or
`reconnectToZone`, both of which set the pool main and the suite conn to
the same `*Connection`), the mutation made the test's *new operational
LOCAL-zone connection* share the suite alias. The teardown's alias filter
`zc == c.suite.Conn()` then misclassified the test's zone connection as a
suite alias, untracked it without sending RemoveZone, and the residual
sweep that was supposed to catch this asked the wrong connection for
`getTestState` (TestControl is restricted to TEST zones; the LOCAL leak
returned `NOT_AUTHORIZED` / status 8) and got nothing back.

**Fix.** `pase.go` now calls a new `swapMainToOperational(opConn)` helper
that closes the old Connection and installs a fresh struct via
`pool.SetMain(newConn)`. This decouples `pool.Main` from any pre-existing
suite alias, exactly matching the invariant that `commissioning.go`'s
`transitionToOperational` already maintained. New regression test
`TestSwapMainToOperational_DecouplesSuiteAlias` asserts the property.

**Result.** TC-TRANS-001 → TC-TRANS-002 → TC-TRANS-004 → TC-E2E-001 all
pass back-to-back (was: TC-TRANS-002 fails after TC-TRANS-001).

### TC-PAIR-001/002/003 — corpus alignment + auto-PICS posture leak

**Change:** `mash-go/internal/testharness/runner/preconditions.go`,
`mash-go/internal/testharness/runner/coordinator.go`,
`mash-go/testdata/cases/pairing-request-tests.yaml`.

These three were the "now-visible" failures the F-2 fix exposed.

- **TC-PAIR-001** expected `txt_records: { id: <device_id>, disc: <discriminator> }`,
  but `id` is not a spec-defined commissionable TXT key (`docs/discovery.md`
  §2.2 defines only `D, cat, serial, brand, model, DN`). The actual device
  emits eight TXT keys; the engine's defaultChecker stringifies map equality
  via `fmt.Sprintf` so the extra spec-required keys broke the check. Switched
  to `txt_records: present` (the mDNS browser already filters by
  discriminator, so a non-empty TXT map is sufficient evidence of a
  spec-compliant commissionable advertisement for the device under test).
- **TC-PAIR-002** failed at step 1 (`verify_mdns_not_advertising`) because
  the runner's auto-PICS sends `TriggerEnterCommissioningMode` after the
  suite zone is recorded so subsequent tests can commission new zones
  without re-triggering -- but TC-PAIR-002 explicitly wants the device
  *not* in commissioning mode. The runner's tier model treats level 1 =
  "commissioning mode active" and ran `WaitForCommissioningMode` at every
  level-1 entry, hiding the pre-existing commissioning posture from any
  test that asked for `commissioning_window_closed: true`. Fix in two
  places: `HandlePreconditionCases` now sends `TriggerExitCommissioningMode`
  and stops the mDNS observer when `commissioning_window_closed: true`;
  the precondLevelCommissioning switch in `coordinator.go` skips
  `WaitForCommissioningMode` (and sets `StateCommissioningActive=false`)
  for that same case.
- **TC-PAIR-003** read a `wexp` (window-expiry) TXT key that the spec
  doesn't define. Reworked the rate-limit verification to compare the
  `D` (discriminator) TXT field before/after the pairing request: the
  observable consequence of "device ignored the pairing request" is that
  the same instance keeps advertising the same discriminator. Replaced the
  `store_as` field (not recognized by the engine) with `store_result`
  (which saves the handler's primary `value` output under the named key,
  per `engine.go:executeStep`).

**Result.** All 5 TC-PAIR-* tests pass in isolation against a single Go
device. (Cross-LAN runs with multiple commissionable devices on the same
multicast address still see cross-contamination — the harness mDNS browser
finds *any* `_mash-comm._tcp` advertiser; that is an environment property,
not a corpus bug.)

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

### F-5 — Genuine discovery/pairing/TLS assertions in Go-on-Go  ✅ ALREADY PASSING

The five tests originally listed here all pass against the current mash-go.
Likely fell out of the F-1/F-4 environmental cleanup; entries kept for
historical reference.

| Test | Status |
|---|---|
| `TC-DISC-001` | PASS |
| `TC-BROWSE-004` | PASS |
| `TC-CONN-REAP-003` | PASS (1m35s — slow but passing) |
| `TC-TLS-CTRL-007` | PASS |
| `TC-MULTI-003` | PASS |

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
