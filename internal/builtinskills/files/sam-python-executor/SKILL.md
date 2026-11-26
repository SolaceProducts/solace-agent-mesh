---
name: sam-python-executor
description: Run arbitrary LLM-generated Python in a sandboxed runtime — general computation, data wrangling and analysis, math, format conversion, and chart/file generation, with a rich pre-installed stack (pandas, numpy, scipy, scikit-learn, statsmodels, sympy, matplotlib, seaborn, networkx, Pillow, openpyxl, python-docx, pdfplumber, lxml/bs4, faker, and more). Works with no inputs at all; optionally reads existing session artifacts and emits new ones. No network access.
tags:
  - python
  - code-execution
  - data-analysis
  - artifacts
  - sandbox
tools:
  - tool_type: builtin
    tool_name: execute_python_code
    timeout_seconds: 300
---

# Python Code Execution Skill

You have access to an **`execute_python_code`** tool that runs Python you generate
inside the Secure Tool Runtime sandbox. It is general-purpose: use it for **any**
computation that's easier to express as code — calculations, algorithms, data generation,
data wrangling/analysis, statistics, simulations, format conversion, and chart/file
generation. Reading existing artifacts is just one optional capability, not a requirement;
plenty of useful runs take no input artifacts at all.

## How It Works

1. Pass your Python source as the `python_code` parameter. That alone is enough for
   self-contained work (e.g. compute something, generate data with `faker`, solve with
   `sympy`, build a chart or spreadsheet from scratch).
2. **Only if your code reads an existing artifact**, list its filename in the
   `input_artifacts` argument (e.g. `input_artifacts=["sales.csv"]`) and access it via
   `INPUT_ARTIFACTS["sales.csv"]`. Omit `input_artifacts` entirely when you don't read
   any. (As a safety net the runtime also auto-detects names your code references via
   `INPUT_ARTIFACTS[...]` and loads ones that exist in the session, but declaring them is
   clearest.)
3. Your code computes, produces output files with `save_artifact(...)` or by writing them
   directly in its working directory (e.g. `df.to_csv("top.csv")`), and `print(...)`s
   anything it wants returned as text.
4. The tool returns captured stdout as the result and registers the output files as
   artifacts on the task, so the agent (and later tool calls) can reference them.

## Runtime contract — globals available to your code

<!-- Maintainers: keep the artifact accessor list below (especially the .as_file()
     guidance) in sync with the execute_python_code docstring in
     tools/python/python_executor/python_executor.py. Both reach the model. -->

- **`INPUT_ARTIFACTS`** — a `dict` keyed by artifact filename. Each value exposes:
  - `.content` — raw `bytes`
  - `.filename`, `.mime_type`
  - `.as_text(encoding="utf-8")` — content decoded to `str`
  - `.as_bytes()` — content as `bytes`
  - `.as_file()` — content as a **seekable file object**. Use this for every library
    that opens a file: `openpyxl`, `docx`, `pdfplumber`, `zipfile`, `PIL`, `pandas`.
    Passing raw bytes fails in all of them except `pandas.read_excel`, which still
    accepts bytes but warns (`pandas.read_csv` does not — it needs `.as_file()`).
- **`save_artifact(filename, content, mime_type="application/octet-stream", description=None)`**
  — create an output artifact. `content` may be `str` (encoded UTF-8; mime defaults to
  `text/plain`) or `bytes`.
- **`send_status(message)`** — push a short progress update to the user for long-running
  work.
- **`print(...)`** — stdout is captured and returned as the tool's text result. An
  uncaught exception returns the traceback (plus any output produced before the error).

`openpyxl` is the trap worth knowing. Handed raw bytes it raises
`InvalidFileException: openpyxl does not support b'...' file format`, which reads like a
corrupt file when in fact only the argument type is wrong. Fix it with `.as_file()`;
don't switch libraries or parse the format by hand.

The `b'...'` is the tell. openpyxl reuses that same "does not support" wording for a
genuinely unsupported format — `does not support the old .xls file format, please use
xlrd`. That one is a real format complaint, so believe it and use `xlrd`; `.as_file()`
will not help there.

## Available libraries

<!-- Maintainers: keep this list in sync with the execute_python_code docstring
     (tools/python/python_executor/python_executor.py) and the dependencies in
     tools/python/python_executor/pyproject.toml. -->

Standard library is fully available. These third-party packages are **guaranteed**
pre-installed in the sandbox:

- **Data / science:** `numpy`, `pandas`, `scipy`, `matplotlib`, `seaborn`,
  `sklearn` (scikit-learn), `statsmodels`, `sympy`, `networkx`
- **Documents / spreadsheets:** `PIL` (Pillow), `openpyxl`, `xlsxwriter`, `xlrd`,
  `docx` (python-docx), `pdfplumber`, `fpdf` (fpdf2)
- **Parsing / utilities:** `lxml`, `bs4` (BeautifulSoup), `yaml` (PyYAML),
  `jsonschema`, `pydantic`, `regex`, `tabulate`, `faker`, `wordcloud`, `dateutil`

**Constraints:**
- **No network access.** You cannot fetch URLs, call external APIs, or `pip install`
  anything. Work only with `INPUT_ARTIFACTS` and the libraries above.
- For matplotlib, use the non-interactive `Agg` backend (`matplotlib.use("Agg")` or
  `import matplotlib; matplotlib.pyplot.savefig(...)` to a buffer) — there is no display.
- Files written directly in the working directory are returned under the same name, up
  to 20 files and 50 MiB per call, ASCII names only. Hidden files and `__pycache__` are
  ignored, and a name also passed to `save_artifact(...)` keeps the `save_artifact(...)`
  version. Other files that are not returned (such as files in folders, links, non-ASCII
  names, and files past the limits) are listed in the result with the reason. Use `save_artifact(...)` to return a
  file under a name you choose.
- If the code raises an exception, no files are returned, including those passed to
  `save_artifact(...)`.

## Examples

**Summarize a CSV with pandas** — call with `input_artifacts=["sales.csv"]`:

```python
import pandas as pd

df = pd.read_csv(INPUT_ARTIFACTS["sales.csv"].as_file())
summary = df.describe(include="all")
save_artifact("summary.csv", summary.to_csv(), "text/csv", "Column statistics")
print(f"Rows: {len(df)}, columns: {list(df.columns)}")
print(f"Total revenue: {df['revenue'].sum():,.2f}")
```

**Resize an image with Pillow** — call with `input_artifacts=["photo.png"]`:

```python
import io
from PIL import Image

img = Image.open(INPUT_ARTIFACTS["photo.png"].as_file())
img = img.resize((img.width // 2, img.height // 2))
buf = io.BytesIO()
img.save(buf, format="PNG")
save_artifact("thumbnail.png", buf.getvalue(), "image/png", "Half-size thumbnail")
print(f"Resized to {img.width}x{img.height}")
```

**Read an Excel workbook with openpyxl** — call with `input_artifacts=["book.xlsx"]`.
Note `.as_file()`: passing `.as_bytes()` or `.content` here fails with a misleading
`InvalidFileException` that looks like file corruption.

```python
import openpyxl

wb = openpyxl.load_workbook(INPUT_ARTIFACTS["book.xlsx"].as_file())
ws = wb.active
print(f"Sheets: {wb.sheetnames}")
print(f"{ws.title}: {ws.max_row} rows x {ws.max_column} cols")
for row in ws.iter_rows(max_row=5, values_only=True):
    print(row)
```

**Generate a chart with matplotlib** — call with `input_artifacts=["monthly.csv"]`:

```python
import io, matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import pandas as pd

df = pd.read_csv(INPUT_ARTIFACTS["monthly.csv"].as_file())
fig, ax = plt.subplots()
ax.plot(df["month"], df["value"], marker="o")
ax.set_title("Monthly trend")
buf = io.BytesIO()
fig.savefig(buf, format="png", dpi=120, bbox_inches="tight")
save_artifact("trend.png", buf.getvalue(), "image/png", "Monthly trend chart")
print("Chart generated.")
```

**Self-contained computation — no input artifacts** (just `python_code`):

```python
import sympy as sp

x = sp.symbols("x")
roots = sp.solve(x**2 - 5*x + 6, x)
print("roots:", roots)
save_artifact("roots.txt", f"x^2-5x+6 = 0 -> {roots}", "text/plain", "Solved roots")
```

**Build an Excel workbook with openpyxl** (no input artifacts):

```python
import io
from openpyxl import Workbook

wb = Workbook()
ws = wb.active
ws.title = "Report"
ws.append(["Name", "Score"])
for name, score in [("Alice", 92), ("Bob", 87)]:
    ws.append([name, score])
buf = io.BytesIO()
wb.save(buf)
save_artifact(
    "report.xlsx",
    buf.getvalue(),
    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    "Scores workbook",
)
print("Workbook written with 2 rows.")
```

## When to use this skill

Reach for `execute_python_code` for any open-ended computation that's cleaner as code than
as a chain of tools — calculations and algorithms, generating or simulating data,
numeric/statistical analysis, joins/aggregations across files, format conversion, and
programmatic chart/image/document generation. It works with or without input artifacts.
Prefer a purpose-built tool (e.g. SQL, chart, or conversion tools) when one cleanly fits;
use Python for everything in between.
