# Tariff Feature

> Price structure, components, and power-dependent tiers

**Feature ID:** 0x0007
**Direction:** IN (controller sends to device)
**FeatureMap Bit:** TARIFF (0x0020)
**Status:** Draft
**Last Updated:** 2025-01-25

!!! info "Normative reference"
    The authoritative attribute and command tables for Tariff are
    [generated from YAML](../generated/features/tariff.md). This page is the
    design rationale and worked-example companion -- if anything here disagrees
    with the generated table, the generated table wins.

---

## Purpose

Describes HOW prices are structured - tariff components, power tiers, currency. Complements the [Signals](signals.md) feature which provides time-slotted price values.

---

## Design Principles

1. **Tariff = Structure** - components, tiers, how to calculate
2. **Signals = Values** - actual prices per time slot
3. **Power tiers** - different prices based on power level (ITPCM)
4. **Multiple components** - energy, demand charges, fees

---

## Attributes

```cbor
Tariff Feature:
{
  // Tariff identification
  1: tariffId,               // uint32: unique identifier
  2: tariffName,             // string?: human-readable name
  3: currency,               // string: ISO 4217 code (EUR, USD)
  4: priceUnit,              // PriceUnitEnum: what the price is for

  // Tariff components
  10: components,            // TariffComponent[]: price components

  // Power tiers (for ITPCM-style)
  20: powerTiers,            // PowerTier[]?: power-dependent pricing

  // Validity
  30: validFrom,             // timestamp?: when tariff starts
  31: validTo,               // timestamp?: when tariff ends
}

TariffComponent:
{
  1: componentId,            // uint8: unique within tariff
  2: type,                   // ComponentTypeEnum
  3: name,                   // string?: description
  4: price,                  // int32: price in 0.0001 currency units
  5: unit,                   // PriceUnitEnum
}

PowerTier:
{
  1: tierId,                 // uint8: tier identifier
  2: minPower,               // int64 mW: tier starts at this power
  3: maxPower,               // int64 mW: tier ends at this power
  4: price,                  // int32: price for this tier
}
```

---

## Enumerations

### PriceUnitEnum

```
PER_KWH           = 0x00  // Price per kWh
PER_KVAH          = 0x01  // Price per kVAh
PER_KW_MONTH      = 0x02  // Demand charge per kW
PER_DAY           = 0x03  // Daily fixed charge
PER_MONTH         = 0x04  // Monthly fixed charge
```

### ComponentTypeEnum

```
ENERGY            = 0x00  // Energy consumption charge
DEMAND            = 0x01  // Peak demand charge
FIXED             = 0x02  // Fixed fee
TAX               = 0x03  // Tax component
GRID_FEE          = 0x04  // Grid access fee
RENEWABLE_LEVY    = 0x05  // Renewable energy surcharge
```

---

## Commands

### SetTariff

Controller sends tariff structure to device.

```cbor
Request:
{
  1: tariff,               // Tariff: complete tariff structure
}

Response:
{
  1: success,              // bool
  2: tariffId,             // uint32: confirmed tariff ID
}
```

---

## Power Tier Semantics

Power tiers define different prices based on consumption level within a time slot:

```
Tier 1: 0 - 3.7kW   @ 0.15/kWh
Tier 2: 3.7 - 7.4kW @ 0.25/kWh
Tier 3: 7.4 - 11kW  @ 0.35/kWh
```

If device consumes 5kW for 1 hour:
- First 3.7kWh @ 0.15 = 0.555
- Next 1.3kWh @ 0.25 = 0.325
- Total: 0.88 for 5kWh

---

## Examples

### Simple Time-of-Use Tariff

```cbor
{
  tariffId: 1,
  tariffName: "Residential ToU",
  currency: "EUR",
  priceUnit: PER_KWH,
  components: [
    { componentId: 1, type: ENERGY, name: "Energy", price: 0, unit: PER_KWH },
    { componentId: 2, type: GRID_FEE, name: "Grid", price: 800, unit: PER_KWH },
    { componentId: 3, type: TAX, name: "VAT", price: 0, unit: PER_KWH }
  ]
}
// Actual energy prices come via Signals feature (time-slotted)
```

### Power-Tiered Tariff (ITPCM)

```cbor
{
  tariffId: 2,
  tariffName: "Dynamic Power Tiers",
  currency: "EUR",
  priceUnit: PER_KWH,
  powerTiers: [
    { tierId: 1, minPower: 0,       maxPower: 3700000,  price: 1500 },  // 0.15/kWh
    { tierId: 2, minPower: 3700000, maxPower: 7400000,  price: 2500 },  // 0.25/kWh
    { tierId: 3, minPower: 7400000, maxPower: 11000000, price: 3500 }   // 0.35/kWh
  ]
}
```

### German Household Consumption Tariff (Detailed)

A realistic German tariff with all component types and power tiers:

```cbor
{
  tariffId: 1,
  tariffName: "Stadtwerke Berlin Flex",
  currency: "EUR",
  priceUnit: PER_KWH,
  components: [
    { componentId: 1, type: ENERGY, name: "Energy", price: 0 },               // variable via Signals
    { componentId: 2, type: GRID_FEE, name: "Grid fee", price: 800 },         // 0.08 EUR fixed
    { componentId: 3, type: RENEWABLE_LEVY, name: "EEG surcharge", price: 350 }, // 0.035 EUR
    { componentId: 4, type: RENEWABLE_LEVY, name: "KWKG levy", price: 50 },   // 0.005 EUR
    { componentId: 5, type: TAX, name: "Electricity tax", price: 205 },        // 0.0205 EUR
    { componentId: 6, type: TAX, name: "VAT 19%", price: 0 }                  // calculated
  ],
  powerTiers: [
    { tierId: 1, minPower: 0,       maxPower: 6000000,  price: 0 },    // 0-6kW: standard rate
    { tierId: 2, minPower: 6000000, maxPower: 22000000, price: 625 }   // >6kW: +0.0625 EUR premium
  ]
}
```

### Feed-in Tariff (PV Surplus)

```cbor
{
  tariffId: 2,
  tariffName: "Feed-in Compensation",
  currency: "EUR",
  priceUnit: PER_KWH,
  components: [
    { componentId: 1, type: ENERGY, name: "Feed-in rate", price: 820 }  // 0.082 EUR/kWh
  ]
  // No power tiers - flat rate for all production
}
```

### Nordic Spot-Based Tariff

```cbor
{
  tariffId: 3,
  tariffName: "Nordpool Spot",
  currency: "SEK",
  priceUnit: PER_KWH,
  components: [
    { componentId: 1, type: ENERGY, name: "Spot price", price: 0 },             // 100% variable
    { componentId: 2, type: GRID_FEE, name: "Network fee", price: 4500 },       // 0.45 SEK fixed
    { componentId: 3, type: TAX, name: "Energy tax", price: 3600 },             // 0.36 SEK
    { componentId: 4, type: TAX, name: "VAT 25%", price: 0 }                    // calculated
  ]
}
```

---

## Price Calculation

Total price = sum of all components for the energy consumed:

```
Energy consumed: 10 kWh
Tariff components:
  - Energy: 0.20/kWh × 10 = 2.00
  - Grid fee: 0.08/kWh × 10 = 0.80
  - Tax (19%): 0.53

Total: 3.33 EUR
```

---

## EEBUS/Matter Coverage

| Standard | Tariff Coverage |
|----------|-----------------|
| EEBUS ITPCM | Power tiers (IncentiveTable) |
| EEBUS ToUT | Components + Signals for time values |
| Matter Tariff cluster | Similar component model |

---

## Related Features

| Feature | Relationship |
|---------|--------------|
| [Signals](signals.md) | Provides time-slotted prices; Tariff provides structure |
| [Plan](plan.md) | Device's cost-optimized response |
