# Copyright Solace Corporation
#
# Use of this software is governed by the Business Source License (BUSL-1.1)
# included in the LICENSE file.

"""
Python code executor tool for the STR.

Allows an agent's LLM to generate and execute Python code in a sandboxed
environment with access to input artifacts and the ability to produce
output artifacts.
"""

import builtins
import gc
import io
import os
import re
import shutil
import stat
import sys
import tempfile
import traceback
import warnings
from typing import List, Optional, Set, Tuple

from sam_tool_sdk import (
    tool_cli,
    Artifact,
    SandboxToolContextFacade,
    ToolResult,
    DataObject,
    DataDisposition,
)

class _ArtifactProxy:
    """Lightweight proxy exposing Artifact data to user code.

    Provides a clean interface for the LLM-generated code to access
    artifact content without exposing the full Artifact internals.
    """

    def __init__(self, artifact: Artifact):
        self._artifact = artifact
        self.filename = artifact.filename
        self.mime_type = artifact.mime_type
        self.content = artifact.as_bytes()

    def as_text(self, encoding: str = "utf-8") -> str:
        """Get artifact content as a string."""
        return self._artifact.as_text(encoding)

    def as_bytes(self, encoding: str = "utf-8") -> bytes:
        """Get artifact content as bytes."""
        return self._artifact.as_bytes(encoding)

    def as_file(self) -> io.BytesIO:
        """Get artifact content as a seekable file object.

        A fresh buffer per call, so repeated reads of the same artifact each
        start at offset 0. That copies the payload, so hold the result in a
        variable rather than calling this once per row or sheet.
        """
        return io.BytesIO(self.content)

    def __repr__(self) -> str:
        size = len(self.content) if self.content else 0
        return f"Artifact({self.filename!r}, {self.mime_type!r}, {size} bytes)"

_BYTES_NOT_FILE_PATTERN = re.compile(
    r"'bytes' object has no attribute 'seek'|does not support b['\"]"
)

def _missing_artifact_hint(
    exc: BaseException, python_code: str, artifacts_dict: dict
) -> Optional[str]:
    """Hint for an INPUT_ARTIFACTS subscript whose file was never staged."""
    if not isinstance(exc, KeyError):
        return None
    missing_key = exc.args[0] if exc.args else None
    if not isinstance(missing_key, str) or missing_key in artifacts_dict:
        return None
    referenced_as_artifact = re.search(
        r"INPUT_ARTIFACTS\s*\[\s*[\"']" + re.escape(missing_key) + r"[\"']\s*\]",
        python_code,
    )
    if not referenced_as_artifact:
        return None
    loaded = sorted(artifacts_dict)
    return (
        f"HINT: INPUT_ARTIFACTS currently contains {loaded}. To read a "
        f"file you MUST pass its name in the input_artifacts argument of "
        f"execute_python_code (e.g. input_artifacts=[{exc}]) — INPUT_ARTIFACTS "
        f"is not auto-populated. Re-call the tool with input_artifacts set."
    )

def _bytes_not_file_hint(exc: BaseException, python_code: str) -> Optional[str]:
    """Hint for a library handed raw bytes where it needs a file object."""
    if not re.search(r"INPUT_ARTIFACTS\s*\[", python_code):
        return None
    if ".as_file()" in python_code:
        return None
    if not _BYTES_NOT_FILE_PATTERN.search(str(exc)):
        return None
    return (
        "HINT: that library needs a seekable file object, not raw bytes. The "
        "artifact is NOT corrupt and its format is fine — only the argument type "
        "is wrong. INPUT_ARTIFACTS[...].as_file() returns a file object, e.g. "
        'openpyxl.load_workbook(INPUT_ARTIFACTS["book.xlsx"].as_file()). Re-run '
        "the same code with .as_file() in place of .as_bytes() or .content — do "
        "NOT switch libraries or parse the file format by hand."
    )

MAX_WRITTEN_FILES = 20
MAX_WRITTEN_BYTES = 50 * 1024 * 1024
MAX_LISTED_SKIPS = 20
RESERVED_ARTIFACT_SUFFIXES = (".metadata.json", ".converted.txt")
RESERVED_ARTIFACT_NAMES = ("project_bm25_index.zip",)
USER_SCOPE_PREFIX = "user:"

def _store_name_problem(name: str) -> Optional[str]:
    """Why the artifact store would not keep this file under this name, if it would not.

    The store replaces every character outside printable ASCII with "_", so two
    such names collapse into one artifact. It hides internal artifacts (the
    reserved suffixes and names) from listings, and reads a "user:" prefix as a
    scope rather than part of the name.
    """
    if not all(0x20 <= ord(ch) < 0x7F for ch in name):
        return "the name has non-ASCII or control characters; use an ASCII name"
    if (
        name.endswith(RESERVED_ARTIFACT_SUFFIXES)
        or name in RESERVED_ARTIFACT_NAMES
        or name.startswith(USER_SCOPE_PREFIX)
    ):
        return "the name is reserved by the artifact store"
    return None

def _shown(name: str) -> str:
    """A file name as it can appear in the result without breaking its lines."""
    return name if name.isprintable() else name.encode("unicode_escape").decode("ascii")

def _collect_written_files(
    work_dir: str, saved_names: Set[str]
) -> Tuple[List[DataObject], List[Tuple[str, str]]]:
    """Return the files the code wrote into work_dir as artifacts.

    Models often write a result with df.to_csv("x.csv") and then refer to x.csv
    in a template or a later call, so a file that is not returned is a file the
    agent believes exists but cannot read. Names already passed to
    save_artifact() keep that call's version.

    Only regular files directly in work_dir are taken: artifact names cannot
    contain a path separator, and the STR collects a flat output directory.
    Collection never fails the run; anything it cannot take is reported with
    the reason instead.
    """
    collected: List[DataObject] = []
    skipped: List[Tuple[str, str]] = []
    total = 0
    saved_folded = {s.casefold() for s in saved_names}
    try:
        entries = sorted(os.scandir(work_dir), key=lambda e: e.name)
    except OSError as exc:
        return collected, [(".", f"working directory unreadable: {exc.strerror}")]
    for entry in entries:
        name = entry.name
        if name.startswith(".") or name == "__pycache__" or name in saved_names:
            continue
        if name.casefold() in saved_folded:
            skipped.append((name, "differs only in letter case from a file passed to save_artifact()"))
            continue
        problem = _store_name_problem(name)
        if problem:
            skipped.append((name, problem))
            continue
        try:
            st = entry.stat(follow_symlinks=False)
        except OSError as exc:
            skipped.append((name, exc.strerror or "unreadable"))
            continue
        if stat.S_ISDIR(st.st_mode):
            skipped.append((name + "/", "a folder; write files directly in the working directory"))
            continue
        if not stat.S_ISREG(st.st_mode):
            skipped.append((name, "not a regular file"))
            continue
        if len(collected) >= MAX_WRITTEN_FILES:
            skipped.append((name, f"over {MAX_WRITTEN_FILES} files"))
            continue
        if total + st.st_size > MAX_WRITTEN_BYTES:
            skipped.append((name, f"over {MAX_WRITTEN_BYTES // (1024 * 1024)} MiB in total"))
            continue
        try:
            with open(entry.path, "rb") as fh:
                content = fh.read()
        except OSError as exc:
            skipped.append((name, exc.strerror or "unreadable"))
            continue
        total += len(content)
        collected.append(
            DataObject(
                name=name,
                content=content,
                mime_type="application/octet-stream",
                disposition=DataDisposition.ARTIFACT,
            )
        )
    return collected, skipped

def _written_files_note(collected: List[DataObject], skipped: List[Tuple[str, str]]) -> str:
    lines = []
    if collected:
        lines.append(
            "Saved the files the code wrote as artifacts: "
            + ", ".join(d.name for d in collected)
        )
    if skipped:
        shown = "; ".join(f"{_shown(name)} ({why})" for name, why in skipped[:MAX_LISTED_SKIPS])
        more = len(skipped) - MAX_LISTED_SKIPS
        if more > 0:
            shown += f"; and {more} more"
        lines.append(
            f"Not saved: {shown}. To return one, write it directly in the working directory "
            "under an ASCII name, or pass its content to save_artifact()."
        )
    return "\n".join(lines)

async def execute_python_code(
    python_code: str,
    input_artifacts: Optional[List[Artifact]] = None,
    tool_context: SandboxToolContextFacade = None,
) -> ToolResult:
    """Run arbitrary Python in a sandbox for any computation — calculations, data generation, analysis/statistics, simulations, format conversion, and chart/file creation (pandas, numpy, scipy, scikit-learn, matplotlib, sympy, faker, and more; no network). Self-contained code needs only python_code. To read an EXISTING artifact, list its filename in input_artifacts (e.g. input_artifacts=["sales.csv"]) and access it via INPUT_ARTIFACTS["sales.csv"]; input_artifacts is optional — omit it when your code reads no artifacts.

    OPTIONAL — only when reading artifacts: list each filename your code accesses
    via INPUT_ARTIFACTS[...] in the `input_artifacts` argument of THIS tool call.
    INPUT_ARTIFACTS contains exactly the files you list there (empty if you pass
    none). If your code does INPUT_ARTIFACTS["sales.csv"], include
    input_artifacts=["sales.csv"] or you get KeyError: 'sales.csv'.

    The code runs with these globals available:

    INPUT_ARTIFACTS - dict of filename -> artifact object, containing exactly
      the files named in the `input_artifacts` argument (empty if you pass none).
      Each artifact has: .content (bytes), .filename, .mime_type, .as_text(),
      .as_bytes(), .as_file()
      Use .as_file() — a seekable file object — for every library that opens a
      file: openpyxl, docx (python-docx), pdfplumber, zipfile, PIL, pandas.
      Passing raw bytes fails in all of them except pandas.read_excel, which
      still accepts bytes but warns. openpyxl is the trap: given bytes it says
      "does not support b'...' file format", which reads as a corrupt file when
      only the argument type is wrong. The b'...' is the tell — a message like
      "does not support the old .xls file format" is a genuine format complaint
      and .as_file() will not help there (use xlrd for real .xls).

    save_artifact(filename, content, mime_type="application/octet-stream", description=None)
      Create an output artifact. content can be str or bytes.

    Files the code writes directly in its working directory (e.g.
    df.to_csv("top.csv")) are also returned as artifacts under the same name, so
    a template or a later call can use them: up to 20 files and 50 MiB per call,
    ASCII names only. Hidden files and __pycache__ are ignored, and a name also
    passed to save_artifact() keeps the save_artifact() version. Other files that
    are not returned (such as files in folders, links, non-ASCII names, and files
    past the limits) are listed in the result with the reason. If the code raises an exception, no files
    are returned.

    send_status(message) - send a progress update to the user

    print() - stdout is captured and returned as the text result

    Importable besides the standard library: numpy, pandas, scipy, matplotlib,
    seaborn, sklearn, statsmodels, sympy, networkx, PIL, openpyxl, xlsxwriter,
    xlrd, docx, pdfplumber, fpdf, lxml, bs4, yaml, jsonschema, pydantic, regex,
    tabulate, faker, wordcloud, dateutil. There is no network and no pip install.
    Use save_artifact() to return a file under a name you choose.

    Example - read an Excel workbook
      (tool call: python_code=<below>, input_artifacts=["book.xlsx"]):
        import openpyxl
        wb = openpyxl.load_workbook(INPUT_ARTIFACTS["book.xlsx"].as_file())
        ws = wb.active
        print(f"{ws.title}: {ws.max_row} rows x {ws.max_column} cols")
        for row in ws.iter_rows(max_row=5, values_only=True):
            print(row)

    Args:
        python_code: The Python code to execute.
        input_artifacts: Optional list of artifact filenames the code reads via
            INPUT_ARTIFACTS[...] (e.g. ["sales.csv"]). Omit it for code that
            reads no artifacts. Listing them explicitly is clearest and most
            reliable; the runtime also auto-detects referenced names that exist
            in the session as a fallback. Append ":<version>" to pin a version.
    """
    if not python_code or not python_code.strip():
        return ToolResult.error("No Python code provided")

    artifacts_dict = {}
    for art in (input_artifacts or []):
        artifacts_dict[art.filename] = _ArtifactProxy(art)

    output_artifacts: List[DataObject] = []

    def save_artifact(
        filename: str,
        content,
        mime_type: str = "application/octet-stream",
        description: Optional[str] = None,
    ):
        """Create an output artifact to return to the agent."""
        if isinstance(content, str):
            content = content.encode("utf-8")
            if mime_type == "application/octet-stream":
                mime_type = "text/plain"
        elif not isinstance(content, (bytes, bytearray)):
            raise TypeError(
                f"save_artifact content must be str or bytes, got {type(content).__name__}"
            )
        output_artifacts.append(
            DataObject(
                name=filename,
                content=content,
                mime_type=mime_type,
                disposition=DataDisposition.ARTIFACT,
                description=description,
            )
        )

    def send_status(message: str):
        """Send a progress update to the user."""
        if tool_context:
            tool_context.send_status(str(message))

    stdout_buf = io.StringIO()
    stderr_buf = io.StringIO()

    namespace = {
        "INPUT_ARTIFACTS": artifacts_dict,
        "save_artifact": save_artifact,
        "send_status": send_status,
        "__builtins__": builtins.__dict__,
    }

    old_stdout, old_stderr = sys.stdout, sys.stderr
    old_cwd = os.getcwd()
    work_dir = tempfile.mkdtemp(prefix="pyexec-")
    try:
        try:
            sys.stdout = stdout_buf
            sys.stderr = stderr_buf
            os.chdir(work_dir)

            compiled = compile(python_code, "<user_code>", "exec")
            exec(compiled, namespace)

        except Exception as exc:
            sys.stdout, sys.stderr = old_stdout, old_stderr
            tb = traceback.format_exc()
            captured_out = stdout_buf.getvalue()
            msg = f"Code execution failed.\n\nTraceback:\n{tb}"
            if captured_out:
                msg = f"Output before error:\n{captured_out}\n\n{msg}"
            hint = _missing_artifact_hint(
                exc, python_code, artifacts_dict
            ) or _bytes_not_file_hint(exc, python_code)
            if hint:
                msg += f"\n\n{hint}"
            return ToolResult.error(msg)
        finally:
            sys.stdout, sys.stderr = old_stdout, old_stderr
            os.chdir(old_cwd)

        with warnings.catch_warnings():
            warnings.simplefilter("ignore", ResourceWarning)
            namespace.clear()
            gc.collect()
        written, skipped = _collect_written_files(
            work_dir, {d.name for d in output_artifacts}
        )
    finally:
        shutil.rmtree(work_dir, ignore_errors=True)
    output_artifacts.extend(written)

    stdout_text = stdout_buf.getvalue()
    stderr_text = stderr_buf.getvalue()

    parts = []
    if stdout_text:
        parts.append(stdout_text.rstrip())
    if stderr_text:
        parts.append(f"[stderr]\n{stderr_text.rstrip()}")
    note = _written_files_note(written, skipped)
    if note:
        parts.append(note)

    message = "\n\n".join(parts) if parts else "Code executed successfully (no output)."

    return ToolResult.ok(message, data_objects=output_artifacts)

cli = tool_cli(execute_python_code)
