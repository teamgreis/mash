"""Jinja2 templates used by the Python code generator.

Templates are kept as ``.j2`` files in this directory and loaded by
:mod:`codegen.generate` via :class:`jinja2.PackageLoader`. Editing a
template is a *spec-output* change — re-run ``make generate`` to refresh
the committed ``*_gen.py`` files and review the diff against the YAML
source to confirm the change is intentional.
"""
