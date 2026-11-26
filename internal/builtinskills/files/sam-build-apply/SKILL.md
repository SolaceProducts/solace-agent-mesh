---
name: sam-build-apply
description: Tools the AI Builder agent uses to confirm completion of an Apply Changes flow. Call mark_changes_applied at the end of editing a build to give the FE an explicit signal that the apply has finished — without this, the post-edit "Switch to Test Mode" CTA waits on weaker all-deployed-status heuristics.
tags:
  - builder
  - apply
---

# Build Apply Skill

You are loaded by the AI Builder agent. Your job is to provide a single
explicit completion marker the frontend can detect when an Apply Changes
flow finishes.

## When to call `sam-build-apply__mark_changes_applied`

Call this tool **once**, at the very end of a flow where:

1. The user previously clicked **Apply Changes** in Test mode, and
2. You (the build agent) received the recommendations message and edited
   one or more components in response, and
3. Every component you modified has been redeployed and is back in a
   working state.

The frontend uses the artifact this tool writes to:
- swap the canvas component pills back to "Active" with confidence the
  edits are settled,
- render the post-edit "Changes are completed!" summary inline in chat,
- show the "Go to Test Mode" CTA so the user can immediately re-test
  the changes.

If you call it before the redeploy finishes, the FE will surface the
completion UI prematurely — wait until validation + deploy succeed.

If you don't call it at all, the FE falls back to the same heuristic
it used before this skill existed (every session-origin component is
deployed) — usable but slower / less reliable.

## Tool

### `sam-build-apply__mark_changes_applied`

Records the apply outcome in a structured `apply_complete.json` artifact
in the current chat session. Parameters:

```json
{
  "summary": "<one-paragraph natural-language summary of what changed>",
  "changesApplied": [
    {
      "component": "<component name>",
      "area": "system_prompt" | "tools" | "workflow_logic" | "model_choice" | "other",
      "change": "<concrete description of the edit>"
    }
  ]
}
```

`summary` becomes the chat-visible "Changes are completed! Here is the
summary:" body. `changesApplied` is rendered as a per-component list
with priority-style colour coding so the user can scan what moved.
