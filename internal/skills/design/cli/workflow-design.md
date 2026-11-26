Where this guide says to look something up, read one of these files. Paths are relative to the `sam-declarative-config` skill root; a sibling skill is installed next to it.

| Topic | Where |
|---|---|
| Workflow schema | `references/workflow.md` |
| Per-tool inputs and result fields | `builtin-tools.md` under the `sam-author-agent` skill's references, the table generated from the registry |

For "An Agent Node Guarantees Only the Fields It Declares": a workflow node's `output_schema_override` sits on the node inside the workflow's `spec.appConfig`. An agent resource carries its own schema as `outputSchema` (camelCase) under `spec.additionalConfigurations`; the platform writes it as the runtime `output_schema` at deploy.
