"""Build-time code generator for mash-py.

Reads YAML feature, shared, protocol, and use case definitions from
``docs/`` and emits Python modules under ``mash_py/features/``,
``mash_py/model/``, and ``mash_py/usecase/``.

The generated files are committed; CI re-runs the generator into a
tmpdir and fails if any committed ``*_gen.py`` file drifts from what
the YAML would produce now.
"""

from __future__ import annotations

from codegen.generate import generate_all

__all__ = ["generate_all"]
