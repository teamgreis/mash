# DeviceInfo Feature

> Device identity, manufacturer information, and device structure

**Feature ID:** 0x01
**Direction:** OUT (device reports to controller)
**Status:** Draft
**Last Updated:** 2025-01-25

!!! info "Normative reference"
    The authoritative attribute and command tables for DeviceInfo are
    [generated from YAML](../generated/features/device-info.md). This page is
    the design rationale and worked-example companion -- if anything here
    disagrees with the generated table, the generated table wins.

---

## Purpose

Device identity, manufacturer information, and complete device structure. This feature exists **only on endpoint 0** (DEVICE_ROOT).

---

## Device ID Format

Devices are identified by a globally unique string:

**With IANA Private Enterprise Number:**
```
PEN<number>.<serial>
PEN12345.EVSE-001-A
```

**Without PEN (fallback):**
```
<vendor>-<product>-<serial>
Wallbox-Pulsar-ABC123
```

---

## Attributes

```cbor
DeviceInfo Feature:
{
  // IDENTIFICATION
  1: deviceId,               // string: globally unique identifier
  2: vendorName,             // string: manufacturer name
  3: productName,            // string: product name
  4: serialNumber,           // string: serial number
  5: vendorId,               // uint32?: IANA PEN (optional)
  6: productId,              // uint16?: vendor's product ID

  // VERSIONS
  10: softwareVersion,       // string: firmware version
  11: hardwareVersion,       // string?: hardware revision
  12: specVersion,           // string: MASH protocol version, major.minor (e.g., "1.0")

  // DEVICE STRUCTURE
  20: endpoints,             // EndpointInfo[]: complete device structure

  // OPTIONAL METADATA
  30: location,              // string?: installation location
  31: label,                 // string?: user-assigned name
}

EndpointInfo:
{
  1: id,                     // uint8: endpoint ID
  2: type,                   // EndpointTypeEnum
  3: label,                  // string?: user-friendly name
  4: features,               // uint16[]: feature IDs on this endpoint
}
```

> Per-feature capability variants are discovered by reading global attribute `featureMap` (0xFFFC) on each feature; see [DEC-074](../decision-log.md#dec-074-featuremap-bit-semantics).

---

## Usage

Read DeviceInfo once at connection to understand:
1. Device identity and manufacturer
2. Complete endpoint structure
3. Available features per endpoint

```cbor
// Example: EVSE with two charging ports
{
  deviceId: "PEN12345.EVSE-001-A",
  vendorName: "Wallbox",
  productName: "Pulsar Plus",
  serialNumber: "WB-2024-001234",
  vendorId: 12345,
  softwareVersion: "3.2.1",
  specVersion: "1.0",
  endpoints: [
    {
      id: 0,
      type: DEVICE_ROOT,
      features: [0x01]
    },
    {
      id: 1,
      type: EV_CHARGER,
      label: "Port 1",
      features: [0x02, 0x03, 0x04, 0x05, 0x06]
    },
    {
      id: 2,
      type: EV_CHARGER,
      label: "Port 2",
      features: [0x02, 0x03, 0x04, 0x05, 0x06]
    }
  ]
}
```

---

## Feature ID Registry

See [`protocol-versions.yaml`](protocol-versions.yaml) for the canonical Feature ID registry. Prose tables are illustrative only — the YAML is the single source of truth that codegen consumes.

---

## EEBUS NID Use Case Coverage

| EEBUS Data | DeviceInfo Mapping |
|------------|-------------------|
| deviceCode | deviceId |
| brandName | vendorName |
| deviceModel | productName |
| serialNumber | serialNumber |
| softwareRevision | softwareVersion |

---

## Related Features

| Feature | Relationship |
|---------|--------------|
| [Discovery](../discovery.md) | mDNS provides basic info; DeviceInfo provides full structure |
