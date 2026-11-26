# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Tests for recursive nested-object schema generation.

Mirrors the Go SDK's ``goTypeToSchema`` behavior in
``pkg/samtoolsdk/schema.go``: nested ``TypedDict`` / ``@dataclass`` /
pydantic ``BaseModel`` params must emit
``{type:"object", properties:{...}, required:[...]}`` instead of the
flat ``{type:"object"}`` the previous walker produced.
"""

from __future__ import annotations

import dataclasses
from typing import List, Optional, TypedDict

import pytest

from sam_tool_sdk.dynamic_tool import _type_to_schema

class _ContactTD(TypedDict):
    name: str
    age: int
    tags: List[str]

@dataclasses.dataclass
class _ContactDC:
    name: str
    age: int
    nickname: Optional[str] = None

def test_typeddict_emits_nested_properties() -> None:
    schema = _type_to_schema(_ContactTD)
    assert schema == {
        "type": "object",
        "properties": {
            "name": {"type": "string"},
            "age": {"type": "integer"},
            "tags": {"type": "array", "items": {"type": "string"}},
        },
        "required": ["name", "age", "tags"],
    }

def test_dataclass_emits_nested_properties_and_required() -> None:
    schema = _type_to_schema(_ContactDC)
    assert schema is not None
    assert schema["type"] == "object"
    assert schema["properties"]["name"] == {"type": "string"}
    assert schema["properties"]["age"] == {"type": "integer"}
    assert schema["properties"]["nickname"] == {"type": "string"}
    assert schema["required"] == ["name", "age"]

def test_list_of_typeddict_recurses() -> None:
    schema = _type_to_schema(List[_ContactTD])
    assert schema is not None
    assert schema["type"] == "array"
    assert schema["items"]["type"] == "object"
    assert "name" in schema["items"]["properties"]
    assert "age" in schema["items"]["properties"]

def test_optional_unwraps_to_inner_type() -> None:
    schema = _type_to_schema(Optional[int])
    assert schema == {"type": "integer"}

def test_dict_stays_flat_object() -> None:
    schema = _type_to_schema(dict)
    assert schema == {"type": "object"}

def test_pydantic_basemodel_emits_nested_properties() -> None:
    pydantic = pytest.importorskip("pydantic")

    class Address(pydantic.BaseModel):
        street: str
        zip: Optional[str] = None

    schema = _type_to_schema(Address)
    assert schema is not None
    assert schema["type"] == "object"
    assert schema["properties"]["street"] == {"type": "string"}
    assert schema["properties"]["zip"] == {"type": "string"}
    assert schema["required"] == ["street"]
