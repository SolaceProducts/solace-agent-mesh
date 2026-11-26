# Skill asset templates

A skill's `assets/` directory can ship more than static files. A single **`.samt`
file** (a packaged template — one self-contained file bundling the body and its
contract) becomes a **template**: the agent fills its `@@KEY@@` placeholders and
the `instantiate_template` tool validates and saves it, leaving the embeds and
Liquid to render live each time the artifact is downloaded. Any other asset is
copied verbatim. This lets a hand-authored skill repo ship report and document
templates with no generation code, and it is one of the larger token-efficiency
levers in Agent Mesh (the model produces only the data; the template engine renders the
document).

The on-disk shape — one file per template, identical to the downloadable
artifact form:

```text
skills/
  <name>/
    assets/
      report.html.samt        # a packaged template (single .samt file)
      logo.png                # a plain asset (verbatim copy)
```

Produce the `.samt` with the `package_template` tool (from the `sam-templates`
skill), then drop that single file into `assets/`. Its internal sidecar's three
sections (`template`, `substitutions`, `data_inputs`), the closed-set `@@KEY@@`
substitution rules, the `data_inputs` JSON-Schema / CSV `columns` data contract,
the fail-closed validation behavior, and a full `quarterly_report` worked example
are documented once in the customer docs — **do not restate them here**:

- **Asset templates** — the `sam-docs` skill, `building/skills.md` (*Asset Templates*)

The agent reads a bundled template's contract with `read_template` (skill_name +
asset) and renders it with `instantiate_template`; everything else about the
`skills/<name>/` bundle is in `references/skill.md`.
