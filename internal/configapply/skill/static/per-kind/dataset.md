A dataset captures a named collection of evaluation examples (a prompt
plus an optional expected response). The resource header carries `name`
and `description`.

Per-example content is authored through an `examples_file:` field under
`spec:` that points at a CSV sidecar (path relative to the dataset YAML).
The examples are reconciled separately during `sam config apply` — they
are applied via the examples API after the dataset itself, so they never
appear on the dataset create/update request. `sam config plan` diffs them
against the stored examples, so a CSV-only edit plans as an update. Apply
makes the stored examples match the file, deleting rows the file no longer
lists, without `--prune`.

The CSV uses a header row and RFC 4180 quoting (quote fields containing
commas, quotes, or newlines):

- `sequence_number` — positive integer, unique within the file. Reused
  across applies so a deleted row is restored at its original position.
- `prompt` — required, unless the row has `input_data`; the column itself
  may be left out when the header has `input_data`.
- `expected_response` — optional; leave the cell empty for none.

Three more optional columns hold a workflow's input and the expectations
two built-in evaluators check. Each cell holds a JSON object; leave it empty for none, and quote it as RFC 4180
requires (wrap the cell in double quotes and double every quote inside):

- `input_data` — the input a workflow target runs on, matching its input
  schema.
- `expected_path` — what the `Workflow Path` evaluator checks on a
  workflow run:
  `{"nodes": {"<node id>": "<state>"}, "tools": {"<tool name>": "called"}}`.
  A node's state is `ran`, `not_ran`, `succeeded`, `failed` or `skipped`;
  a tool's is `called` or `not_called`.
- `expected_facts` — what the `Output Facts` evaluator checks:
  `{"required": ["<value>"], "forbidden": ["<value>"]}`, values the
  response must or must not contain.

`sam config pull` writes each dataset as `<name>.yaml` plus a
`<name>.examples.csv` sidecar (rows sorted by `sequence_number`) and sets
`examples_file` to point at it, so a pulled tree re-applies with no diff. It
writes `input_data`, `expected_path` and `expected_facts` only when some
example has a value for them.

The CSV carries no attachments. An example can also hold one optional
input file (delivered to the agent) and one optional expected file (the
evaluator's reference), uploaded per example through the platform API, and
`examples_file` can neither declare nor restore them. `sam config pull`
warns for each dataset whose examples carry attachments; re-applying that
tree leaves the prompts intact and the files detached.

Example — `datasets/support-qa.yaml`:

```yaml
kind: dataset
name: support-qa
description: Support questions with reference answers.
spec:
  examples_file: support-qa.examples.csv
```

`datasets/support-qa.examples.csv`:

```csv
sequence_number,prompt,expected_response
1,What is the refund window?,30 days from purchase.
2,"Does it support commas, quotes?","Yes — use RFC 4180 quoting."
3,Where are logs stored?,
```

A workflow dataset — `datasets/order-exceptions.examples.csv`:

```csv
sequence_number,prompt,input_data,expected_path,expected_facts
1,,"{""orderId"": ""O-1001"", ""reason"": ""damaged""}","{""nodes"": {""refund"": ""succeeded""}}","{""required"": [""refund issued""]}"
```

Datasets are leaf resources — they do not reference other manifest
resources. Experiments cross-reference a dataset by name to scope the
evaluation corpus.
