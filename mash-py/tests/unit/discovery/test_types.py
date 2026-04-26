"""Unit tests for ``mash_py.discovery.types``.

These cover:

- DEC-078 commissionable TXT parsing (round-trip + missing keys).
- DEC-082 SRV port preservation — never substitute a default.
- ``cat`` parsing including multi-category and empty/whitespace tokens.
- Operational TXT optional-key fallbacks.
"""

from __future__ import annotations

import pytest

from mash_py.discovery.types import (
    MAX_DISCRIMINATOR,
    SERVICE_TYPE_COMMISSIONABLE,
    SERVICE_TYPE_OPERATIONAL,
    SERVICE_TYPE_PAIRING_REQUEST,
    TXTRecordError,
    parse_categories,
    parse_commissionable_txt,
    parse_feature_map,
    parse_operational_txt,
)


class TestServiceTypeConstants:
    """Spec-defined service names (``discovery.md §2.1``)."""

    def test_commissionable_matches_spec(self):
        assert SERVICE_TYPE_COMMISSIONABLE == "_mash-comm._tcp.local."

    def test_operational_matches_spec(self):
        assert SERVICE_TYPE_OPERATIONAL == "_mash._tcp.local."

    def test_pairing_request_matches_spec(self):
        assert SERVICE_TYPE_PAIRING_REQUEST == "_mashp._udp.local."


class TestParseCategories:
    def test_single(self):
        assert parse_categories("3") == (3,)

    def test_multi(self):
        # Spec example: hybrid EMS + inverter (discovery.md §2.2).
        assert parse_categories("2,5") == (2, 5)

    def test_whitespace_tolerated(self):
        assert parse_categories("2 , 5") == (2, 5)

    def test_trailing_empty_token_skipped(self):
        assert parse_categories("3,") == (3,)

    def test_non_numeric_rejected(self):
        with pytest.raises(TXTRecordError):
            parse_categories("3,abc")


class TestParseFeatureMap:
    def test_hex_with_prefix(self):
        # Spec example: 0x001B = CORE | FLEX | BATTERY | EMOB (§2.3).
        assert parse_feature_map("0x001B") == 0x1B

    def test_uppercase_prefix(self):
        assert parse_feature_map("0X1B") == 0x1B

    def test_decimal(self):
        # Both bases accepted; spec phrasing is loose.
        assert parse_feature_map("27") == 27

    def test_invalid(self):
        with pytest.raises(TXTRecordError):
            parse_feature_map("zz")


class TestParseCommissionableTXT:
    """DEC-078 6-key schema."""

    def _txt_minimal(self):
        return {
            b"D": b"1234",
            b"cat": b"3",
            b"serial": b"WB-2024-001234",
            b"brand": b"ChargePoint",
            b"model": b"Home Flex",
        }

    def test_minimal_required_keys(self):
        svc = parse_commissionable_txt(
            instance_name="MASH-1234",
            host="evse-001.local.",
            port=8443,
            addresses=("192.168.1.42",),
            txt=self._txt_minimal(),
        )
        assert svc.discriminator == 1234
        assert svc.categories == (3,)
        assert svc.serial == "WB-2024-001234"
        assert svc.brand == "ChargePoint"
        assert svc.model == "Home Flex"
        assert svc.device_name == ""  # absent → empty
        assert svc.port == 8443

    def test_optional_device_name(self):
        txt = self._txt_minimal()
        txt[b"DN"] = b"Garage Charger"
        svc = parse_commissionable_txt(
            instance_name="MASH-1234",
            host="evse-001.local.",
            port=8443,
            addresses=(),
            txt=txt,
        )
        assert svc.device_name == "Garage Charger"

    def test_str_keys_accepted(self):
        # Tests pass dict[str, str]; zeroconf passes dict[bytes, bytes].
        svc = parse_commissionable_txt(
            instance_name="MASH-1234",
            host="x.local.",
            port=8443,
            addresses=(),
            txt={
                "D": "1234",
                "cat": "3",
                "serial": "S",
                "brand": "B",
                "model": "M",
            },
        )
        assert svc.discriminator == 1234

    def test_dec_082_port_preserved_when_nonstandard(self):
        # Two-port deployment: commissioning may live on a non-default port.
        # The parser must surface whatever the SRV said.
        svc = parse_commissionable_txt(
            instance_name="MASH-1234",
            host="x.local.",
            port=51234,
            addresses=(),
            txt=self._txt_minimal(),
        )
        assert svc.port == 51234

    @pytest.mark.parametrize(
        "missing_key",
        [b"D", b"cat", b"serial", b"brand", b"model"],
    )
    def test_missing_required_key_raises(self, missing_key):
        txt = self._txt_minimal()
        del txt[missing_key]
        with pytest.raises(TXTRecordError):
            parse_commissionable_txt(
                instance_name="MASH-1234",
                host="x.local.",
                port=8443,
                addresses=(),
                txt=txt,
            )

    def test_discriminator_out_of_range(self):
        txt = self._txt_minimal()
        txt[b"D"] = str(MAX_DISCRIMINATOR + 1).encode()
        with pytest.raises(TXTRecordError):
            parse_commissionable_txt(
                instance_name="MASH-1234",
                host="x.local.",
                port=8443,
                addresses=(),
                txt=txt,
            )

    def test_discriminator_non_numeric(self):
        txt = self._txt_minimal()
        txt[b"D"] = b"not-a-number"
        with pytest.raises(TXTRecordError):
            parse_commissionable_txt(
                instance_name="MASH-1234",
                host="x.local.",
                port=8443,
                addresses=(),
                txt=txt,
            )


class TestParseOperationalTXT:
    def test_minimal_required(self):
        svc = parse_operational_txt(
            instance_name="PEN12345-EVSE001",
            host="evse-001.local.",
            port=8443,
            addresses=("192.168.1.42",),
            txt={b"DI": b"PEN12345.EVSE001"},
        )
        assert svc.device_id == "PEN12345.EVSE001"
        assert svc.vendor_product == ""
        assert svc.firmware == ""
        assert svc.endpoint_count == 0
        assert svc.feature_map == 0

    def test_full_record(self):
        svc = parse_operational_txt(
            instance_name="PEN12345-EVSE001",
            host="evse-001.local.",
            port=8443,
            addresses=("192.168.1.42",),
            txt={
                b"DI": b"PEN12345.EVSE001",
                b"VP": b"1234:5678",
                b"FW": b"1.2.3",
                b"EP": b"2",
                b"FM": b"0x001B",
            },
        )
        assert svc.vendor_product == "1234:5678"
        assert svc.firmware == "1.2.3"
        assert svc.endpoint_count == 2
        assert svc.feature_map == 0x1B

    def test_missing_di_raises(self):
        with pytest.raises(TXTRecordError):
            parse_operational_txt(
                instance_name="x",
                host="x.local.",
                port=8443,
                addresses=(),
                txt={},
            )

    def test_invalid_ep(self):
        with pytest.raises(TXTRecordError):
            parse_operational_txt(
                instance_name="x",
                host="x.local.",
                port=8443,
                addresses=(),
                txt={b"DI": b"x", b"EP": b"abc"},
            )
