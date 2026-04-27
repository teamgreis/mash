"""Use case registry and definition types.

Use case definitions describe how features compose into observable
device behavior (LPC, EVC, GPL, etc.). The generated
:mod:`mash_py.usecase.definitions_gen` populates the registry from
``docs/usecases/<version>/*.yaml``.

Mirrors ``mash-go/pkg/usecase/`` at the API level. The Go package keeps
``UseCaseName`` (string) and ``UseCaseID`` (uint16) separate; mash-py
unifies both into :class:`mash_py.model.UseCaseType` (an
:class:`~enum.IntEnum`).
"""

from __future__ import annotations

from mash_py.usecase.definitions_gen import ID_TO_NAME, NAME_TO_ID, REGISTRY
from mash_py.usecase.types import (
    AttributeRequirement,
    CommandRequirement,
    FeatureRequirement,
    ScenarioDef,
    UseCaseDef,
)

__all__ = [
    "ID_TO_NAME",
    "NAME_TO_ID",
    "REGISTRY",
    "AttributeRequirement",
    "CommandRequirement",
    "FeatureRequirement",
    "ScenarioDef",
    "UseCaseDef",
]
