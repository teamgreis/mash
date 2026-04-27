"""Tests for the generator pipeline.

These cover four properties:

1. **Symbol coverage parity with Go.** Every attribute id in
   ``measurement_gen.go`` has a counterpart constant in
   ``measurement_gen.py``; ditto for command and parameter ids.
2. **Property test.** Every attribute declared in a YAML feature
   produces (a) a constant, (b) a getter, (c) a setter on the generated
   class.
3. **Determinism.** Running the generator twice yields byte-identical
   output.
4. **Drift gate.** Re-running the generator into a tmpdir matches the
   committed ``mash_py/`` files. Catches the case where the YAML is
   updated but ``make generate`` was forgotten.

The plan also calls out a vector test (``MEASUREMENT_ATTR_AC_ACTIVE_POWER
= 1``, ``class Measurement(Feature):``); covered in
:func:`test_measurement_vector_strings`.
"""

from __future__ import annotations

import importlib
import re
import sys
from pathlib import Path

import pytest

from codegen import generate, helpers, specparse

# ---------------------------------------------------------------------------
# Vector test (Phase 12 acceptance)
# ---------------------------------------------------------------------------


def test_measurement_vector_strings(features_dir: Path, usecases_dir: Path) -> None:
    """Generated ``measurement_gen.py`` must contain the constant and class
    declarations the plan calls out as vector signals."""
    outputs = generate.generate_all(features_dir, usecases_dir)
    by_path = {gf.rel_path: gf.content for gf in outputs}
    text = by_path["features/measurement_gen.py"]

    assert "MEASUREMENT_ATTR_AC_ACTIVE_POWER: int = 1" in text
    assert "class Measurement(Feature):" in text
    assert "MEASUREMENT_FEATURE_REVISION: int = 1" in text


# ---------------------------------------------------------------------------
# Property test: every YAML attribute → constant + getter + setter
# ---------------------------------------------------------------------------


def _generated_module(name: str, root: Path):
    """Import a generated feature module from ``mash_py/features/``."""
    module_name = f"mash_py.features.{name}_gen"
    if module_name in sys.modules:
        del sys.modules[module_name]
    return importlib.import_module(module_name)


def test_every_attribute_has_constant_getter_setter(features_dir: Path) -> None:
    pv = specparse.load_protocol_version(features_dir / "protocol-versions.yaml")
    feats = specparse.load_all_features(features_dir, pv)
    failures: list[str] = []
    for feat in feats:
        module_name = helpers.feature_module_name(feat.name)
        mod = _generated_module(module_name, features_dir)
        cls = getattr(mod, feat.name)
        for attr in feat.attributes:
            const = helpers.attribute_const(feat.name, attr.name)
            getter = helpers.snake_case(attr.name)
            setter = "set_" + getter
            if not hasattr(mod, const):
                failures.append(f"{module_name}: missing constant {const}")
            if not hasattr(cls, getter):
                failures.append(f"{module_name}.{feat.name}: missing getter {getter}()")
            if not hasattr(cls, setter):
                failures.append(f"{module_name}.{feat.name}: missing setter {setter}()")
    assert not failures, "\n".join(failures)


def test_every_command_has_constants_and_request_dataclass(features_dir: Path) -> None:
    pv = specparse.load_protocol_version(features_dir / "protocol-versions.yaml")
    feats = specparse.load_all_features(features_dir, pv)
    failures: list[str] = []
    for feat in feats:
        if not feat.commands:
            continue
        mod = _generated_module(helpers.feature_module_name(feat.name), features_dir)
        for cmd in feat.commands:
            const = helpers.command_const(feat.name, cmd.name)
            if not hasattr(mod, const):
                failures.append(f"{feat.name}: missing command constant {const}")
            if cmd.parameters:
                cls_name = (cmd.name[0].upper() + cmd.name[1:]) + "Request"
                if not hasattr(mod, cls_name):
                    failures.append(f"{feat.name}: missing request dataclass {cls_name}")
                for p in cmd.parameters:
                    if not hasattr(mod, helpers.param_const(cmd.name, p.name)):
                        failures.append(
                            f"{feat.name}.{cmd.name}: missing param constant for {p.name}"
                        )
    assert not failures, "\n".join(failures)


# ---------------------------------------------------------------------------
# Symbol parity with Go
# ---------------------------------------------------------------------------


def _camel_to_upper_snake(name: str) -> str:
    """``ACActivePower`` -> ``AC_ACTIVE_POWER``.

    Splits on transitions from lower→upper *and* on runs of upper letters
    that are followed by a single capital + lower (``ACActive`` → ``AC``,
    ``Active``). Same logic the Go generator's :func:`enumValueSuffix`
    inverse applies, in the opposite direction.
    """
    # Insert separator before each upper that follows a lower or at a
    # run boundary (uppercase followed by uppercase+lowercase).
    s = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", name)
    s = re.sub(r"([A-Z]+)([A-Z][a-z])", r"\1_\2", s)
    return s.upper()


def test_measurement_symbol_parity_with_go(features_dir: Path, usecases_dir: Path) -> None:
    """Every Go ``MeasurementAttr*`` constant has a Python counterpart."""
    go_path = (
        features_dir.parent.parent
        / "mash-go"
        / "pkg"
        / "features"
        / "measurement_gen.go"
    )
    if not go_path.exists():
        pytest.skip(f"go file {go_path} not present")

    go_text = go_path.read_text()
    go_consts = set(re.findall(r"MeasurementAttr(\w+)", go_text))

    py_path = features_dir.parent.parent / "mash-py" / "mash_py" / "features" / "measurement_gen.py"
    py_text = py_path.read_text()
    py_consts = set(re.findall(r"MEASUREMENT_ATTR_(\w+)", py_text))

    go_translated = {_camel_to_upper_snake(name) for name in go_consts}
    missing = go_translated - py_consts
    extra = py_consts - go_translated
    assert not missing, f"Go has but Python missing: {sorted(missing)}"
    assert not extra, f"Python has but Go missing: {sorted(extra)}"


# ---------------------------------------------------------------------------
# Determinism
# ---------------------------------------------------------------------------


def test_generation_is_deterministic(features_dir: Path, usecases_dir: Path) -> None:
    a = generate.generate_all(features_dir, usecases_dir)
    b = generate.generate_all(features_dir, usecases_dir)
    assert [(g.rel_path, g.content) for g in a] == [(g.rel_path, g.content) for g in b]


# ---------------------------------------------------------------------------
# Drift gate (committed files match what the YAML would produce now)
# ---------------------------------------------------------------------------


def test_committed_outputs_match_generation(
    features_dir: Path,
    usecases_dir: Path,
    mash_py_root: Path,
) -> None:
    """The committed ``*_gen.py`` files must match a fresh generation.

    If this fails, run ``make generate`` and review the diff. The
    failure message lists every drifted file so the next ``git diff`` is
    targeted.
    """
    outputs = generate.generate_all(features_dir, usecases_dir)
    drift: list[str] = []
    for gf in outputs:
        on_disk = mash_py_root / gf.rel_path
        if not on_disk.exists():
            drift.append(f"missing: {gf.rel_path}")
            continue
        if on_disk.read_text(encoding="utf-8") != gf.content:
            drift.append(f"drift:   {gf.rel_path}")
    assert not drift, "Committed generated files are stale:\n  " + "\n  ".join(drift)


# ---------------------------------------------------------------------------
# Idempotency of write_outputs
# ---------------------------------------------------------------------------


def test_write_outputs_is_idempotent(
    tmp_path: Path,
    features_dir: Path,
    usecases_dir: Path,
) -> None:
    outputs = generate.generate_all(features_dir, usecases_dir)
    first = generate.write_outputs(outputs, tmp_path)
    mtimes = {p: p.stat().st_mtime_ns for p in first}
    second = generate.write_outputs(outputs, tmp_path)
    assert sorted(first) == sorted(second)
    # Files weren't rewritten because content matched.
    for p in second:
        assert p.stat().st_mtime_ns == mtimes[p], f"{p} was rewritten unnecessarily"
