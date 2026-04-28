"""YAML test-case loader, schema-compatible with mash-go's loader.

The Go schema (``mash-go/internal/testharness/loader/types.go``) drives
the canonical shape of every ``mash-go/testdata/cases/*.yaml`` file. This
module mirrors it field-for-field so the same YAML files parse here
without modification — which is required for Phase 21 acceptance
("at least 5 test cases pass identically under Go and Python harnesses").

Multi-document YAML support is required: many files in the suite hold
multiple ``TestCase`` documents separated by ``---``. Empty / comment-only
documents are silently skipped, matching ``loader.ParseTestCases``.

PICS file loading is delegated to :mod:`mash_py.testharness.pics`.
"""

from __future__ import annotations

import fnmatch
import os
from collections.abc import Iterable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml


class LoadError(Exception):
    """Raised when a YAML file cannot be parsed or fails schema validation."""

    def __init__(self, message: str, *, path: str | None = None) -> None:
        super().__init__(f"{path}: {message}" if path else message)
        self.message = message
        self.path = path


@dataclass(slots=True)
class Step:
    """A single step in a test case (mirrors Go's ``loader.Step``)."""

    action: str
    params: dict[str, Any] = field(default_factory=dict)
    expect: dict[str, Any] = field(default_factory=dict)
    timeout: str = ""
    description: str = ""
    store_result: str = ""

    @classmethod
    def from_yaml(cls, raw: Any, *, ctx: str) -> Step:
        if not isinstance(raw, dict):
            raise LoadError(f"{ctx}: step must be a mapping, got {type(raw).__name__}")
        action = raw.get("action")
        if not isinstance(action, str) or not action:
            raise LoadError(f"{ctx}: step is missing required 'action' field")
        return cls(
            action=action,
            params=_as_dict(raw.get("params"), ctx=f"{ctx}.params"),
            expect=_as_dict(raw.get("expect"), ctx=f"{ctx}.expect"),
            timeout=str(raw.get("timeout") or ""),
            description=str(raw.get("description") or ""),
            store_result=str(raw.get("store_result") or ""),
        )


# Conditions are loosely-typed mappings (e.g. ``device_in_commissioning_mode: true``);
# the runner inspects them by key, so a plain dict is enough.
Condition = dict[str, Any]


@dataclass(slots=True)
class TestCase:
    """One YAML document under ``testdata/cases/`` (mirrors ``loader.TestCase``)."""

    # Tell pytest not to collect this dataclass as a test class.
    __test__ = False

    id: str
    name: str = ""
    description: str = ""
    pics_requirements: list[str] = field(default_factory=list)
    preconditions: list[Condition] = field(default_factory=list)
    steps: list[Step] = field(default_factory=list)
    postconditions: list[Condition] = field(default_factory=list)
    timeout: str = ""
    tags: list[str] = field(default_factory=list)
    connection_tier: str = ""
    skip: bool = False
    skip_reason: str = ""

    @classmethod
    def from_yaml(cls, raw: Any, *, ctx: str = "<doc>") -> TestCase:
        if not isinstance(raw, dict):
            raise LoadError(
                f"{ctx}: test case must be a mapping, got {type(raw).__name__}"
            )
        tc_id = raw.get("id")
        if not isinstance(tc_id, str) or not tc_id:
            raise LoadError(f"{ctx}: test case is missing required 'id'")

        steps_raw = raw.get("steps") or []
        if not isinstance(steps_raw, list) or not steps_raw:
            raise LoadError(f"{ctx} ({tc_id}): must have at least one step")

        steps: list[Step] = []
        for i, s in enumerate(steps_raw):
            steps.append(Step.from_yaml(s, ctx=f"{ctx}.{tc_id}.steps[{i}]"))

        return cls(
            id=tc_id,
            name=str(raw.get("name") or ""),
            description=str(raw.get("description") or ""),
            pics_requirements=_normalize_pics_requirements(raw.get("pics_requirements")),
            preconditions=_as_condition_list(raw.get("preconditions")),
            steps=steps,
            postconditions=_as_condition_list(raw.get("postconditions")),
            timeout=str(raw.get("timeout") or ""),
            tags=_as_str_list(raw.get("tags")),
            connection_tier=str(raw.get("connection_tier") or ""),
            skip=bool(raw.get("skip") or False),
            skip_reason=str(raw.get("skip_reason") or ""),
        )


# ---------------------------------------------------------------------------
# PICSFile — re-exported from .pics so callers see a unified surface
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class PICSFile:
    """Protocol Implementation Conformance Statement (parsed form).

    Stored as ``items: dict[str, bool | int | str]``. Boolean items use
    Python ``bool``; numeric items use ``int``; everything else is ``str``.
    Mirrors Go's ``loader.PICSFile`` shape closely enough that the
    requirement-checker can be a one-to-one port.
    """

    name: str = ""
    items: dict[str, Any] = field(default_factory=dict)


# ---------------------------------------------------------------------------
# Loaders
# ---------------------------------------------------------------------------


def parse_test_cases(data: bytes | str, *, source: str = "<bytes>") -> list[TestCase]:
    """Parse one or more YAML documents into :class:`TestCase` objects.

    Empty / comment-only documents are silently dropped (matches Go's
    ``ParseTestCases``). A document with no ``id`` is treated as empty.
    """
    cases: list[TestCase] = []
    docs = list(yaml.safe_load_all(data))
    for i, doc in enumerate(docs):
        if doc is None:
            continue
        if not isinstance(doc, dict):
            raise LoadError(
                f"document {i} must be a mapping, got {type(doc).__name__}",
                path=source,
            )
        if not doc.get("id"):
            # Comment-only / empty document.
            continue
        try:
            cases.append(TestCase.from_yaml(doc, ctx=f"{source}#doc{i}"))
        except LoadError as exc:
            raise LoadError(exc.message, path=source) from exc
    return cases


def load_test_cases(path: str | os.PathLike[str]) -> list[TestCase]:
    """Load all test cases from a single YAML file."""
    p = Path(path)
    try:
        data = p.read_bytes()
    except OSError as exc:
        raise LoadError(f"failed to read file: {exc}", path=str(p)) from exc
    return parse_test_cases(data, source=str(p))


def load_test_directory(
    directory: str | os.PathLike[str],
    *,
    files_filter: str = "",
    recursive: bool = False,
) -> list[TestCase]:
    """Load every ``.yaml`` / ``.yml`` file in a directory.

    ``files_filter`` is a comma-separated list of glob patterns matched
    against the filename stem, mirroring mash-go's ``-files`` flag. Empty
    means "load all files".
    """
    base = Path(directory)
    if not base.is_dir():
        raise LoadError("not a directory", path=str(base))

    patterns = _parse_file_filter(files_filter)
    out: list[TestCase] = []

    paths: Iterable[Path]
    if recursive:
        paths = sorted(p for p in base.rglob("*") if p.is_file())
    else:
        paths = sorted(p for p in base.iterdir() if p.is_file())

    for p in paths:
        ext = p.suffix.lower()
        if ext not in (".yaml", ".yml"):
            continue
        if patterns and not _matches_any(p.stem, patterns):
            continue
        out.extend(load_test_cases(p))
    return out


# ---------------------------------------------------------------------------
# Filtering helpers
# ---------------------------------------------------------------------------


def filter_by_pattern(cases: Iterable[TestCase], pattern: str) -> list[TestCase]:
    """Filter cases by ID using fnmatch-style glob (mirrors Go's ``-filter``)."""
    if not pattern:
        return list(cases)
    return [tc for tc in cases if fnmatch.fnmatch(tc.id, pattern)]


def filter_by_tags(
    cases: Iterable[TestCase],
    *,
    include: str = "",
    exclude: str = "",
) -> list[TestCase]:
    """Filter cases by tag membership.

    Both ``include`` and ``exclude`` are comma-separated tag lists. A test
    must have *all* include tags (intersection) and *none* of the exclude
    tags. This matches mash-go's behavior for the ``-tags`` and
    ``-exclude-tags`` flags.
    """
    inc = {t.strip() for t in include.split(",") if t.strip()}
    exc = {t.strip() for t in exclude.split(",") if t.strip()}
    out: list[TestCase] = []
    for tc in cases:
        tags = set(tc.tags)
        if inc and not inc.issubset(tags):
            continue
        if exc and tags & exc:
            continue
        out.append(tc)
    return out


# ---------------------------------------------------------------------------
# Internals
# ---------------------------------------------------------------------------


def _as_dict(raw: Any, *, ctx: str) -> dict[str, Any]:
    if raw is None:
        return {}
    if not isinstance(raw, dict):
        raise LoadError(f"{ctx}: expected mapping, got {type(raw).__name__}")
    return dict(raw)


def _as_str_list(raw: Any) -> list[str]:
    if raw is None:
        return []
    if not isinstance(raw, list):
        return []
    return [str(item) for item in raw]


def _as_condition_list(raw: Any) -> list[Condition]:
    if raw is None:
        return []
    if not isinstance(raw, list):
        return []
    out: list[Condition] = []
    for item in raw:
        if isinstance(item, dict):
            out.append(dict(item))
    return out


def _normalize_pics_requirements(raw: Any) -> list[str]:
    """Normalize the ``pics_requirements`` list.

    YAML allows mixed entries: plain strings (``"MASH.S.COMM"``) and maps
    (``"MASH.S.WINDOW: 900"``). The Go loader flattens both into strings;
    we do the same so the checker only ever sees string keys.
    """
    if raw is None:
        return []
    if not isinstance(raw, list):
        return []
    out: list[str] = []
    for item in raw:
        if isinstance(item, str):
            out.append(item)
        elif isinstance(item, dict):
            for k, v in item.items():
                out.append(f"{k}: {v}")
        else:
            out.append(str(item))
    return out


def _parse_file_filter(raw: str) -> list[str]:
    if not raw:
        return []
    return [s.strip() for s in raw.split(",") if s.strip()]


def _matches_any(stem: str, patterns: list[str]) -> bool:
    return any(fnmatch.fnmatch(stem, p) for p in patterns)


__all__ = [
    "Condition",
    "LoadError",
    "PICSFile",
    "Step",
    "TestCase",
    "filter_by_pattern",
    "filter_by_tags",
    "load_test_cases",
    "load_test_directory",
    "parse_test_cases",
]
