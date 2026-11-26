# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Fail clearly when an installed agent framework no longer has a hook or event an adapter is written against.

The adapters are written against fast-moving hook APIs. When a framework renames or drops a hook, it simply stops
calling it, so an adapter would go on running and report nothing (or the wrong thing). Each adapter therefore checks,
before it imports anything from its framework, that every symbol it depends on is still there, and raises
FrameworkDrift with the framework, the installed version and the missing symbols.

A symbol may list alternatives, ``"pkg:Old.attr | pkg:New.attr"``, for a hook a framework renamed or moved between
releases the adapter supports: it is present when any of them is, and `resolve` hands the adapter whichever one the
installed release has. That is how an adapter keeps an older release working instead of raising its floor.

A framework that is not installed at all still raises the usual ImportError.
"""

from __future__ import annotations

import dataclasses
import importlib
from collections.abc import Iterable
from importlib import metadata
from typing import Any

_MISSING = object()


class FrameworkDrift(RuntimeError):
    """An installed framework lacks a hook, event or field a sam_link_sdk adapter depends on."""

    def __init__(self, adapter: str, framework: str, version: str, tested: str, missing: list[str]):
        self.adapter, self.framework, self.version, self.tested, self.missing = (
            adapter,
            framework,
            version,
            tested,
            missing,
        )
        super().__init__(
            f"{adapter} cannot use {framework} {version}: it no longer has {', '.join(missing)}. "
            f"The adapter is tested with {framework} {tested}; install a tested version or update sam-link-sdk."
        )


def installed_version(distribution: str) -> str:
    try:
        return metadata.version(distribution)
    except metadata.PackageNotFoundError:
        return "(version unknown)"


def _has_member(owner: Any, name: str) -> Any:
    """The member `name` of `owner`, or a missing marker.

    A class has fields that only exist on its instances (dataclass fields with a default factory, pydantic model
    fields), so those count as present.
    """
    value = getattr(owner, name, _MISSING)
    if value is not _MISSING or not isinstance(owner, type):
        return value
    if dataclasses.is_dataclass(owner) and any(f.name == name for f in dataclasses.fields(owner)):
        return None
    if name in (getattr(owner, "model_fields", None) or {}):
        return None
    for klass in owner.__mro__:
        if name in getattr(klass, "__annotations__", {}):
            return None
    return _MISSING


def _alternatives(symbol: str) -> list[str]:
    return [part.strip() for part in symbol.split("|")]


def _lookup(symbol: str) -> Any:
    """What one ``"module:attr.attr"`` names, or a missing marker."""
    module_name, _, path = symbol.partition(":")
    try:
        target: Any = importlib.import_module(module_name)
    except ImportError:
        return _MISSING
    for part in path.split(".") if path else ():
        target = _has_member(target, part)
        if target is _MISSING:
            return _MISSING
    return target


def resolves(symbol: str) -> bool:
    """Whether ``"module:attr.attr"``, or any of its ``|`` alternatives, names something that exists (a class's
    declared fields count)."""
    return any(_lookup(alternative) is not _MISSING for alternative in _alternatives(symbol))


def resolve(symbol: str) -> Any:
    """What the first of the ``|`` alternatives the installed framework has names. LookupError when it has none."""
    for alternative in _alternatives(symbol):
        target = _lookup(alternative)
        if target is not _MISSING:
            return target
    raise LookupError(f"none of {symbol} exists in the installed framework")


def require(adapter: str, framework: str, distribution: str, tested: str, symbols: Iterable[str]) -> None:
    """Raise FrameworkDrift naming every symbol in `symbols` (``"module:attr.attr"``) the installed framework lacks.

    The first symbol's module is imported plainly first (any of its alternatives' modules will do), so a framework that
    is not installed raises ImportError.
    """
    symbols = list(symbols)
    if symbols:
        _import_any(_alternatives(symbols[0]))
    missing = [s.replace(":", ".") for s in symbols if not resolves(s)]
    if missing:
        raise FrameworkDrift(adapter, framework, installed_version(distribution), tested, missing)


def _import_any(alternatives: list[str]) -> None:
    error: ImportError | None = None
    for alternative in alternatives:
        try:
            importlib.import_module(alternative.partition(":")[0])
            return
        except ImportError as raised:
            error = error or raised
    if error is not None:
        raise error


def require_members(
    adapter: str, framework: str, distribution: str, tested: str, owner: Any, label: str, names: Iterable[str]
) -> None:
    """At first use: raise FrameworkDrift if the object the framework handed over lacks any of `names`."""
    missing = [f"{label}.{n}" for n in names if _has_member(owner, n) is _MISSING]
    if missing:
        raise FrameworkDrift(adapter, framework, installed_version(distribution), tested, missing)
