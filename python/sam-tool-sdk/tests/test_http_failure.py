# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""An uncaught exception carrying an HTTP status comes back as a coded tool error.

``requests.HTTPError`` and ``httpx.HTTPStatusError`` both expose the status as
``exc.response.status_code``; the fakes below reproduce that shape and each
library's message format without importing either library.
"""

from __future__ import annotations

import re
import traceback
from http import HTTPStatus
from pathlib import Path
from typing import Any, Dict, List, Optional

import pytest

from sam_tool_sdk.cli import _run_class_tool, _run_function_tool, _run_provider_tool
from sam_tool_sdk.dynamic_tool import DynamicTool, DynamicToolProvider
from sam_tool_sdk.http_failure import (
    _MAX_DETAIL_CHARS,
    execution_failure,
    http_failure_code,
)
from sam_tool_sdk.tool_result import TOOL_RESULT_SCHEMA_VERSION
from sam_tool_sdk.tool_runner import run_tool

_REPO_ROOT = Path(__file__).resolve().parents[3]
_GO_TABLE = _REPO_ROOT / "internal" / "tool" / "http_failure_test.go"

class _Response:
    def __init__(self, status_code: Any) -> None:
        self.status_code = status_code

    def __bool__(self) -> bool:
        """Falsy for a failure status, as ``requests.Response`` is."""
        return not (isinstance(self.status_code, int) and self.status_code >= 400)

class _FakeHTTPError(Exception):
    def __init__(self, message: str, status_code: Any) -> None:
        super().__init__(message)
        self.response = _Response(status_code)

def _requests_error(
    status: int, url: str = "https://api.example.com/v1/items"
) -> _FakeHTTPError:
    return _FakeHTTPError(f"{status} Client Error: Reason for url: {url}", status)

def _failure(exc: BaseException) -> Dict[str, Any]:
    try:
        raise exc
    except BaseException as e:
        return execution_failure(e)

@pytest.mark.parametrize(
    ("status", "code"),
    [
        (400, "DISPATCH_BACKEND_4XX"),
        (401, "DISPATCH_AUTH_REQUIRED"),
        (403, "PERMISSION_DENIED"),
        (404, "DISPATCH_BACKEND_4XX"),
        (408, "DISPATCH_TIMEOUT"),
        (429, "RATE_LIMITED"),
        (499, "DISPATCH_BACKEND_4XX"),
        (500, "DISPATCH_BACKEND_5XX"),
        (503, "DISPATCH_BACKEND_5XX"),
        (504, "DISPATCH_TIMEOUT"),
        (599, "DISPATCH_BACKEND_5XX"),
    ],
)
def test_http_status_becomes_coded_error_result(status: int, code: str) -> None:
    out = _failure(_requests_error(status))

    assert list(out) == ["result"]
    result = out["result"]
    assert result["_schema"] == "ToolResult"
    assert result["_schema_version"] == TOOL_RESULT_SCHEMA_VERSION
    assert result["status"] == "error"
    assert result["error_code"] == code
    assert result["message"] == (
        f"HTTP {status}: {status} Client Error: Reason for url: https://api.example.com/v1/items"
    )

def test_401_is_never_the_oauth_code() -> None:
    assert http_failure_code(401) != "AUTH_REQUIRED"

def _go_http_failure_cases() -> List[tuple]:
    table = _GO_TABLE.read_text()
    rows = re.findall(r'\{(http\.Status(\w+)|\d+), "([A-Z0-9_]*)"\}', table)
    assert len(rows) == len(re.findall(r"^\s*\{.*\},\s*$", table, re.MULTILINE)), (
        "a TestHTTPFailureCode row is in a form this test cannot read; update the regex"
    )
    cases = []
    for literal, status_name, code in rows:
        if status_name:
            status = HTTPStatus[
                re.sub(r"(?<=[a-z])(?=[A-Z])", "_", status_name).upper()
            ].value
        else:
            status = int(literal)
        cases.append((status, code or None))
    return cases

@pytest.mark.skipif(
    not (_REPO_ROOT / "go.mod").exists(),
    reason="Go source not present (installed from sdist)",
)
def test_mapping_matches_go_http_failure_code_table() -> None:
    assert _GO_TABLE.exists(), f"{_GO_TABLE} moved; point this test at the Go table"
    cases = _go_http_failure_cases()

    assert len(cases) >= 10, "Go TestHTTPFailureCode table not parsed; update the regex"
    for status, code in cases:
        assert http_failure_code(status) == code, status

@pytest.mark.parametrize(
    "exc",
    [
        RuntimeError("boom"),
        _FakeHTTPError("not a failure", 302),
        _FakeHTTPError("too high", 600),
        _FakeHTTPError("string status", "403"),
        _FakeHTTPError("float status", 403.0),
        _FakeHTTPError("bool status", True),
        _FakeHTTPError("no status", None),
    ],
    ids=["no-response", "3xx", "600", "str", "float", "bool", "none"],
)
def test_exception_without_http_failure_status_keeps_uncoded_error(
    exc: Exception,
) -> None:
    try:
        raise exc
    except Exception as e:
        out = execution_failure(e)
        expected = {"error": f"Execution error: {e}\n{traceback.format_exc()}"}

    assert out == expected

class _RaisingResponse:
    @property
    def status_code(self) -> int:
        raise ValueError("status unavailable")

def test_status_lookup_that_raises_keeps_uncoded_error() -> None:
    exc = RuntimeError("boom")
    exc.response = _RaisingResponse()  # type: ignore[attr-defined]

    out = _failure(exc)

    assert list(out) == ["error"]
    assert out["error"].startswith("Execution error: boom\n")

def test_long_exception_text_is_capped_before_redaction() -> None:
    url = "https://api.example.com/p"
    padding = "x" * (_MAX_DETAIL_CHARS - len(f" {url}?k=s3") - 1)
    many_urls = ",".join(f'"https://example.com/items/{i}"' for i in range(20000))
    text = f"{padding} {url}?k=s3cret tail {many_urls}"

    message = _failure(_FakeHTTPError(text, 500))["result"]["message"]

    assert message == f"HTTP 500: {padding} {url} [truncated]"

def test_url_cut_inside_its_authority_drops_the_authority() -> None:
    url = "https://user:s3cret@api.example.com/v1"
    prefix = "x" * (_MAX_DETAIL_CHARS - len("https://user:s3"))

    message = _failure(_FakeHTTPError(f"{prefix}{url}", 401))["result"]["message"]

    assert "user" not in message
    assert "s3" not in message.replace("x", "")
    assert message.endswith("https:// [truncated]")

def test_exception_with_response_but_no_status_code_keeps_uncoded_error() -> None:
    exc = RuntimeError("boom")
    exc.response = object()  # type: ignore[attr-defined]

    assert list(_failure(exc)) == ["error"]

@pytest.mark.parametrize(
    ("text", "redacted"),
    [
        (
            "403 Client Error: Forbidden for url: https://api.example.com/v1/items?api_key=s3cret&q=1",
            "403 Client Error: Forbidden for url: https://api.example.com/v1/items",
        ),
        (
            "Client error '429 Too Many Requests' for url 'https://api.example.com/v1?token=s3cret'\n"
            "For more information check: https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/429",
            "Client error '429 Too Many Requests' for url 'https://api.example.com/v1'\n"
            "For more information check: https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/429",
        ),
        (
            "redirected from http://a.example/x?k=1 to http://b.example/y#access_token=s3cret",
            "redirected from http://a.example/x to http://b.example/y",
        ),
        (
            "Client error '401 Unauthorized' for url "
            "'https://api.example.com/o'neil/x?q=O'Brien&api_key=s3cret'",
            "Client error '401 Unauthorized' for url 'https://api.example.com/o'neil/x'",
        ),
        (
            "401 Client Error: Unauthorized for url: https://user:s3cret@api.example.com/x?k=1",
            "401 Client Error: Unauthorized for url: https://api.example.com/x",
        ),
    ],
    ids=["requests", "httpx", "query-and-fragment", "apostrophe", "userinfo"],
)
def test_message_drops_url_query_strings(text: str, redacted: str) -> None:
    out = _failure(_FakeHTTPError(text, 403))

    message = out["result"]["message"]
    assert message == f"HTTP 403: {redacted}"
    assert "s3cret" not in message

def test_message_has_no_traceback() -> None:
    out = _failure(_requests_error(500))

    assert "Traceback" not in out["result"]["message"]

_RAISED = _requests_error(403, "https://api.example.com/v1/items?api_key=s3cret")
_EXPECTED_MESSAGE = (
    "HTTP 403: 403 Client Error: Reason for url: https://api.example.com/v1/items"
)

def _runner_args(tmp_path: Path) -> Dict[str, Any]:
    (tmp_path / "out").mkdir(exist_ok=True)
    return {
        "args": {},
        "tool_config": {},
        "artifact_metadata": {},
        "artifact_paths": {},
        "status_pipe": "",
        "output_dir": str(tmp_path / "out"),
        "user_id": "u",
        "session_id": "s",
        "app_name": "app",
        "tool_oauth_token": "",
        "task_id": "t",
        "ipc_socket": "",
        "volume_mounts": [],
    }

def _assert_coded_403(out: Dict[str, Any]) -> None:
    assert list(out) == ["result"], out
    assert out["result"]["status"] == "error"
    assert out["result"]["error_code"] == "PERMISSION_DENIED"
    assert out["result"]["message"] == _EXPECTED_MESSAGE

def http_failing_tool(tool_context=None) -> str:
    """Fails with an HTTP 403."""
    raise _RAISED

class _HTTPFailingTool(DynamicTool):
    @property
    def tool_name(self) -> str:
        return "http_failing"

    @property
    def tool_description(self) -> str:
        return "Fails with an HTTP 403."

    @property
    def parameters_schema(self) -> dict:
        return {"type": "object", "properties": {}, "required": []}

    async def _run_async_impl(
        self, args: dict, tool_context=None, credential: Optional[str] = None
    ) -> dict:
        raise _RAISED

class _HTTPFailingProvider(DynamicToolProvider):
    def create_tools(self, tool_config=None) -> List[DynamicTool]:
        return [_HTTPFailingTool(tool_config=tool_config)]

def test_function_tool_path(tmp_path: Path) -> None:
    _assert_coded_403(_run_function_tool(http_failing_tool, _runner_args(tmp_path)))

def test_class_tool_path(tmp_path: Path) -> None:
    _assert_coded_403(_run_class_tool(_HTTPFailingTool, _runner_args(tmp_path)))

def test_provider_tool_path(tmp_path: Path) -> None:
    _assert_coded_403(
        _run_provider_tool(_HTTPFailingProvider, "http_failing", _runner_args(tmp_path))
    )

def test_run_tool_path(tmp_path: Path) -> None:
    runner = _runner_args(tmp_path)
    runner["module"] = __name__
    runner["function"] = "http_failing_tool"

    _assert_coded_403(run_tool(runner))

def test_run_tool_path_non_http_exception_keeps_uncoded_error(tmp_path: Path) -> None:
    runner = _runner_args(tmp_path)
    runner["module"] = __name__
    runner["function"] = "plain_failing_tool"

    out = run_tool(runner)

    assert list(out) == ["error"]
    assert out["error"].startswith(
        "Execution error: boom\nTraceback (most recent call last):"
    )

def plain_failing_tool(tool_context=None) -> str:
    """Fails without an HTTP status."""
    raise RuntimeError("boom")
