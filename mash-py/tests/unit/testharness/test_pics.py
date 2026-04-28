"""PICS unit tests."""

from __future__ import annotations

from mash_py.testharness.pics import (
    check_pics_requirements,
    parse_pics,
)


def test_parse_yaml_form() -> None:
    pf = parse_pics(
        b"""
items:
  MASH.S.COMM: true
  MASH.S.WINDOW_MIN: 180
  MASH.S.NAME: foo
""",
        name="test",
    )
    assert pf.items == {
        "MASH.S.COMM": True,
        "MASH.S.WINDOW_MIN": 180,
        "MASH.S.NAME": "foo",
    }


def test_parse_dsl_form() -> None:
    pf = parse_pics(
        b"""
# comment line
MASH.S.COMM = true
MASH.S.PORT = 8443
MASH.S.NAME: alice
""",
    )
    assert pf.items["MASH.S.COMM"] is True
    assert pf.items["MASH.S.PORT"] == 8443
    assert pf.items["MASH.S.NAME"] == "alice"


def test_check_requirements_no_pics_passes_through() -> None:
    """When ``pf`` is None (no -pics flag), all requirements pass.

    Mirrors mash-go's runner behavior — "no PICS configured" means "no
    capability filtering", not "fail every test that lists requirements".
    """
    assert check_pics_requirements(None, ["MASH.S.COMM"]) is True


def test_check_requirements_truthy_form() -> None:
    pf = parse_pics(b"items:\n  MASH.S.COMM: true\n  MASH.S.OTHER: false\n")
    assert check_pics_requirements(pf, ["MASH.S.COMM"]) is True
    assert check_pics_requirements(pf, ["MASH.S.OTHER"]) is False
    assert check_pics_requirements(pf, ["MASH.S.MISSING"]) is False


def test_check_requirements_value_form() -> None:
    pf = parse_pics(b"items:\n  MASH.S.WINDOW: 900\n  MASH.S.NAME: bob\n")
    assert check_pics_requirements(pf, ["MASH.S.WINDOW: 900"]) is True
    assert check_pics_requirements(pf, ["MASH.S.WINDOW=900"]) is True
    assert check_pics_requirements(pf, ["MASH.S.WINDOW: 600"]) is False
    assert check_pics_requirements(pf, ["MASH.S.NAME=bob"]) is True
