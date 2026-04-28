"""Loader unit tests — verify the YAML schema parses every Phase-21 case.

These tests exercise the loader against the real ``mash-go/testdata/cases``
fixtures so any spec-author-side change to a case file (added field, new
``store_result`` form) shows up here as a parse error rather than at run
time. The bar is "byte-compatible with mash-go's loader" — the same YAML
files MUST parse identically.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from mash_py.testharness.loader import (
    LoadError,
    Step,
    TestCase,
    filter_by_pattern,
    filter_by_tags,
    load_test_directory,
    parse_test_cases,
)

REPO_ROOT = Path(__file__).resolve().parents[3]
GO_CASES_DIR = REPO_ROOT.parent / "mash-go" / "testdata" / "cases"


def test_parse_minimal_case() -> None:
    yaml = b"""
id: TC-FOO-001
name: Foo
steps:
  - action: connect
"""
    cases = parse_test_cases(yaml)
    assert len(cases) == 1
    assert cases[0].id == "TC-FOO-001"
    assert cases[0].steps[0].action == "connect"


def test_parse_multi_document() -> None:
    yaml = b"""
---
id: TC-A
steps:
  - action: connect
---
# comment-only doc, should be skipped
---
id: TC-B
steps:
  - action: read
"""
    cases = parse_test_cases(yaml)
    assert [tc.id for tc in cases] == ["TC-A", "TC-B"]


def test_parse_pics_requirements_mixed_form() -> None:
    yaml = b"""
id: TC-X
steps: [{action: read}]
pics_requirements:
  - MASH.S.COMM
  - MASH.S.WINDOW: 900
  - "MASH.S.PORT=8443"
"""
    [tc] = parse_test_cases(yaml)
    assert tc.pics_requirements == [
        "MASH.S.COMM",
        "MASH.S.WINDOW: 900",
        "MASH.S.PORT=8443",
    ]


def test_missing_id_raises() -> None:
    yaml = b"""
name: no id
steps:
  - action: connect
"""
    cases = parse_test_cases(yaml)
    # No id => skipped per Go behavior (treat as empty document).
    assert cases == []


def test_steps_required() -> None:
    yaml = b"""
id: TC-EMPTY
"""
    with pytest.raises(LoadError):
        parse_test_cases(yaml)


def test_step_requires_action() -> None:
    yaml = b"""
id: TC-NO-ACTION
steps:
  - params: {a: 1}
"""
    with pytest.raises(LoadError):
        parse_test_cases(yaml)


def test_filter_by_pattern_glob() -> None:
    cases = [
        TestCase(id="TC-FOO-001", steps=[Step(action="x")]),
        TestCase(id="TC-FOO-002", steps=[Step(action="x")]),
        TestCase(id="TC-BAR-001", steps=[Step(action="x")]),
    ]
    assert [tc.id for tc in filter_by_pattern(cases, "TC-FOO-*")] == [
        "TC-FOO-001",
        "TC-FOO-002",
    ]


def test_filter_by_tags_intersection_and_exclude() -> None:
    cases = [
        TestCase(id="A", tags=["x", "y"], steps=[Step(action="x")]),
        TestCase(id="B", tags=["x"], steps=[Step(action="x")]),
        TestCase(id="C", tags=["x", "slow"], steps=[Step(action="x")]),
    ]
    inc = filter_by_tags(cases, include="x,y")
    assert [tc.id for tc in inc] == ["A"]
    exc = filter_by_tags(cases, exclude="slow")
    assert [tc.id for tc in exc] == ["A", "B"]


def test_load_real_go_cases_smoke() -> None:
    """Phase 21 acceptance prerequisite: every YAML in testdata/cases parses.

    The harness only *runs* a small subset, but the loader must accept the
    full fixture set (schema-compatible with mash-go) so users can drop in
    any case path without surprise.
    """
    if not GO_CASES_DIR.is_dir():
        pytest.skip(f"mash-go testdata not found at {GO_CASES_DIR}")
    cases = load_test_directory(GO_CASES_DIR)
    # The fixture set is large (>200 cases); a hard lower bound catches
    # regressions where a future schema change silently drops cases.
    assert len(cases) > 100
    # Each case has a non-empty id and at least one step (loader invariant).
    for tc in cases:
        assert tc.id, "test case has empty id"
        assert tc.steps, f"{tc.id} has no steps"
