Where this guide says to look something up, read one of these files. Paths are relative to the `sam-declarative-config` skill root; a sibling skill is installed next to it.

| Topic | Where |
|---|---|
| Agent, workflow, tool schema | `references/agent.md`, `references/workflow.md`, `references/toolset.md` |
| Entrypoint schema | `references/entrypoint.md` |
| Event mesh entrypoint schema | `references/entrypoint.md`, section *type: event_mesh* |
| Event mesh entrypoint design | the rule under *Event Mesh Entrypoint* above is the whole pattern |
| Scheduled work design | `workflows.md` under the `sam-author-agent` skill's references, the scheduled-task paragraph |
| Best-practices guide | `references/design/best-practices.md` |

Every component is a YAML file in your config repo, applied with `sam config apply`; `references/layout.md` says where each kind lives.
