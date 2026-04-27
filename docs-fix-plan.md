# MASH Documentation Fix Plan

Concrete remediation for the issues surfaced by the 2026-04-25 doc audit.
Items are grouped HIGH / MEDIUM / LOW. Within each group, items appear in
the order a single person should execute them: source-of-truth (YAML)
fixes first, then generated-doc effects, then hand-written prose, then
nav and home-page polish.

After each item: re-run `cd mash-go && make docs` and `cd .. && mkdocs build --strict`
(both must remain clean). Some items require a decision before
mechanical work can start — those are flagged **DECISION REQUIRED**.

---

## How to read each entry

- **Where:** file paths and line ranges, against the current tree.
- **Problem:** what the audit found (verified against source).
- **Fix:** the concrete change to make.
- **Verify:** how to confirm the fix landed.

---

## HIGH — must fix before shipping; will mislead implementers

### H1. EnergyControl `optionalProcess`: hand-written contradicts YAML

**Where:**
- Source of truth: `docs/features/energy-control/1.0.yaml:391-450` (id 81 is `bool`; ids 82-87 are flat siblings: `minRunDuration`, `minPauseDuration`, `maxRunDuration`, `maxPauseDuration`, `optionalProcessPower`, `controlMode`).
- Generated: `docs/generated/features/energy-control.md:51-87` (correct, mirrors YAML).
- Hand-written: `docs/features/energy-control.md:91` declares `81: optionalProcess` typed as `OptionalProcess?`; lines 293-321 spell out a 13-field nested CBOR struct (`processId`, `description`, `powerEstimate`, `powerMin`, `powerMax`, `estimatedDuration`, `minRunDuration`, `minPauseDuration`, `isPausable`, `isStoppable`, `energyEstimate`, `resumeEnergyPenalty`, `scheduledStart`).

**Problem:** Hand-written treats `optionalProcess` as a struct; YAML and generated docs treat it as a boolean flag with separate flat attributes. An implementer following the hand-written tree would build the wrong wire format.

**Fix:**
1. In `docs/features/energy-control.md:91`, change the line to:
   `81: optionalProcess,            // bool: true if a process is currently scheduled or running`
2. Replace the `### OptionalProcess (requires PROCESS feature flag)` block (lines ~285-322) with a flat-attribute table that lists ids 81-87 and points to the generated table:
   - `81 optionalProcess (bool)`
   - `82 minRunDuration (uint32 s)`
   - `83 minPauseDuration (uint32 s)`
   - `84 maxRunDuration (uint32 s)`
   - `85 maxPauseDuration (uint32 s)`
   - `86 optionalProcessPower (int64 mW)`
   - `87 controlMode (ControlModeEnum)`
3. Audit any reference elsewhere in the same file to fields that no longer exist (`processId`, `description`, `isPausable`, `isStoppable`, `energyEstimate`, `resumeEnergyPenalty`, `scheduledStart`, `estimatedDuration`, `powerMin`/`powerMax`, `powerEstimate`). If any remain in prose, the prose is referencing a removed structure.
4. Confirm the matching generated page renders the same set after the next `make docs`.

**Verify:** `grep -n "OptionalProcess\b\|processId\|isPausable" docs/features/energy-control.md` should return zero matches; `make docs && mkdocs build --strict` clean.

---

### H2. ChargingSession three-way attribute drift — YAML is the lagging side

**Where:**
- YAML: `docs/features/charging-session/1.0.yaml` — attribute ids: `1, 3, 4, 10, 11, 20, 30, 31, 32, 33, 40, 41, 42, 43, 44, 50, 51, 52, 60, 61, 62, 70, 71, 72, 80, 81`. Note: **no id 2**, but the rest of the spec ecosystem treats id 2 as a mandatory attribute named `sessionId`.
- Generated: `docs/generated/features/charging-session.md:17-42` — mirrors YAML (so also missing id 2).
- Hand-written: `docs/features/charging-session.md:38` has `2: sessionId, // uint32: unique session identifier`; is **missing** id 32 (`evMinStateOfCharge`) and id 33 (`evTargetStateOfCharge`).
- Conformance: `docs/conformance/README.md:185` — `| 2 | sessionId | M |` (Mandatory).
- PICS: `docs/testing/pics-format.md:283, 492, 583` — code `MASH.S.E01.CHRG.A02` is `sessionId | M`.
- Behavior spec: `docs/testing/behavior/chargingsession-feature.md:295` (attribute table) and `:381` (`TC-CHRG-002: StartSession sets ID and time`).
- Decision log: `docs/decision-log.md:1504-1506` — three EEBUS use-case mapping entries reference `sessionId`.

**Problem:** Conformance, PICS, behavior tests, decision log, and the hand-written prose all treat `sessionId` as mandatory at id 2. The YAML alone omits it — the attribute id list jumps 1 → 3. This is almost certainly an accidental drop during YAML editing/regeneration; the YAML is the lagging side, not the hand-written doc.

**Fix:**
1. Add `sessionId` to the YAML at `docs/features/charging-session/1.0.yaml`, inserted between ids 1 and 3 (around line 60):
   ```yaml
   - id: 2
     name: sessionId
     type: uint32
     access: readOnly
     mandatory: true
     description: Unique session identifier, monotonically increasing per device
   ```
   Match the schema/style of the surrounding entries — copy structure from id 1 or id 3 verbatim.
2. Add `evMinStateOfCharge` and `evTargetStateOfCharge` rows to the hand-written attribute table at `docs/features/charging-session.md` after the existing id 31 line (around line 51), so all three sources match:
   ```
     32: evMinStateOfCharge,       // uint8 0..100 %?: floor SoC for must-charge
     33: evTargetStateOfCharge,    // uint8 0..100 %?: user-defined target SoC
   ```
3. Regenerate: `cd mash-go && make generate && go build ./... && go test ./...`. The generated charging-session page at `docs/generated/features/charging-session.md` should then list id 2.
4. Spot-check that the Go code generator emits a `SessionId` accessor and that nothing else in `mash-go/` was relying on the absence of id 2.

**Verify:** All seven sources (YAML, generated md, hand-written md, conformance, PICS, behavior spec, decision log) agree that id 2 is `sessionId`, mandatory. `grep -rn "sessionId" docs/ mash-go/pkg/usecase/` should show consistent typing everywhere; `mkdocs build --strict` clean.

---

### H3. Zone-count contradiction across multi-zone / security / transport

**Where:**
- `docs/multi-zone.md:36` — "Maximum 5 zones per device".
- `docs/multi-zone.md:65` — "Max zones per type: 1 (DEC-043)".
- `docs/multi-zone.md:246` — "max_zones (default: 5)".
- `docs/security.md:480, 482, 484-485` — uses `max_zones` symbolically; example sets `max_zones = 2`.
- `docs/transport.md:165, 167, 169, 171, 202, 207` — uses `max_zones` symbolically; example uses `max_zones=2`.
- `docs/decision-log.md:2455` (DEC-043) — supersedes the original 5-zones design with one-zone-per-type.

**Problem:** The original "5 zones" wording was never deleted after DEC-043. Three zone types (GRID, LOCAL, TEST) with at most one zone each yield a hard ceiling of 3, not 5. The constant `max_zones` is also referenced 12+ times without a single defining sentence.

**Fix:**
1. Add a "Zone limits" subsection at the top of `docs/multi-zone.md` (just after `## 1.` introduction, around line 30) that defines:
   - Zone types: GRID, LOCAL, TEST (priority order).
   - Constraint: at most one zone per type (DEC-043).
   - Therefore `max_zones` is a device-configured value with hard ceiling 3.
   - Typical home device: `max_zones = 2` (one GRID + one LOCAL).
   - TEST zone is mutually exclusive with operational zones during conformance testing (cite DEC-060).
2. Delete or rewrite `docs/multi-zone.md:36` ("Maximum 5 zones per device") to reference the new subsection.
3. In `docs/multi-zone.md:246`, change `max_zones (default: 5)` to `max_zones (typical: 2, max: 3)` and link to the new subsection.
4. In `docs/security.md:480-482` and `docs/transport.md:165-167`, replace the inline phrase "max_zones" with a link to the new subsection on first occurrence in each file.

**Verify:** `grep -nE "5 zones|default: 5|max.{0,3}zones.{0,3}5" docs/` returns zero matches.

---

### H4. `features/README.md:146` claims signals/tariff/plan are not YAML-defined

**Where:** `docs/features/README.md:146-148`.

**Problem:** The directory diagram still annotates `signals.md`, `tariff.md`, `plan.md` with `# Not yet YAML-defined`, but YAML now exists at `docs/features/{signals,tariff,plan}/1.0.yaml` and generated pages exist at `docs/generated/features/`.

**Fix:** Remove the three "Not yet YAML-defined" comments. If the directory tree is meant to show structure, replace with the same `name/1.0.yaml` + `name.md` pattern used for the YAML-defined features above (lines 138-145).

**Verify:** `grep -n "Not yet YAML" docs/features/README.md` returns zero matches.

---

### H5. Twelve behavior specs orphaned from nav

**Where:** `mkdocs.yml:113-122` lists 9 behavior specs. `docs/testing/behavior/` has 21 files. Missing from nav:

- `chargingsession-feature.md`
- `connection-cap.md`
- `connection-establishment.md`
- `duration-semantics.md`
- `electrical-feature.md`
- `energycontrol-feature.md`
- `measurement-feature.md`
- `message-framing.md`
- `status-feature.md`
- `tls-profile.md`
- `zone-lifecycle.md`
- `zone-management.md`

**Problem:** `docs/testing/README.md` lists all of these as "Complete" but they are unreachable from the rendered site nav. An external integrator looking up "how does my Electrical feature need to behave?" cannot find `electrical-feature.md`.

**Fix:** Extend `mkdocs.yml` "Behavior Specs" section (around lines 113-122). Group by topic for readability:

```yaml
- Behavior Specs:
    - Protocol Behaviors: testing/behavior/protocol-behaviors.md
    - State Machines: testing/behavior/state-machines.md
    - Connection:
        - State Machine: testing/behavior/connection-state-machine.md
        - Establishment: testing/behavior/connection-establishment.md
        - Connection Cap: testing/behavior/connection-cap.md
        - Message Framing: testing/behavior/message-framing.md
        - TLS Profile: testing/behavior/tls-profile.md
    - Commissioning: testing/behavior/commissioning-pase.md
    - Discovery: testing/behavior/discovery.md
    - Subscriptions: testing/behavior/subscription-semantics.md
    - Zones:
        - Lifecycle: testing/behavior/zone-lifecycle.md
        - Management: testing/behavior/zone-management.md
        - Multi-Zone Resolution: testing/behavior/multi-zone-resolution.md
    - Failsafe Timing: testing/behavior/failsafe-timing.md
    - Feature Behaviors:
        - Electrical: testing/behavior/electrical-feature.md
        - Measurement: testing/behavior/measurement-feature.md
        - EnergyControl: testing/behavior/energycontrol-feature.md
        - ChargingSession: testing/behavior/chargingsession-feature.md
        - Status: testing/behavior/status-feature.md
    - Feature Interactions: testing/behavior/feature-interactions.md
    - Duration Semantics: testing/behavior/duration-semantics.md
```

**Verify:** `mkdocs build --strict` then `find site/testing/behavior -maxdepth 1 -type d` shows all 21 entries.

---

### H6. `index.md` is not a usable home page

**Where:** `docs/index.md` (15 lines).

**Problem:** A developer landing on the home page gets four bullets and no orientation. There is no answer to "what is this", "who should read what", or "how do I get started".

**Fix:** Rewrite `index.md` to about 60-90 lines covering:

1. **What is MASH** (3-4 lines) — keep current opening but expand: lightweight Matter-inspired protocol, replacement for EEBUS SHIP/SPINE in energy management, runs on 256 KB MCUs, 4 operations (Read/Write/Subscribe/Invoke), TLS 1.3 transport.
2. **Quick Start** (5 short steps) — install the Go reference; pair a controller with a device; read an attribute; set a limit; subscribe to changes. Each step is one sentence with a link to the relevant doc.
3. **Reading paths by role:**
   - "I'm implementing a device" → Protocol Overview, Transport, Discovery, Security, then Endpoint Types and Features.
   - "I'm implementing a controller" → Protocol Overview, Interaction Model, Multi-Zone, Use Cases.
   - "I'm writing a conformance test" → Testing Overview, PICS Format, Behavior Specs.
   - "I'm reviewing the spec" → Spec Conventions, Decision Log, Glossary.
4. **Where things live** (3-4 lines) — `docs/` is normative spec; `mash-go/` is reference implementation; YAML in `docs/features/*/1.0.yaml` drives both code generation and the "Features" section of these docs.
5. Keep the existing version + counts footer line.

**Verify:** Re-read on the rendered site (`mkdocs serve`); a developer who has never seen MASH should know within 60 seconds what to read first.

---

### H7. End-to-end interaction example missing

**Where:** New file `docs/example-walkthrough.md`; link from `docs/index.md` Quick Start, from `docs/protocol-overview.md`, and from `docs/interaction-model.md`. Add nav entry under "Protocol".

**Problem:** `interaction-model.md` documents Read/Write/Subscribe/Invoke individually, but the docs never connect them. This is the single largest onboarding gap surfaced in the audit.

**Fix:** Author a walkthrough that follows one realistic flow end to end, using the reference implementation. Suggested structure (~200-400 lines):

1. **Scenario:** A home EMS (controller) is paired with an EVSE (device). The controller wants to read static capabilities, set a charging limit, subscribe to effective limits, and react to a multi-zone override from the GRID zone.
2. **Step 1 — Pairing.** Show the discovery + PASE handshake at the message level, with links to `discovery.md` / `security.md`.
3. **Step 2 — Read attributeList.** Show the Read message, the response, and explain what the controller does with it.
4. **Step 3 — SetLimit Invoke.** Show the request, the response, and the state transition (`ControlStateEnum: AUTONOMOUS → CONTROLLED`).
5. **Step 4 — Subscribe to effectiveConsumptionLimit.** Show the priming report and a delta notification.
6. **Step 5 — Multi-zone override.** GRID zone sends a tighter limit; show how `effectiveConsumptionLimit` resolves and what the LOCAL controller sees through its subscription.
7. **Step 6 — Failsafe.** Pull the GRID zone offline; show the failsafe transition and `OperatingStateEnum` change.

Use real CBOR field numbers from `docs/message-types.md`. Keep code blocks as commented CBOR pseudo-syntax (matches existing style in `interaction-model.md`).

**Verify:** A reader going from `index.md` → Quick Start → Walkthrough should be able to predict the wire bytes for any of the four operations without flipping to other pages.

---

### H8. `EndpointInfo.featureMap` field 5 contradicts DEC-074

**Where:**
- Spec shape: `docs/features/device-info.md:68-76` — `EndpointInfo` declares `5: featureMap, // uint32: feature flags bitmap`.
- Spec example: `docs/features/device-info.md:97-119` — example endpoint records carry `featureMap: 0x0001` / `0x0009`.
- Decision: `docs/decision-log.md` DEC-074 (commit `2ccc270`, 2026-04-23) — `featureMap` bits encode capability variants *within* a feature, not feature presence; cross-feature aggregation has no consistent semantics; mDNS TXT FM key dropped for the same reason.
- Reference impl: `mash-go/pkg/model/endpoint.go:151` — `EndpointInfo` struct emits 4 fields only (no field 5). DEC-074-aligned, predates the DEC.
- mash-py runtime: `mash-py/mash_py/model/endpoint.py` — Phase 13 deliberately exposes per-feature `Feature.feature_map` but no `Endpoint.feature_map` aggregator (DEC-074-aligned).
- Tracker: `mash-py-spec-issues.md` Issue #12 (filed 2026-04-27).

**Problem:** Field 5 is the same kind of cross-feature aggregation that DEC-074 explicitly removed at the mDNS TXT layer ("a single device-level 'FM' hex number is lossy"). Endpoint-level aggregation has no DEC-074-consistent meaning: bits are scoped per-feature, so OR-ing across features mixes orthogonal namespaces. The `featureMap: 0x0009` numbers in the example look hand-picked rather than systematically computed. Both reference implementations (Go and Python) already omit the field; only the spec text still mandates it.

**Fix:**
1. In `docs/features/device-info.md`, drop the `5: featureMap, // uint32: feature flags bitmap` line from the `EndpointInfo` shape (around line 74). Result is a four-field shape: `{1: id, 2: type, 3: label, 4: features}`.
2. In the example endpoint records (around lines 99-117), delete the `featureMap: 0x0001` / `featureMap: 0x0009` lines and any trailing comma fix-ups.
3. Add a one-line note immediately below the (now four-field) `EndpointInfo` shape:
   > Per-feature capability variants are discovered by reading global attribute `featureMap` (0xFFFC) on each feature; see DEC-074.
4. In `docs/decision-log.md`, append to DEC-074's "Implementation notes" bullet list:
   > - Removed: `EndpointInfo` field 5 (`featureMap: uint32`) from `docs/features/device-info.md` — the same cross-feature aggregation rejected for mDNS TXT has no per-endpoint definition either.
5. Mark Issue #12 in `mash-py-spec-issues.md` as Resolved with the spec PR commit hash.

**No code changes** in `mash-go/` or `mash-py/`. Both implementations are already correct for the four-field shape.

**Verify:**
- `grep -n "featureMap" docs/features/device-info.md` returns only matches related to the per-feature global attribute (0xFFFC), not endpoint-level field 5.
- `mkdocs build --strict` clean.
- `mash-py-spec-issues.md` Issue #12 status flipped to ✅ Resolved.

**Coordination:** Resolve **before H9** so the H9 example rewrite doesn't carry the now-removed field 5 forward.

---

### H9. `device-info.md` example uses wrong feature IDs and inline registry table drifts

**Where:**
- Spec example: `docs/features/device-info.md:97-119` — endpoint records use 16-bit IDs (`features: [0x0001, 0x0002, 0x0003, 0x0005, 0x0007]`) and treat `DeviceInfo = 0x0006`, `Electrical = 0x0001`, `Status = 0x0005`, `ChargingSession = 0x0007`.
- Spec inline registry: `docs/features/device-info.md:124-137` — restates the same wrong mapping in a markdown table.
- Canonical registry: `docs/features/protocol-versions.yaml` — assigns `DeviceInfo = 0x01`, `Status = 0x02`, `Electrical = 0x03`, `Measurement = 0x04`, `EnergyControl = 0x05`, `ChargingSession = 0x06`, `Tariff = 0x07`, `Signals = 0x08`, `Plan = 0x09`, `TestControl = 0x0A`.
- Codegen consumers: `mash-go/cmd/mash-featgen/`, `mash-py/codegen/` — both read the YAML; neither reads the prose. Both impls are registry-faithful as of Phase 12.
- Tracker: `mash-py-spec-issues.md` Issue #13 (filed 2026-04-27).

**Problem:** The prose example and inline registry table contradict the YAML registry on **all seven mappings** (DeviceInfo, Electrical, Status, ChargingSession, etc.). Documentation-only drift — no implementation reads the prose — but a new-language porter who reads the prose first will lose ~30 minutes reconciling and may file a spurious bug. Commit `58e9a4b` (2026-04-25, "docs+docgen: reconcile YAML drift") swept several similar drifts but missed this one.

**Fix:**
1. After **H8** has dropped field 5, rewrite the example endpoint records in `docs/features/device-info.md` (around lines 97-119) using the actual registry IDs:
   ```
   endpoints: [
     { id: 0, type: DEVICE_ROOT, features: [0x01] },
     { id: 1, type: EV_CHARGER, label: "Port 1",
       features: [0x02, 0x03, 0x04, 0x05, 0x06] },
     { id: 2, type: EV_CHARGER, label: "Port 2",
       features: [0x02, 0x03, 0x04, 0x05, 0x06] }
   ]
   ```
   (Status / Electrical / Measurement / EnergyControl / ChargingSession on the EV_CHARGER endpoints, DeviceInfo on DEVICE_ROOT.)
2. Replace the inline "Feature ID Registry" table at lines 124-137 with one of:
   - **Preferred:** auto-generated from `protocol-versions.yaml` via `mash-docgen` (same pattern commit `58e9a4b` used for other tables); or
   - **Minimal:** a one-line pointer — `See [protocol-versions.yaml](protocol-versions.yaml) for the canonical Feature ID registry.` — and delete the prose table.
3. Add a one-line note to `docs/features/README.md` (in the same area commit `58e9a4b` added the normative-reference banner) confirming that `protocol-versions.yaml` is canonical and prose tables are illustrative only.
4. Mark Issue #13 in `mash-py-spec-issues.md` as Resolved with the spec PR commit hash.

**No code changes** in `mash-go/` or `mash-py/`. Both impls already consume the YAML registry.

**Verify:**
- `grep -nE '0x000[0-9A-F]' docs/features/device-info.md` returns zero matches in the example (only short-form `0x01`-`0x0A`).
- The example feature IDs match what `protocol-versions.yaml` declares; spot-check by feature: `grep -A1 "DEVICE_INFO\|ELECTRICAL\|STATUS\|MEASUREMENT\|ENERGY_CONTROL\|CHARGING_SESSION" docs/features/protocol-versions.yaml`.
- If the auto-generation path is taken: `cd mash-go && make docs` regenerates the table cleanly; running it twice is a no-op.
- `mkdocs build --strict` clean.
- `mash-py-spec-issues.md` Issue #13 status flipped to ✅ Resolved.

**Coordination:** Do **after H8** — H8 removes field 5 from the example, then H9 corrects the surviving fields. Doing H9 first means redoing the example.

---

## MEDIUM — causes confusion, content hidden from readers

### M1. One-way linking between hand-written and generated feature trees

**Where:** All `docs/features/*.md` (the "Feature Design" nav section) and `docs/generated/features/*.md` (the "Features" nav section).

**Problem:** Generated pages already link back to hand-written ones (e.g., `docs/generated/features/energy-control.md:11` links to `../../features/energy-control.md`). Hand-written pages do not link forward to the generated normative tables. A reader on `docs/features/energy-control.md` has no signal that the generated tree is the source of truth.

**Fix:**
1. Add a banner at the top of each hand-written feature page (insert after the H1 title), e.g.:

   ```markdown
   > **Normative reference:** [Generated table](../generated/features/energy-control.md). This page is the design rationale; the generated page is the source of truth for attribute IDs, types, and access modes.
   ```

   Apply to: `device-info.md`, `status.md`, `electrical.md`, `measurement.md`, `energy-control.md`, `charging-session.md`, `tariff.md`, `signals.md`, `plan.md`.

2. Add a one-paragraph note at the top of `docs/features/README.md` (after the H1) describing the two-tree split and which is normative.

3. Optional: add an introductory `index.md` page for each of the two nav sections (mkdocs supports `navigation.indexes`, already enabled in `mkdocs.yml:27`) that explains the section's purpose.

**Verify:** Pick three random feature pages on the rendered site; both directions of navigation should be obvious within one click.

---

### M2. Generated feature pages lack design context

**Where:** `docs/generated/features/*.md`.

**Problem:** Generated pages are pure schema (attribute / enum / command tables + state diagrams). Multi-zone resolution semantics, ControlStateEnum design intent, ProcessState opt-in (DEC-075), and unified control-state priority all live only in the hand-written tree. A reader who treats "Features" as authoritative will miss the rationale.

**Fix:** Two viable approaches; pick one project-wide.

- **Option A — Augment the generator.** Extend `mash-go/cmd/mash-docgen` so each feature YAML gains optional fields like `designNotes:` (free-form markdown blocks) that the generator embeds into the rendered page. Migrate prose from the hand-written tree into YAML. Eventually retire the hand-written tree.
- **Option B — Inline cross-link.** In each generated feature page, add a "Design context" section at the bottom that links to the relevant H2 anchors of the hand-written sibling. Cheaper; keeps the trees parallel; requires the generator to emit the link from a YAML metadata field (e.g. `designDoc: features/energy-control.md`).

**Recommended:** Option B as a first pass (small generator change), then evaluate Option A once the dust settles. Option A is the long-term direction.

**Verify:** Reading any generated feature page surfaces a path to its design rationale without the reader needing to know about the parallel tree.

---

### M3. `protocol-overview.md:120` broken in-page anchor

**Where:** `docs/protocol-overview.md:120`.

**Problem:** `> Detailed flows: [Security & Commissioning](#8-security-commissioning)` points to `#8-security-commissioning` in the same file. There is no §8 in `protocol-overview.md`. The intended target lives in `security.md`. mkdocs `--strict` does not validate same-page anchors, so this slipped through.

**Fix:** Change the link to `[Security & Commissioning](security.md)` (or to the specific anchor if `security.md` has a §8). Spot-check the rest of `protocol-overview.md` for similar in-page anchors that point at sections that don't exist locally.

**Verify:** `grep -n "](#" docs/protocol-overview.md` and click each on the rendered site.

---

### M4. Ship-quality docs orphaned from nav

**Where:** `mkdocs.yml`. Files exist on disk but unlinked:
- `docs/device-attestation-considerations.md`
- `docs/conformance/README.md`
- `docs/testing/test-process.md`
- `docs/testing/pics/pairing-connection-registry.md`
- `docs/usecases/README.md`

**Fix:**
1. Add `device-attestation-considerations.md` under "Reference" in `mkdocs.yml` (after `security-considerations.md`).
2. Add `conformance/README.md` under "Testing" with title "Conformance Rules".
3. Add `testing/test-process.md` under "Testing" with title "Test Process".
4. Add `testing/pics/pairing-connection-registry.md` under "Testing" with title "PICS — Pairing & Connection".
5. Add `usecases/README.md` under "Use Cases" (it currently only shows `generated/usecases/index.md`); make it the section index.

**Verify:** `mkdocs build --strict` shows zero "exists in docs but not in nav" warnings for these five files.

---

### M5. DEC-XXX inline references lack one-line summaries; decision-log has no TOC

**Where:** Throughout `docs/`. `decision-log.md` has 81 entries with no index.

**Problem:** "per DEC-077" forces a context switch every time. The log is searchable but not browsable.

**Fix:**
1. Add a table of contents to the top of `docs/decision-log.md`. Auto-generate from the `### DEC-XXX:` headings (or use the existing `toc:` markdown extension's permalinks plus a hand-written summary index grouped by topic — protocol fundamentals, transport, security, multi-zone, testing, etc.).
2. Add a `**DEC-NNN**` glossary entry to `docs/glossary.md` defining the convention and pointing to `decision-log.md`.
3. For the ~10 most-referenced DECs (DEC-026, DEC-043, DEC-047, DEC-051, DEC-060, DEC-069, DEC-070, DEC-072, DEC-075, DEC-077), do a quick prose pass on the docs that cite them and ensure the citation includes a parenthetical gist on first occurrence per page (e.g. "DEC-069 (max message size: 8 KB)").

**Verify:** Reading any normative doc, a DEC reference is either self-explanatory or the reader can scan the decision-log TOC to find its summary.

---

### M6. CBOR key disambiguation across MsgType namespaces underexplained

**Where:** `docs/message-types.md:16` (the "namespaces do not collide" claim).

**Problem:** `message-types.md` claims the three MsgType namespaces (Operational, Commissioning, Renewal) "do not collide" but never says how a decoder routes between them. The answer (ALPN selector + connection plane) is implicit elsewhere.

**Fix:** Expand the paragraph at `message-types.md:16` to one short subsection: "Disambiguation": ALPN `mash-comm/1` selects the commissioning namespace; `mash/1` selects operational; renewal messages travel inside an operational session and are tagged by their MsgType range. Cross-link to `transport.md` (ALPN section) and `security.md` (renewal section).

**Verify:** A reader writing a CBOR decoder can answer "given a frame on this connection, which MsgType table do I look up?" without leaving `message-types.md`.

---

## LOW — polish

### L1. Internal/dev artifacts mixed with shipped docs

**Where:**
- `docs/design/capability-snapshots-tdd-plan.md`
- `docs/design/runner-lifecycle-tdd-refactor-plan.md`
- `docs/research/*.md` (8 files)

**Fix:**
1. Move TDD plans out of `docs/`. Either delete (planning artifacts that have served their purpose) or move under `docs/internal/` and exclude that subtree via `mkdocs.yml exclude_docs:`.
2. `research/` is already excluded from nav; leave it on disk for audit-trail value, but add a one-line `docs/research/README.md` explaining "pre-design notes preserved for context; not part of the spec." That way the directory isn't a mystery if someone discovers it via Google.

**Verify:** `find docs -name "*tdd*"` returns nothing under shipped paths.

---

### L2. Failsafe duration units never stated

**Where:** `docs/multi-zone.md:266` references "2-24 hours".

**Fix:** Change to "`failsafeDuration` (uint32 seconds, valid range 7200-86400)" and add the unit to the corresponding generated attribute description in YAML if the YAML field lacks it (`docs/features/energy-control/1.0.yaml`, search for `failsafeDuration`).

**Verify:** `grep -nE "failsafeDuration" docs/` shows units consistent across all hits.

---

### L3. Endpoint-type enum casing inconsistent

**Where:**
- `docs/protocol-overview.md` uses `DEVICE_ROOT`, `GRID_CONNECTION` (SCREAMING_SNAKE_CASE).
- `docs/stack-architecture.md` uses `DeviceRoot`, `GridConnection` (PascalCase).

**Fix:** Pick the wire-format casing as canonical. Per current YAML and generated docs, this is SCREAMING_SNAKE_CASE. Add a one-line note to `docs/spec-conventions.md`: "Endpoint types are written in SCREAMING_SNAKE_CASE in protocol prose and YAML; language bindings may use PascalCase or camelCase per language conventions." Then global-replace the PascalCase occurrences in `stack-architecture.md` (or leave them if they refer to Go type names — in which case wrap them in backticks to make the distinction explicit).

**Verify:** `grep -nE "DeviceRoot|GridConnection" docs/` shows only backticked code references.

---

### L4. `index.md` link rot from H6 fix

**Where:** After H6 lands, the existing four bullets in `index.md` may end up duplicated in the new "Reading paths by role" section. Make sure to remove or restructure them rather than carrying them in two places.

**Verify:** Each link in `index.md` has exactly one referent.

---

## Execution checklist

Recommended sequence (smallest blast radius first):

1. **YAML pass.** Apply H2 (add `sessionId` to charging-session YAML). H1 and H4 are prose-only — no YAML change. Run `cd mash-go && make generate && go build ./... && go test ./...`.
2. **Generated regen.** `cd mash-go && make docs` so generated pages reflect the new YAML.
3. **Hand-written prose pass.** H1 prose, H2 prose (add ids 32/33 to hand-written), H3, H4, M1, M3, M5, M6, L2, L3, L4.
4. **Phase 13 spec sweep (DEC-074 alignment).** **H8 first**, then **H9** — H8 removes `EndpointInfo` field 5 from the spec, H9 then corrects the example feature IDs. Order matters: doing H9 first means redoing the example after H8 lands. Both close issues filed in `mash-py-spec-issues.md` (#12, #13). Pure spec edits — no code changes in `mash-py/` or `mash-go/`.
5. **Nav pass.** H5, M4, L1.
6. **New content.** H6 (rewrite `index.md`), H7 (author `example-walkthrough.md`), M2 (decide Option A vs B; if B, ship the small generator change).
7. **Final verification.** `cd .. && mkdocs build --strict` clean. Click through the rendered site as a first-time reader following the role-based paths from the new home page; note anything that still feels rough and open follow-up items.

After step 7, this plan can be archived or deleted.
