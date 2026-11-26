# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The public API is pinned to tests/fixtures/public_api.json: the names each public module exports, each callable's
parameters (names, kinds, defaults) and each class's public instance attributes. A change to it must be deliberate, with a CHANGELOG entry and a version decision
(CONTRIBUTING.md, "Versioning and deprecation")."""

from __future__ import annotations

import ast
import dataclasses
import enum
import importlib
import inspect
import json
import os
import pathlib
import pkgutil
import re
import textwrap

import pytest

import sam_link_sdk

SNAPSHOT = pathlib.Path(__file__).parent / "fixtures" / "public_api.json"
UPDATE = "SAM_LINK_SDK_UPDATE_API_SNAPSHOT"

WITH_ALL = (
    "sam_link_sdk",
    "sam_link_sdk.adk",
    "sam_link_sdk.agent",
    "sam_link_sdk.agent_framework",
    "sam_link_sdk.agentcore",
    "sam_link_sdk.audit",
    "sam_link_sdk.context",
    "sam_link_sdk.crewai",
    "sam_link_sdk.delegation",
    "sam_link_sdk.events",
    "sam_link_sdk.foundry",
    "sam_link_sdk.hermes",
    "sam_link_sdk.langchain",
    "sam_link_sdk.link",
    "sam_link_sdk.metrics",
    "sam_link_sdk.openai_agents",
    "sam_link_sdk.prompting",
    "sam_link_sdk.stop",
    "sam_link_sdk.strands",
)
"""Every module with an __all__, and the documented modules that have none (their public names are the classes and
functions defined in them). A new module with an __all__ has to be added here or marked internal with an underscore.
"""
WITHOUT_ALL = (
    "sam_link_sdk.config",
    "sam_link_sdk.otel_steps",
    "sam_link_sdk.redact",
)
PUBLIC_MODULES = WITH_ALL + WITHOUT_ALL

KNOWN_PRIVATE_TYPE_LEAKS = {"sam_link_sdk.otel_steps.SamLinkSpanProcessor(run: _Run)"}
"""Public names whose signature shows a private type today; each is a decision for a later change, not to grow."""

MESSAGE = """The public API of sam-link-sdk changed (below). If that is intended:
  1. add an entry to the Unreleased section of CHANGELOG.md (a removal or change of a signature is breaking),
  2. decide the version: before 1.0 a breaking change needs a new minor, a patch never changes the public API,
  3. regenerate the snapshot and commit it with the change:
       {update}=1 uv run --locked pytest tests/test_public_api.py
If it is not intended, undo the change (a private name starts with an underscore).

{diff}"""


def _default(value: object) -> str:
    text = repr(value)
    return f"<{type(value).__name__}>" if re.search(r" at 0x[0-9a-f]+", text) else text


def _signature(function: object) -> list | str:
    try:
        parameters = inspect.signature(function).parameters.values()
    except (TypeError, ValueError):
        return "<no signature>"
    return [[p.name, p.kind.name, "<required>" if p.default is p.empty else _default(p.default)] for p in parameters]


def _callable(function: object) -> dict:
    entry: dict = {"parameters": _signature(function)}
    if inspect.iscoroutinefunction(function):
        entry["async"] = True
    return entry


def _private(qualified: str) -> bool:
    return any(part.startswith("_") for part in qualified.split("."))


def _attributes(cls: type) -> list[str]:
    """The public attributes an instance gets: a dataclass's fields, and every ``self.<name> = ...`` in the methods of
    the class and its sam_link_sdk bases."""
    names = {f.name for f in dataclasses.fields(cls)} if dataclasses.is_dataclass(cls) else set()
    for klass in cls.__mro__:
        if not klass.__module__.startswith("sam_link_sdk"):
            continue
        for raw in vars(klass).values():
            function = raw.__func__ if isinstance(raw, (staticmethod, classmethod)) else raw
            if not inspect.isfunction(function):
                continue
            try:
                tree = ast.parse(textwrap.dedent(inspect.getsource(function)))
            except (OSError, TypeError, SyntaxError):
                continue
            for node in ast.walk(tree):
                targets = (
                    node.targets
                    if isinstance(node, ast.Assign)
                    else [node.target]
                    if isinstance(node, (ast.AnnAssign, ast.AugAssign))
                    else []
                )
                for target in targets:
                    for leaf in ast.walk(target):
                        if (
                            isinstance(leaf, ast.Attribute)
                            and isinstance(leaf.value, ast.Name)
                            and leaf.value.id == "self"
                        ):
                            names.add(leaf.attr)
    return sorted(n for n in names if not n.startswith("_"))


def _class(cls: type) -> dict:
    bases = [f"{b.__module__}.{b.__qualname__}" for b in cls.__bases__]
    entry: dict = {"kind": "class", "bases": [b for b in bases if not _private(b)]}
    if issubclass(cls, enum.Enum):
        entry["members"] = [m.name for m in cls]
    own_init = any("__init__" in vars(k) for k in cls.__mro__ if k.__module__.startswith("sam_link_sdk"))
    if own_init or "__init__" in vars(cls):
        entry["init"] = _signature(cls)
    members: dict = {}
    for klass in reversed(cls.__mro__):
        if not klass.__module__.startswith("sam_link_sdk"):
            continue
        for name, raw in vars(klass).items():
            if name.startswith("_"):
                continue
            if isinstance(raw, property):
                members[name] = {"property": True}
            elif isinstance(raw, (staticmethod, classmethod)):
                members[name] = {**_callable(raw.__func__), "decorator": type(raw).__name__}
            elif inspect.isfunction(raw):
                members[name] = _callable(raw)
    if members:
        entry["methods"] = members
    attributes = [a for a in _attributes(cls) if a not in members]
    if attributes:
        entry["attributes"] = attributes
    return entry


def _describe(value: object) -> dict:
    if inspect.isclass(value):
        return _class(value)
    if inspect.isroutine(value):
        return {"kind": "function", **_callable(value)}
    if isinstance(value, (str, int, float, bool)) or value is None:
        return {"kind": "constant", "value": value}
    return {"kind": "object", "type": type(value).__name__}


def _exported(module) -> list[str]:
    if hasattr(module, "__all__"):
        return sorted(module.__all__)
    return sorted(
        name
        for name, value in vars(module).items()
        if not name.startswith("_")
        and (inspect.isclass(value) or inspect.isfunction(value))
        and value.__module__ == module.__name__
    )


def current_api() -> dict:
    api = {}
    for name in PUBLIC_MODULES:
        module = importlib.import_module(name)
        api[name] = {export: _describe(getattr(module, export)) for export in _exported(module)}
    return api


def _flat(value: object, path: str = "") -> dict:
    if isinstance(value, dict):
        return {k: v for key, item in value.items() for k, v in _flat(item, f"{path}.{key}" if path else key).items()}
    return {path: value}


def _diff(old: dict, new: dict) -> str:
    lines = []
    for module in sorted(set(old) | set(new)):
        before, after = old.get(module, {}), new.get(module, {})
        for name in sorted(set(before) | set(after)):
            if name not in after:
                lines.append(f"removed  {module}.{name}")
            elif name not in before:
                lines.append(f"added    {module}.{name}")
            elif before[name] != after[name]:
                was, now = _flat(before[name]), _flat(after[name])
                for key in sorted(set(was) | set(now)):
                    if was.get(key, "<absent>") != now.get(key, "<absent>"):
                        lines.append(
                            f"changed  {module}.{name} {key}: {was.get(key, '<absent>')} -> {now.get(key, '<absent>')}"
                        )
    return "\n".join(lines)


def _dump(api: dict) -> str:
    text = json.dumps(api, indent=1, sort_keys=True)
    one_line = re.compile(
        r"\[\s*((?:\"(?:[^\"\\\n]|\\.)*\"|true|false|null|-?\d+(?:\.\d+)?)"
        r"(?:,\s*(?:\"(?:[^\"\\\n]|\\.)*\"|true|false|null|-?\d+(?:\.\d+)?))*)\s*\]"
    )
    return one_line.sub(lambda m: "[" + re.sub(r",\s+", ", ", m.group(1)) + "]", text) + "\n"


def test_the_public_api_matches_the_snapshot():
    api = current_api()
    if os.environ.get(UPDATE):
        SNAPSHOT.write_text(_dump(api))
        return
    pinned = json.loads(SNAPSHOT.read_text())
    assert api == pinned, MESSAGE.format(update=UPDATE, diff=_diff(pinned, api))


def test_every_module_with_an_all_is_pinned():
    found = {"sam_link_sdk"}
    for info in pkgutil.walk_packages(sam_link_sdk.__path__, "sam_link_sdk."):
        if any(part.startswith("_") for part in info.name.split(".")):
            continue
        if hasattr(importlib.import_module(info.name), "__all__"):
            found.add(info.name)
    assert found == set(WITH_ALL), (
        f"modules with an __all__ and a public name must be in WITH_ALL (and so in the snapshot): "
        f"{sorted(found ^ set(WITH_ALL))}"
    )


@pytest.mark.parametrize("name", WITH_ALL)
def test_everything_in_an_all_exists_and_is_listed_once(name):
    module = importlib.import_module(name)
    assert [n for n in module.__all__ if not hasattr(module, n)] == []
    assert len(module.__all__) == len(set(module.__all__))


@pytest.mark.parametrize("name", PUBLIC_MODULES)
def test_no_public_signature_names_a_private_type(name):
    module = importlib.import_module(name)
    leaks = []
    for export in _exported(module):
        value = getattr(module, export)
        targets = (
            [value]
            if inspect.isroutine(value)
            else (
                [
                    v
                    for k, v in vars(value).items()
                    if inspect.isfunction(v) and (k == "__init__" or not k.startswith("_"))
                ]
                if inspect.isclass(value)
                else []
            )
        )
        for target in targets:
            for parameter in inspect.signature(target).parameters.values():
                if parameter.annotation is parameter.empty:
                    continue
                text = f"{parameter.annotation}"
                if re.search(r"(?<![\w.])_[A-Za-z]\w*", text) or re.search(r"\._[A-Za-z]", text):
                    leaks.append(f"{name}.{export}({parameter.name}: {text})")
    assert [leak for leak in leaks if leak not in KNOWN_PRIVATE_TYPE_LEAKS] == []


def test_a_snapshot_regeneration_is_never_on_in_ci():
    assert not os.environ.get(UPDATE) or not os.environ.get("CI")
