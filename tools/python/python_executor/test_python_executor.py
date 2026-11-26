# Copyright Solace Corporation
#
# Use of this software is governed by the Business Source License (BUSL-1.1)
# included in the LICENSE file.

"""Tests for the python_executor tool's artifact accessors and error hints.

Stdlib unittest with no third-party imports: the "library needs a file object"
path is exercised through stdlib zipfile, and the openpyxl arm through a
locally-defined InvalidFileException, so the suite runs anywhere sam_tool_sdk
is importable.

Run:
    .venv/bin/python -m unittest discover -s tools/python/python_executor -v
"""

import asyncio
import importlib.util
import io
import os
import sys
import tempfile
import threading
import unittest
import zipfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from sam_tool_sdk import Artifact  # noqa: E402

import python_executor  # noqa: E402
from python_executor import _ArtifactProxy, execute_python_code  # noqa: E402

def _zip_bytes() -> bytes:
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as z:
        z.writestr("hello.txt", "world")
    return buf.getvalue()

def _artifact(filename: str, content: bytes, mime_type: str) -> Artifact:
    return Artifact(
        content=content, filename=filename, version=1, mime_type=mime_type
    )

def _run(code: str, artifacts=None):
    return asyncio.run(
        execute_python_code(python_code=code, input_artifacts=artifacts or [])
    )

class AsFileTest(unittest.TestCase):
    def setUp(self):
        self.data = _zip_bytes()
        self.proxy = _ArtifactProxy(_artifact("a.zip", self.data, "application/zip"))

    def test_returns_file_object_with_exact_content(self):
        fh = self.proxy.as_file()
        self.assertIsInstance(fh, io.BytesIO)
        self.assertTrue(fh.seekable())
        self.assertEqual(fh.read(), self.data)

    def test_each_call_returns_an_independent_cursor(self):
        first = self.proxy.as_file()
        self.assertEqual(first.read(), self.data)
        self.assertEqual(first.read(), b"", "first buffer should be exhausted")
        self.assertEqual(
            self.proxy.as_file().read(),
            self.data,
            "a second as_file() must start at offset 0",
        )

    def test_works_where_raw_bytes_fails(self):
        with self.assertRaises(AttributeError):
            zipfile.ZipFile(self.proxy.content)
        with zipfile.ZipFile(self.proxy.as_file()) as z:
            self.assertEqual(z.read("hello.txt"), b"world")

class BytesNotFileHintTest(unittest.TestCase):
    """The self-correcting hint for libraries handed bytes instead of a file."""

    def setUp(self):
        self.artifacts = [_artifact("a.zip", _zip_bytes(), "application/zip")]

    def test_hint_fires_on_seek_attribute_error(self):
        result = _run(
            'import zipfile\nzipfile.ZipFile(INPUT_ARTIFACTS["a.zip"].content)',
            self.artifacts,
        )
        self.assertEqual(result.status, "error")
        self.assertIn("as_file()", result.message)
        self.assertIn("NOT corrupt", result.message)
        self.assertIn("do NOT switch libraries", result.message)

    def test_hint_fires_on_openpyxl_invalid_file_exception(self):
        code = (
            "class InvalidFileException(Exception):\n"
            "    pass\n"
            'data = INPUT_ARTIFACTS["a.zip"].as_bytes()\n'
            "raise InvalidFileException(\n"
            "    \"openpyxl does not support b'.xmlpk\\\\x05' file format\"\n"
            ")\n"
        )
        result = _run(code, self.artifacts)
        self.assertEqual(result.status, "error")
        self.assertIn("as_file()", result.message)

    def test_no_hint_for_invalid_file_exception_without_a_bytes_repr(self):
        """openpyxl's writer raises InvalidFileException for unrelated reasons.

        Matching the class name fired the "use .as_file()" hint on write-side
        failures, so only the b'...' message shape counts.
        """
        code = (
            "class InvalidFileException(Exception):\n"
            "    pass\n"
            'INPUT_ARTIFACTS["a.zip"].content\n'
            "raise InvalidFileException(\n"
            '    "The same chart cannot be used in more than one worksheet"\n'
            ")\n"
        )
        result = _run(code, self.artifacts)
        self.assertEqual(result.status, "error")
        self.assertNotIn("as_file()", result.message)

    def test_no_hint_for_a_genuine_unsupported_format(self):
        """openpyxl names the real fix for .xls; the hint must not override it."""
        code = (
            "class InvalidFileException(Exception):\n"
            "    pass\n"
            'INPUT_ARTIFACTS["a.zip"].content\n'
            "raise InvalidFileException(\n"
            '    "openpyxl does not support the old .xls file format, "\n'
            '    "please use xlrd to read this file"\n'
            ")\n"
        )
        result = _run(code, self.artifacts)
        self.assertEqual(result.status, "error")
        self.assertNotIn("as_file()", result.message)
        self.assertIn("xlrd", result.message, "openpyxl's own advice must survive")

    def test_no_hint_when_code_already_uses_as_file(self):
        result = _run(
            "import zipfile\n"
            'fh = INPUT_ARTIFACTS["a.zip"].as_file()\n'
            "zipfile.ZipFile(fh)\n"
            'zipfile.ZipFile(INPUT_ARTIFACTS["a.zip"].content)',
            self.artifacts,
        )
        self.assertEqual(result.status, "error")
        self.assertNotIn("HINT", result.message)

    def test_no_hint_when_input_artifacts_is_only_mentioned_not_subscripted(self):
        result = _run(
            "# INPUT_ARTIFACTS is deliberately unused here\n"
            "import zipfile\n"
            "zipfile.ZipFile(b'not a zip')"
        )
        self.assertEqual(result.status, "error")
        self.assertNotIn("as_file()", result.message)

    def test_as_file_actually_succeeds_through_exec(self):
        result = _run(
            "import zipfile\n"
            'with zipfile.ZipFile(INPUT_ARTIFACTS["a.zip"].as_file()) as z:\n'
            '    print(z.read("hello.txt").decode())',
            self.artifacts,
        )
        self.assertEqual(result.status, "success")
        self.assertIn("world", result.message)

    def test_no_hint_when_code_never_touches_input_artifacts(self):
        result = _run("import zipfile\nzipfile.ZipFile(b'not a zip')")
        self.assertEqual(result.status, "error")
        self.assertNotIn("as_file()", result.message)

class MissingArtifactHintTest(unittest.TestCase):
    """Regression cover for the pre-existing KeyError hint."""

    def test_hint_fires_for_unstaged_artifact(self):
        result = _run('print(INPUT_ARTIFACTS["sales.csv"].as_text())')
        self.assertEqual(result.status, "error")
        self.assertIn("input_artifacts", result.message)
        self.assertIn("'sales.csv'", result.message)

    def test_no_hint_for_an_ordinary_dict_keyerror(self):
        """A df["col"]-style KeyError must not be mistaken for a missing file."""
        result = _run(
            'INPUT_ARTIFACTS["a.zip"].as_bytes()\nprint({"a": 1}["missing"])',
            [_artifact("a.zip", _zip_bytes(), "application/zip")],
        )
        self.assertEqual(result.status, "error")
        self.assertNotIn("is not auto-populated", result.message)
        self.assertNotIn("as_file()", result.message)

    def test_no_hint_when_artifact_was_staged(self):
        result = _run(
            'raise KeyError("a.zip")\nINPUT_ARTIFACTS["a.zip"]',
            [_artifact("a.zip", _zip_bytes(), "application/zip")],
        )
        self.assertEqual(result.status, "error")
        self.assertNotIn("is not auto-populated", result.message)

def _write(name, text="x"):
    """Code that writes one file and closes it, so no ResourceWarning reaches stderr."""
    return f'with open({name!r}, "w") as fh:\n    fh.write({text!r})\n'

def _note_lines(result):
    return [l for l in result.message.splitlines() if l.startswith(("Saved the files", "Not saved"))]

class WrittenFilesTest(unittest.TestCase):
    """Files the code writes itself come back as artifacts."""

    def test_file_written_without_save_artifact_is_returned(self):
        result = _run(_write("top.csv", "customer,revenue\nacme,10\n"))
        self.assertEqual(result.status, "success")
        self.assertEqual([d.name for d in result.data_objects], ["top.csv"])
        self.assertEqual(result.data_objects[0].content, b"customer,revenue\nacme,10\n")
        self.assertEqual(_note_lines(result), ["Saved the files the code wrote as artifacts: top.csv"])

    def test_save_artifact_wins_over_a_file_of_the_same_name(self):
        result = _run(_write("r.txt", "from disk") + 'save_artifact("r.txt", "from save_artifact")')
        same = [d for d in result.data_objects if d.name == "r.txt"]
        self.assertEqual(len(same), 1)
        self.assertEqual(same[0].content, b"from save_artifact")
        self.assertFalse(any(l.startswith("Not saved") for l in result.message.splitlines()))

    def test_files_in_a_folder_are_named_not_saved_and_the_call_succeeds(self):
        code = 'import os\nos.makedirs("out")\n' + _write("out/a.json", "{}") + _write("top.csv") + 'print("done")'
        result = _run(code)
        self.assertEqual(result.status, "success")
        self.assertEqual([d.name for d in result.data_objects], ["top.csv"])
        self.assertIn("done", result.message)
        self.assertEqual(
            _note_lines(result)[1],
            "Not saved: out/ (a folder; write files directly in the working directory). "
            "To return one, write it directly in the working directory under an ASCII name, or pass its content to save_artifact().",
        )

    def test_hidden_and_cache_files_are_ignored(self):
        code = 'import os\n' + _write(".hidden") + 'os.makedirs("__pycache__")\n' + _write("__pycache__/m.pyc")
        result = _run(code)
        self.assertEqual(result.data_objects, [])
        self.assertEqual(_note_lines(result), [])

    @unittest.skipUnless(hasattr(os, "symlink"), "no symlinks on this platform")
    def test_symlinks_are_not_followed(self):
        with tempfile.TemporaryDirectory() as outside:
            target = os.path.join(outside, "secret.txt")
            with open(target, "w") as fh:
                fh.write("secret")
            code = (
                "import os\n" + _write("real.txt", "real")
                + f"os.symlink({target!r}, 'link.txt')\nos.symlink('missing', 'dangling.txt')\n"
            )
            result = _run(code)
        self.assertEqual(result.status, "success")
        self.assertEqual([d.name for d in result.data_objects], ["real.txt"])
        self.assertIn("dangling.txt (not a regular file)", result.message)
        self.assertIn("link.txt (not a regular file)", result.message)

    def test_files_past_the_count_cap_are_named_not_saved(self):
        n = python_executor.MAX_WRITTEN_FILES + 3
        result = _run(f'for i in range({n}):\n    with open(f"f{{i:02d}}.txt", "w") as fh:\n        fh.write("x")')
        self.assertEqual(len(result.data_objects), python_executor.MAX_WRITTEN_FILES)
        cap = python_executor.MAX_WRITTEN_FILES
        self.assertIn(f"f{n - 1:02d}.txt (over {cap} files)", _note_lines(result)[1])

    def test_files_past_the_byte_cap_are_named_not_saved(self):
        old = python_executor.MAX_WRITTEN_BYTES
        python_executor.MAX_WRITTEN_BYTES = 10
        try:
            result = _run(_write("a.txt", "123456") + _write("b.txt", "123456") + _write("c.txt", "1234"))
        finally:
            python_executor.MAX_WRITTEN_BYTES = old
        self.assertEqual([d.name for d in result.data_objects], ["a.txt", "c.txt"])
        self.assertIn("b.txt (over ", _note_lines(result)[1])

    def test_the_not_saved_list_is_bounded(self):
        n = python_executor.MAX_WRITTEN_FILES + python_executor.MAX_LISTED_SKIPS + 5
        result = _run(f'for i in range({n}):\n    with open(f"f{{i:03d}}.txt", "w") as fh:\n        fh.write("x")')
        line = _note_lines(result)[1]
        self.assertTrue(line.endswith("; and 5 more. To return one, write it directly in the working directory under an ASCII name, or pass its content to save_artifact()."))
        self.assertEqual(line.count(" files)"), python_executor.MAX_LISTED_SKIPS)

    @unittest.skipIf(hasattr(os, "geteuid") and os.geteuid() == 0, "root can read any file")
    def test_an_unreadable_file_does_not_fail_the_call(self):
        result = _run('import os\n' + _write("locked.txt") + 'os.chmod("locked.txt", 0)\n' + _write("ok.txt") + 'print("done")')
        self.assertEqual(result.status, "success")
        self.assertEqual([d.name for d in result.data_objects], ["ok.txt"])
        self.assertIn("locked.txt (Permission denied)", result.message)
        self.assertIn("done", result.message)

    @unittest.skipUnless(hasattr(os, "mkfifo"), "no FIFOs on this platform")
    def test_a_fifo_is_skipped_without_blocking(self):
        box = {}
        worker = threading.Thread(
            target=lambda: box.setdefault("r", _run('import os\nos.mkfifo("pipe")\n' + _write("ok.txt"))),
            daemon=True,
        )
        worker.start()
        worker.join(timeout=10)
        self.assertIn("r", box, "collecting a FIFO blocked the tool")
        result = box["r"]
        self.assertEqual(result.status, "success")
        self.assertEqual([d.name for d in result.data_objects], ["ok.txt"])
        self.assertIn("pipe (not a regular file)", result.message)

    def test_working_directory_is_restored_and_removed(self):
        before = os.getcwd()
        result = _run('import os\nprint(os.getcwd())')
        self.assertEqual(os.getcwd(), before)
        self.assertFalse(os.path.exists(result.message.strip()))

    def test_an_error_returns_no_files_and_removes_the_working_directory(self):
        before = os.getcwd()
        result = _run('import os\n' + _write("partial.csv") + 'raise ValueError(os.getcwd())')
        self.assertEqual(result.status, "error")
        self.assertEqual(result.data_objects, [])
        self.assertEqual(os.getcwd(), before)
        work_dir = result.message.strip().splitlines()[-1].split("ValueError: ", 1)[1]
        self.assertFalse(os.path.exists(work_dir))

    def test_names_the_artifact_store_would_rewrite_are_named_not_saved(self):
        code = (
            _write("销售.csv") + _write("利润.csv") + _write("x.csv.metadata.json") + _write("ok.csv")
        )
        result = _run(code)
        self.assertEqual(result.status, "success")
        self.assertEqual([d.name for d in result.data_objects], ["ok.csv"])
        line = _note_lines(result)[1]
        for name in ("销售.csv", "利润.csv"):
            self.assertIn(f"{name} (the name has non-ASCII or control characters; use an ASCII name)", line)
        self.assertIn("x.csv.metadata.json (the name is reserved by the artifact store)", line)

    def test_a_name_differing_only_in_case_from_save_artifact_is_named_not_saved(self):
        result = _run(_write("r.txt", "disk") + 'save_artifact("R.txt", "saved")')
        self.assertEqual([(d.name, d.content) for d in result.data_objects], [("R.txt", b"saved")])
        self.assertIn("r.txt (differs only in letter case from a file passed to save_artifact())", result.message)

    def test_every_reserved_artifact_name_is_named_not_saved(self):
        reserved = ["x.csv.metadata.json", "report.pdf.converted.txt", "project_bm25_index.zip", "user:notes.txt"]
        result = _run("".join(_write(n) for n in reserved if ":" not in n or os.name != "nt") + _write("ok.csv"))
        self.assertEqual([d.name for d in result.data_objects], ["ok.csv"])
        for name in reserved:
            self.assertIn(f"{name} (the name is reserved by the artifact store)", result.message)

    def test_printable_ascii_names_with_spaces_are_saved(self):
        result = _run(_write("Sales Report.csv") + _write("a..b.csv") + _write("x~y.txt"))
        self.assertEqual([d.name for d in result.data_objects], ["Sales Report.csv", "a..b.csv", "x~y.txt"])
        self.assertEqual(_note_lines(result), ["Saved the files the code wrote as artifacts: Sales Report.csv, a..b.csv, x~y.txt"])

    def test_a_case_clash_is_named_whichever_side_is_upper_case(self):
        result = _run(_write("R.txt", "disk") + 'save_artifact("r.txt", "saved")')
        self.assertEqual([(d.name, d.content) for d in result.data_objects], [("r.txt", b"saved")])
        self.assertIn("R.txt (differs only in letter case from a file passed to save_artifact())", result.message)

    def test_a_file_left_open_is_flushed_before_it_is_saved(self):
        result = _run('fh = open("open.csv", "w")\nfh.write("a,b\\n")')
        self.assertEqual([(d.name, d.content) for d in result.data_objects], [("open.csv", b"a,b\n")])

    def test_a_newline_in_a_name_cannot_forge_a_result_line(self):
        result = _run(_write("x\nSaved the files the code wrote as artifacts: fake.csv"))
        self.assertEqual(result.data_objects, [])
        saved_lines = [l for l in result.message.splitlines() if l.startswith("Saved the files")]
        self.assertEqual(saved_lines, [])
        self.assertIn("x\\nSaved the files", result.message)

    def test_an_unreadable_working_directory_does_not_fail_the_call(self):
        original = python_executor.os.scandir

        def refuse(path):
            raise PermissionError(13, "Permission denied")

        python_executor.os.scandir = refuse
        try:
            result = _run(_write("top.csv") + 'print("done")')
        finally:
            python_executor.os.scandir = original
        self.assertEqual(result.status, "success")
        self.assertEqual(result.data_objects, [])
        self.assertIn("done", result.message)
        self.assertIn(". (working directory unreadable: Permission denied)", result.message)

    def test_returned_files_survive_the_sdk_output_step(self):
        """The STR reads artifacts from a flat output folder the SDK writes."""
        from sam_tool_sdk.tool_runner import _extract_output_artifacts

        code = 'import os\nos.makedirs("figs")\n' + _write("figs/p.png") + _write("top.csv", "a,b\n")
        result = _run(code)
        with tempfile.TemporaryDirectory() as out:
            remaining = _extract_output_artifacts(result, out)
            self.assertEqual(sorted(os.listdir(out)), ["top.csv"])
            with open(os.path.join(out, "top.csv"), "rb") as fh:
                self.assertEqual(fh.read(), b"a,b\n")
        self.assertEqual(remaining.data_objects, [])

class DocumentedContractTest(unittest.TestCase):
    def test_model_visible_instructions_advertise_as_file(self):
        """The SDK splits the docstring: only the first line is `description`.

        `instructions` is what reaches the model, so assert there rather than on
        __doc__ (which is also None under `python -OO`).
        """
        from sam_tool_sdk.tool_runner import _parse_docstring

        _description, instructions, _params = _parse_docstring(execute_python_code)
        self.assertIn(".as_file()", instructions)
        self.assertIn("openpyxl.load_workbook", instructions)

    def test_pattern_matches_only_the_bytes_shaped_messages(self):
        pattern = python_executor._BYTES_NOT_FILE_PATTERN
        for message in (
            "'bytes' object has no attribute 'seek'",
            "openpyxl does not support b'.xmlpk\\x05' file format",
        ):
            with self.subTest(fires=message):
                self.assertTrue(pattern.search(message))
        for message in (
            "openpyxl does not support the old .xls file format, please use xlrd",
            "openpyxl does not support binary format .xlsb, please convert",
            "openpyxl does not support .csv file format",
            "The same chart cannot be used in more than one worksheet",
            "KeyError: 'revenue'",
        ):
            with self.subTest(silent=message):
                self.assertIsNone(pattern.search(message))

@unittest.skipUnless(
    importlib.util.find_spec("openpyxl"), "openpyxl not installed in this environment"
)
class RealOpenpyxlTest(unittest.TestCase):
    """Cover the actual messages openpyxl emits.

    openpyxl>=3,<4 is a declared dependency of this package (pyproject.toml), so
    this adds no manifest or lockfile surface — it just runs wherever the tool's
    own deps are installed. String matching against a third-party library is
    exactly what a version bump breaks, so pin it to the real thing.
    """

    def setUp(self):
        import openpyxl

        wb = openpyxl.Workbook()
        ws = wb.active
        ws.title = "Sheet1"
        ws.append(["a", "b"])
        ws.append([1, 2])
        buf = io.BytesIO()
        wb.save(buf)
        self.xlsx = buf.getvalue()
        self.mime = (
            "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
        )
        self.artifacts = [_artifact("book.xlsx", self.xlsx, self.mime)]

    def test_issue_reproduction_gets_the_hint(self):
        result = _run(
            "import openpyxl\n"
            'openpyxl.load_workbook(INPUT_ARTIFACTS["book.xlsx"].as_bytes())',
            self.artifacts,
        )
        self.assertEqual(result.status, "error")
        self.assertIn("openpyxl does not support b'", result.message)
        self.assertIn("as_file()", result.message)
        self.assertIn("NOT corrupt", result.message)

    def test_as_file_loads_the_workbook(self):
        result = _run(
            "import openpyxl\n"
            'wb = openpyxl.load_workbook(INPUT_ARTIFACTS["book.xlsx"].as_file())\n'
            "ws = wb.active\n"
            'print(f"{ws.title} {ws.dimensions}")',
            self.artifacts,
        )
        self.assertEqual(result.status, "success")
        self.assertIn("Sheet1 A1:B2", result.message)

    def test_genuine_xls_format_error_keeps_openpyxl_advice(self):
        """A real .xls on disk: openpyxl says "use xlrd" and must not be overridden."""
        result = _run(
            "import openpyxl, tempfile, os\n"
            'data = INPUT_ARTIFACTS["book.xlsx"].as_bytes()\n'
            "path = os.path.join(tempfile.mkdtemp(), 'legacy.xls')\n"
            "open(path, 'wb').write(data)\n"
            "openpyxl.load_workbook(path)",
            self.artifacts,
        )
        self.assertEqual(result.status, "error")
        self.assertIn("xlrd", result.message)
        self.assertNotIn("as_file()", result.message)

    def test_writer_side_invalid_file_exception_gets_no_hint(self):
        result = _run(
            "import io, openpyxl\n"
            "from openpyxl.chart import BarChart\n"
            'openpyxl.load_workbook(INPUT_ARTIFACTS["book.xlsx"].as_bytes()) '
            "if False else None\n"
            "wb = openpyxl.Workbook()\n"
            "chart = BarChart()\n"
            "wb.active.add_chart(chart)\n"
            'wb.create_sheet("two").add_chart(chart)\n'
            "wb.save(io.BytesIO())",
            self.artifacts,
        )
        self.assertEqual(result.status, "error")
        self.assertIn("more than one worksheet", result.message)
        self.assertNotIn("as_file()", result.message)

if __name__ == "__main__":
    unittest.main()
