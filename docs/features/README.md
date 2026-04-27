# MASH Feature Definitions

> Feature specifications for the MASH protocol

**Status:** Draft
**Last Updated:** 2025-01-25

!!! info "Two doc trees, one source of truth"
    Each MASH feature has two pages:

    - The **Features** section in the site nav (e.g.
      [`generated/features/electrical.md`](../generated/features/electrical.md))
      is generated from the YAML definitions in this directory and is the
      **normative source of truth** for attribute IDs, types, defaults, access
      modes, and commands.
    - The **Feature Design** section (these hand-written `.md` files alongside
      the YAML, e.g. [`electrical.md`](electrical.md)) provides design
      rationale, worked examples, multi-zone semantics, and EEBUS use-case
      mapping that the generator does not emit.

    When the two disagree, the generated page wins. Drift is reported by the
    audit pipeline -- if you find a contradiction, file it as a doc bug.

    The Feature ID and EndpointType registries live in
    [`protocol-versions.yaml`](protocol-versions.yaml) and are the canonical
    source for codegen; any prose ID tables on hand-written pages are
    illustrative only.

---

## Feature Philosophy

MASH features are organized by concern:

| Feature | Question | Update Frequency |
|---------|----------|------------------|
| **Electrical** | "What CAN this do?" | Rarely (hardware/installation) |
| **Measurement** | "What IS it doing?" | Constantly (telemetry) |
| **EnergyControl** | "What SHOULD it do?" | On command (control) |
| **Status** | "Is it working?" | On state change |

---

## Feature Registry

See [`protocol-versions.yaml`](protocol-versions.yaml) for the canonical Feature ID registry — both `mash-go` and `mash-py` codegen consume it directly. Per-feature data-flow direction is declared on each feature's hand-written page (`**Direction:**` header).

**Direction (data flow):**
- **IN** = Data flows TO device (controller sends)
- **OUT** = Data flows FROM device (device reports)
- **-** = Bidirectional or static configuration

> **Note:** Direction indicates the typical data flow, not request direction. MASH operations (Read/Write/Subscribe/Invoke) are bidirectional - either side can initiate requests. For example, a device can Read from a controller that exposes features. See [Interaction Model: Bidirectional Communication](../interaction-model.md#11-bidirectional-communication).

---

## Feature Map Bits

The `featureMap` global attribute (ID `0xFFFC`) is a per-feature **32-bit bitmap** indicating capability **variants** within that feature. Same feature, different bits: e.g. an EVSE Electrical feature carries `CORE|FLEX|EMOB|V2X`, a battery inverter's Electrical feature carries `CORE|FLEX|BATTERY`. Bits are NOT a feature-presence bitmap — use the endpoint's `featureList` for "is this feature here".

Each bit corresponds to a PICS conformance point (F00–F1F). Auto-PICS reads the `featureMap` attribute per feature and enumerates the bits as individual conformance items. See [DEC-074](../decision-log.md#dec-074-featuremap-bit-semantics) for the clarified semantics and the 2026-04-23 cleanup that removed mDNS TXT advertisement + symbolic display rendering.

```
bit 0  (0x0001): CORE       - EnergyCore basics (always set)
bit 1  (0x0002): FLEX       - Flexible power adjustment (FlexibilityStruct)
bit 2  (0x0004): BATTERY    - Battery-specific attributes (SoC, SoH)
bit 3  (0x0008): EMOB       - E-Mobility/EVSE (charging sessions)
bit 4  (0x0010): SIGNALS    - Incentive signals support
bit 5  (0x0020): TARIFF     - Tariff data support
bit 6  (0x0040): PLAN       - Power plan support
bit 7  (0x0080): PROCESS    - Optional process lifecycle (OHPCF)
bit 8  (0x0100): FORECAST   - Power forecasting capability
bit 9  (0x0200): ASYMMETRIC - Per-phase asymmetric control
bit 10 (0x0400): V2X        - Vehicle-to-grid/home (bidirectional EV)
```

### Capability Discovery Pattern

Capability discovery reads the `featureMap` attribute directly (wire-level attribute 0xFFFC) on each feature; the mDNS TXT record does not duplicate this. Bits below map to feature-attribute details:

| FeatureMap Bit | Quick Check | Details In |
|----------------|-------------|------------|
| EMOB | Has EV charging | ChargingSession: `supportedChargingModes`, `evDemandMode` |
| ASYMMETRIC | Per-phase control | Electrical: `supportsAsymmetric` enum (NONE/CONSUMPTION/PRODUCTION/BIDIRECTIONAL) |
| V2X | Bidirectional EV | Electrical: `supportedDirections` enum; ChargingSession: `evDemandMode` = DYNAMIC_BIDIRECTIONAL |
| BATTERY | Has battery | Electrical: `energyCapacity`; separate BATTERY endpoint |
| FLEX | Flexible power | EnergyControl: `FlexibilityStruct` |
| SIGNALS | Price/limit signals | Signals feature present |
| TARIFF | Tariff structure | Tariff feature present |
| PLAN | Power planning | Plan feature present |
| PROCESS | Process control | EnergyControl: `processState`, `optionalProcess` |
| FORECAST | Power forecasting | EnergyControl: `ForecastStruct` |

**Discovery flow:**
1. Read `featureMap` → quick check what categories are supported
2. Read relevant feature attributes → get specific capability details

**Example:** EVSE with V2X and asymmetric charging but symmetric discharging:
- `featureMap`: CORE | EMOB | ASYMMETRIC | V2X
- Electrical: `supportsAsymmetric = CONSUMPTION` (asymmetric charge only)
- Electrical: `supportedDirections = BIDIRECTIONAL`

---

## Attribute Conformance (DEC-053)

MASH uses a **two-layer conformance model** for attributes:

| Layer | Defines | Location |
|-------|---------|----------|
| **Feature YAML** | Superset of all possible attributes, with universal `mandatory` flags | `<feature>/1.0.yaml` |
| **Endpoint type registry** | Per-endpoint-type mandatory/recommended attributes | `endpoint-conformance.yaml` |

**Feature YAML** defines what *can* exist. Attributes marked `mandatory: true` in the YAML are mandatory whenever the feature is present on **any** endpoint type.

**Endpoint type registry** tightens conformance per endpoint type. For example, Measurement has no universally mandatory attributes (a PV_STRING has DC only, a meter has AC only). Instead, `endpoint-conformance.yaml` defines:
- GRID_CONNECTION + Measurement: `acActivePower` mandatory
- BATTERY + Measurement: `dcPower`, `stateOfCharge` mandatory
- PV_STRING + Measurement: `dcPower` mandatory

Conformance levels:
- **mandatory**: Must be in `attributeList`. PICS validation fails without it. Value may be `null` (DEC-051).
- **recommended**: Should be in `attributeList` if hardware supports it. PICS validation warns if missing.
- **optional**: Everything else. Device's choice.

See [endpoint-conformance.yaml](endpoint-conformance.yaml) for the complete registry.

---

## YAML Definitions and Code Generation

Each feature has two representations:
- **`.md` files** -- Human-readable specifications with design rationale and examples
- **`<feature>/1.0.yaml` files** -- Machine-readable definitions used for Go code generation

The YAML files are the **source of truth** for attribute IDs, enum values, CBOR keys, data types, and command definitions. The `mash-featgen` tool in `mash-go/cmd/mash-featgen/` reads these YAML files and generates `*_gen.go` files in `pkg/features/` and `pkg/model/`.

### Directory Layout

```
features/
├── _shared/1.0.yaml              # Shared types (enums, structs used across features)
├── protocol-versions.yaml        # Feature type and endpoint type registries
├── device-info/1.0.yaml          # DeviceInfo feature definition
├── device-info.md                # DeviceInfo human-readable spec
├── electrical/1.0.yaml
├── electrical.md
├── measurement/1.0.yaml
├── measurement.md
├── energy-control/1.0.yaml
├── energy-control.md
├── status/1.0.yaml
├── status.md
├── charging-session/1.0.yaml
├── charging-session.md
├── signals/1.0.yaml
├── signals.md
├── tariff/1.0.yaml
├── tariff.md
├── plan/1.0.yaml
└── plan.md
```

### Regenerating Code

After editing any YAML file:

```bash
cd mash-go
make features    # Regenerate feature and model code
make generate    # Regenerate everything (features + use cases + mocks)
```

---

## Common Patterns

### Attribute Numbering Convention

Each feature uses consistent attribute ID ranges:

| Range | Purpose |
|-------|---------|
| 1-9 | Core identity/type attributes |
| 10-19 | Capability flags |
| 20-29 | Primary data (limits, setpoints, etc.) |
| 30-39 | Secondary data (per-phase variants) |
| 40-49 | Tertiary data |
| 50-59 | Additional data |
| 60-69 | Complex structures (flexibility, forecast) |
| 70-79 | Failsafe configuration |
| 80-89 | Process management |
| 0xFFF0-0xFFFF | Global attributes (reserved) |

### Command Numbering Convention

| Range | Purpose |
|-------|---------|
| 1-4 | Primary commands (Set/Clear for limits) |
| 5-8 | Secondary commands (Set/Clear for currents) |
| 9-12 | Control commands (Pause/Resume/Stop) |
| 13-16 | Process commands (Schedule/Cancel) |

---

## Device Composition

### EVSE Example

```
Endpoint 1 (type: EV_CHARGER)
├── Electrical           ← Phase config, ratings
├── Measurement          ← Power, energy readings
├── EnergyControl        ← Limits, control
├── Status               ← Operating state
└── ChargingSession      ← EV session info
```

### Hybrid Inverter Example

```
Endpoint 1 (type: INVERTER)
├── Electrical
├── Measurement
├── EnergyControl
└── Status

Endpoint 2 (type: PV_STRING)
├── Electrical
├── Measurement
└── Status

Endpoint 3 (type: BATTERY)
├── Electrical
├── Measurement
├── EnergyControl
└── Status
```

### Heat Pump Example

```
Endpoint 1 (type: HEAT_PUMP)
├── Electrical
├── Measurement
├── EnergyControl        ← includes processState, optionalProcess
└── Status
```

---

## EEBUS Use Case Mapping

| EEBUS Use Case | MASH Features |
|----------------|---------------|
| LPC (Load Power Control) | EnergyControl |
| LPP (Load Power Production) | EnergyControl |
| MPC (Measurement Power) | Measurement |
| MGCP (Monitoring Grid Connection) | Measurement (on GRID_CONNECTION endpoint) |
| EVSE, EVCEM, CEVC | ChargingSession + EnergyControl + Signals + Plan |
| COB (Control of Battery) | EnergyControl + Measurement (on BATTERY endpoint) |
| MOB (Monitoring of Battery) | Measurement + Status |
| MOI (Monitoring of Inverter) | Measurement + Status |
| MPS (Monitoring of PV String) | Measurement |
| ToUT (Time of Use Tariff) | Signals + Tariff |
| POEN (Power Envelope) | Signals (CONSTRAINT type) |
| ITPCM (Incentive Table) | Signals + Tariff |
| OHPCF (Heat Pump Flexibility) | EnergyControl (processState, optionalProcess) |

---

## Related Documents

| Document | Description |
|----------|-------------|
| [Protocol Overview](../protocol-overview.md) | Vision, architecture, device model |
| [Discovery](../discovery.md) | Capability discovery, featureMap |
| [Interaction Model](../interaction-model.md) | Read/Write/Subscribe/Invoke |
| [Decision Log](../decision-log.md) | Design decisions |
