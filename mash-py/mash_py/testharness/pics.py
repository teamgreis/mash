"""PICS file loader & requirement checker (subset port of mash-go pics).

The Go reference parses a custom DSL (``MASH.S.COMM = true`` style) via
``internal/pics``. mash-py only needs to *consume* PICS files, not author
them, and our YAML cases don't need PICS at all to satisfy Phase 21
acceptance — but we still implement loading + filtering so the harness
behaves identically when a user passes ``-pics``.

Two on-disk formats are supported:

1. **YAML** — ``items:`` map of ``KEY: value``. This is what Go writes
   when authoring a PICS file (``loader.PICSFile`` round-trips it).
2. **Plain text DSL** — one ``KEY=value`` (or ``KEY: value``) per line,
   blank lines and ``#`` comments ignored. Mirrors the Go ``pics`` parser.

Boolean values: ``true``/``false`` (case-insensitive), ``1``/``0``.
Numeric values become ``int``. Everything else stays ``str``.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any

import yaml

from mash_py.testharness.loader import LoadError, PICSFile


def parse_pics(data: bytes | str, *, name: str = "") -> PICSFile:
    """Parse a PICS file from bytes (auto-detects YAML vs plain text)."""
    text = data.decode("utf-8") if isinstance(data, bytes) else data
    text = text.strip()
    if not text:
        return PICSFile(name=name)

    # YAML form starts with ``device:`` or ``items:`` (the two top-level
    # keys Go writes). Plain DSL never has those tokens at column zero.
    is_yaml = any(text.startswith(prefix) for prefix in ("device:", "items:", "---"))
    if is_yaml:
        try:
            doc = yaml.safe_load(text)
        except yaml.YAMLError as exc:
            raise LoadError(f"failed to parse PICS YAML: {exc}") from exc
        if not isinstance(doc, dict):
            raise LoadError("PICS YAML root must be a mapping")
        items = doc.get("items") or {}
        if not isinstance(items, dict):
            raise LoadError("PICS YAML 'items' must be a mapping")
        return PICSFile(name=name, items={str(k): _coerce(v) for k, v in items.items()})

    # Plain DSL fallback.
    items: dict[str, Any] = {}
    for line_no, line in enumerate(text.splitlines(), start=1):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if "=" in line:
            key, _, value = line.partition("=")
        elif ":" in line:
            key, _, value = line.partition(":")
        else:
            raise LoadError(f"line {line_no}: expected 'KEY=value' or 'KEY: value'")
        items[key.strip()] = _coerce(value.strip())
    return PICSFile(name=name, items=items)


def load_pics(path: str | os.PathLike[str]) -> PICSFile:
    """Load a PICS file from disk (auto-detects YAML vs plain text)."""
    p = Path(path)
    try:
        data = p.read_bytes()
    except OSError as exc:
        raise LoadError(f"failed to read PICS: {exc}", path=str(p)) from exc
    pf = parse_pics(data, name=p.stem)
    if not pf.name:
        pf.name = p.stem
    return pf


def check_pics_requirements(pf: PICSFile | None, requirements: list[str]) -> bool:
    """Return True iff every requirement is satisfied by ``pf``.

    Supports the three forms documented in mash-go's
    ``loader.CheckPICSRequirements``::

        "MASH.S.COMM"                 -> exists and is truthy
        "MASH.S.WINDOW_MIN: 180"      -> exists and equals 180
        "MASH.S.DISC.BROWSE_TIMEOUT=10" -> same, ``=`` separator

    A ``None`` PICS file means "no PICS configured" — every requirement
    is treated as satisfied (matches mash-go's ``runner.go`` when
    ``-pics`` and ``-setup-code`` are both absent).
    """
    if pf is None:
        return True
    for req in requirements:
        key, want, has_value = _parse_requirement(req)
        value = pf.items.get(key)
        if value is None:
            return False
        if has_value:
            if not _match_value(value, want):
                return False
        else:
            if isinstance(value, bool) and not value:
                return False
    return True


def filter_test_cases(cases, pf: PICSFile | None):
    """Return cases whose PICS requirements are met by ``pf`` (mirrors Go)."""
    return [tc for tc in cases if check_pics_requirements(pf, tc.pics_requirements)]


# ---------------------------------------------------------------------------
# Internals
# ---------------------------------------------------------------------------


def _coerce(value: Any) -> Any:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return value
    if not isinstance(value, str):
        return str(value)
    s = value.strip()
    if not s:
        return ""
    low = s.lower()
    if low in ("true", "yes"):
        return True
    if low in ("false", "no"):
        return False
    # Try int (preserve "1.3" etc as strings — see Go loader's special case).
    try:
        return int(s)
    except ValueError:
        pass
    return s


def _parse_requirement(req: str) -> tuple[str, str, bool]:
    if ": " in req:
        key, _, val = req.partition(": ")
        return key.strip(), val.strip(), True
    if "=" in req:
        key, _, val = req.partition("=")
        return key.strip(), val.strip(), True
    return req.strip(), "", False


def _match_value(actual: Any, want: str) -> bool:
    if isinstance(actual, bool):
        return (
            (want == "true" and actual)
            or (want == "false" and not actual)
            or (want == "1" and actual)
            or (want == "0" and not actual)
        )
    if isinstance(actual, int):
        try:
            return actual == int(want)
        except ValueError:
            return False
    if isinstance(actual, float):
        try:
            return actual == float(want)
        except ValueError:
            return False
    return str(actual) == want


__all__ = [
    "check_pics_requirements",
    "filter_test_cases",
    "load_pics",
    "parse_pics",
]
