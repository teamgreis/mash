"""Unit tests for ``mash_py.discovery.types``.

These cover:

- DEC-078 commissionable TXT parsing (round-trip + missing keys).
- DEC-082 SRV port preservation — never substitute a default.
- ``cat`` parsing including multi-category and empty/whitespace tokens.
- DEC-088 operational TXT parsing: ``ZI`` + ``DI`` required, hex-validated;
  ``FM`` silently ignored (DEC-074).
- DEC-088 instance-name helpers and TXT builders.
"""

from __future__ import annotations

import pytest

from mash_py.discovery.types import (
    DEFAULT_OPERATIONAL_PORT,
    MAX_DISCRIMINATOR,
    SERVICE_TYPE_COMMISSIONABLE,
    SERVICE_TYPE_OPERATIONAL,
    SERVICE_TYPE_PAIRING_REQUEST,
    AdvertiseError,
    CommissionableAdvertisement,
    OperationalAdvertisement,
    TXTRecordError,
    build_commissionable_txt,
    build_operational_txt,
    commissionable_instance_name,
    operational_instance_name,
    parse_categories,
    parse_commissionable_txt,
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
    """DEC-088 schema: ZI + DI required (16 hex), FM ignored (DEC-074)."""

    ZI = "A1B2C3D4E5F6A7B8"
    DI = "F9E8D7C6B5A49382"

    def _txt_minimal(self):
        return {b"ZI": self.ZI.encode(), b"DI": self.DI.encode()}

    def test_minimal_required(self):
        svc = parse_operational_txt(
            instance_name=f"{self.ZI}-{self.DI}",
            host="evse-001.local.",
            port=8443,
            addresses=("192.168.1.42",),
            txt=self._txt_minimal(),
        )
        assert svc.zone_id == self.ZI
        assert svc.device_id == self.DI
        assert svc.vendor_product == ""
        assert svc.firmware == ""
        assert svc.endpoint_count == 0
        # DEC-074: FM is forbidden in operational TXT; the parser does not
        # surface it as a field at all (no `feature_map` attr).
        assert not hasattr(svc, "feature_map")

    def test_full_record(self):
        svc = parse_operational_txt(
            instance_name=f"{self.ZI}-{self.DI}",
            host="evse-001.local.",
            port=8443,
            addresses=("192.168.1.42",),
            txt={
                b"ZI": self.ZI.encode(),
                b"DI": self.DI.encode(),
                b"VP": b"1234:5678",
                b"FW": b"1.2.3",
                b"EP": b"2",
            },
        )
        assert svc.vendor_product == "1234:5678"
        assert svc.firmware == "1.2.3"
        assert svc.endpoint_count == 2

    def test_legacy_fm_silently_ignored(self):
        """DEC-088: decoders MUST ignore any legacy FM key (DEC-074 backstop)."""
        svc = parse_operational_txt(
            instance_name=f"{self.ZI}-{self.DI}",
            host="x.local.",
            port=8443,
            addresses=(),
            txt={
                b"ZI": self.ZI.encode(),
                b"DI": self.DI.encode(),
                b"FM": b"0x001B",  # legacy from a non-conformant peer
            },
        )
        # Must not raise, must not surface the value anywhere.
        assert svc.zone_id == self.ZI
        assert svc.device_id == self.DI

    def test_missing_zi_raises(self):
        with pytest.raises(TXTRecordError, match="ZI"):
            parse_operational_txt(
                instance_name="x",
                host="x.local.",
                port=8443,
                addresses=(),
                txt={b"DI": self.DI.encode()},
            )

    def test_missing_di_raises(self):
        with pytest.raises(TXTRecordError, match="DI"):
            parse_operational_txt(
                instance_name="x",
                host="x.local.",
                port=8443,
                addresses=(),
                txt={b"ZI": self.ZI.encode()},
            )

    def test_zi_wrong_length_raises(self):
        with pytest.raises(TXTRecordError, match="ZI"):
            parse_operational_txt(
                instance_name="x",
                host="x.local.",
                port=8443,
                addresses=(),
                txt={b"ZI": b"DEAD", b"DI": self.DI.encode()},
            )

    def test_di_non_hex_raises(self):
        bad_di = "ZZZZZZZZZZZZZZZZ"  # 16 chars but not hex
        with pytest.raises(TXTRecordError, match="DI"):
            parse_operational_txt(
                instance_name="x",
                host="x.local.",
                port=8443,
                addresses=(),
                txt={b"ZI": self.ZI.encode(), b"DI": bad_di.encode()},
            )

    def test_invalid_ep(self):
        with pytest.raises(TXTRecordError):
            parse_operational_txt(
                instance_name="x",
                host="x.local.",
                port=8443,
                addresses=(),
                txt={
                    b"ZI": self.ZI.encode(),
                    b"DI": self.DI.encode(),
                    b"EP": b"abc",
                },
            )


class TestInstanceNameHelpers:
    """DEC-088: canonical instance names for each service type."""

    @pytest.mark.parametrize(
        ("d", "expected"),
        [
            (0, "MASH-0000"),
            (1, "MASH-0001"),
            (42, "MASH-0042"),
            (1234, "MASH-1234"),
            (4095, "MASH-4095"),
        ],
    )
    def test_commissionable_instance_name_zero_pads_to_four(self, d, expected):
        assert commissionable_instance_name(d) == expected

    @pytest.mark.parametrize("d", [-1, 4096, 65535])
    def test_commissionable_instance_name_rejects_out_of_range(self, d):
        with pytest.raises(AdvertiseError):
            commissionable_instance_name(d)

    def test_operational_instance_name_concat(self):
        zi = "A1B2C3D4E5F6A7B8"
        di = "F9E8D7C6B5A49382"
        assert operational_instance_name(zi, di) == f"{zi}-{di}"
        assert len(operational_instance_name(zi, di)) == 33  # ≤ 63

    @pytest.mark.parametrize("bad", ["short", "", "TOOLONG_TOOLONG_X", "a" * 17])
    def test_operational_instance_name_rejects_wrong_length(self, bad):
        di = "F9E8D7C6B5A49382"
        with pytest.raises(AdvertiseError):
            operational_instance_name(bad, di)
        with pytest.raises(AdvertiseError):
            operational_instance_name(di, bad)

    def test_operational_instance_name_requires_hex(self):
        with pytest.raises(AdvertiseError):
            operational_instance_name("ZZZZZZZZZZZZZZZZ", "F9E8D7C6B5A49382")


class TestBuildTXTHelpers:
    """DEC-078 + DEC-088 TXT-building round-trip with the parser."""

    def test_commissionable_round_trip(self):
        adv = CommissionableAdvertisement(
            discriminator=1234,
            serial="WB-2024-001234",
            brand="ChargePoint",
            model="Home Flex",
            port=8443,
            categories=(3,),
            device_name="Garage Charger",
        )
        txt = build_commissionable_txt(adv)
        # All required keys present, plus optional DN.
        assert txt["D"] == "1234"
        assert txt["cat"] == "3"
        assert txt["serial"] == "WB-2024-001234"
        assert txt["brand"] == "ChargePoint"
        assert txt["model"] == "Home Flex"
        assert txt["DN"] == "Garage Charger"

        # Round-trip through the parser.
        svc = parse_commissionable_txt(
            instance_name=commissionable_instance_name(adv.discriminator),
            host="x.local.",
            port=adv.port,
            addresses=(),
            txt={k.encode(): v.encode() for k, v in txt.items()},
        )
        assert svc.discriminator == adv.discriminator
        assert svc.serial == adv.serial
        assert svc.device_name == adv.device_name

    def test_commissionable_omits_device_name_when_blank(self):
        adv = CommissionableAdvertisement(
            discriminator=1,
            serial="S",
            brand="B",
            model="M",
            port=8443,
        )
        txt = build_commissionable_txt(adv)
        assert "DN" not in txt
        # cat is mandatory but may be empty when no category is declared.
        assert txt["cat"] == ""

    @pytest.mark.parametrize(
        ("field", "value"),
        [
            ("serial", ""),
            ("brand", ""),
            ("model", ""),
        ],
    )
    def test_commissionable_rejects_blank_required_field(self, field, value):
        kwargs = {
            "discriminator": 1234,
            "serial": "S",
            "brand": "B",
            "model": "M",
            "port": 8443,
            field: value,
        }
        adv = CommissionableAdvertisement(**kwargs)
        with pytest.raises(AdvertiseError, match=field):
            build_commissionable_txt(adv)

    def test_commissionable_rejects_out_of_range_discriminator(self):
        adv = CommissionableAdvertisement(
            discriminator=MAX_DISCRIMINATOR + 1,
            serial="S",
            brand="B",
            model="M",
            port=8443,
        )
        with pytest.raises(AdvertiseError):
            build_commissionable_txt(adv)

    def test_operational_round_trip(self):
        adv = OperationalAdvertisement(
            zone_id="A1B2C3D4E5F6A7B8",
            device_id="F9E8D7C6B5A49382",
            vendor_product="1234:5678",
            firmware="1.2.3",
            endpoint_count=2,
        )
        assert adv.port == DEFAULT_OPERATIONAL_PORT  # DEC-082 default

        txt = build_operational_txt(adv)
        assert txt["ZI"] == adv.zone_id
        assert txt["DI"] == adv.device_id
        assert txt["VP"] == "1234:5678"
        assert txt["FW"] == "1.2.3"
        assert txt["EP"] == "2"
        # DEC-074: FM is never emitted.
        assert "FM" not in txt

        svc = parse_operational_txt(
            instance_name=operational_instance_name(adv.zone_id, adv.device_id),
            host="x.local.",
            port=adv.port,
            addresses=(),
            txt={k.encode(): v.encode() for k, v in txt.items()},
        )
        assert svc.zone_id == adv.zone_id
        assert svc.device_id == adv.device_id
        assert svc.endpoint_count == 2

    def test_operational_omits_optional_fields_when_blank(self):
        adv = OperationalAdvertisement(
            zone_id="A1B2C3D4E5F6A7B8",
            device_id="F9E8D7C6B5A49382",
        )
        txt = build_operational_txt(adv)
        assert set(txt.keys()) == {"ZI", "DI"}

    def test_operational_rejects_invalid_zone_id(self):
        adv = OperationalAdvertisement(
            zone_id="not-hex-and-wrong-len",
            device_id="F9E8D7C6B5A49382",
        )
        with pytest.raises(AdvertiseError, match="zone_id"):
            build_operational_txt(adv)
