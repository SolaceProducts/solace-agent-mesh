# Examples

Minimal reference tools that cover the patterns real tool authors need.

| File | Pattern | What it teaches |
|---|---|---|
| `numeric_params_tool.py` | `tool_cli` | Typed `int`/`float` params; coercion from string LLM args |
| `artifact_inout_tool.py` | `tool_cli` | Load an `Artifact` input; write an output `DataObject` |
| `config_schema_tool.py` | `tool_cli` + `@with_config_schema` | Operator-supplied config (API key, base URL) |
| `multi_tool_provider.py` | `provider_cli` | Expose 2+ tools from one executable via `DynamicToolProvider` |

Each file is self-contained: run with `--schema` to inspect the tool
definition, or supply a `runner_args.json` to execute it.

```bash
python examples/numeric_params_tool.py --schema
python examples/multi_tool_provider.py --schema
```
