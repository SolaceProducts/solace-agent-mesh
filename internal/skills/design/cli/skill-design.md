Where this guide says to look something up: *Skill schema* is `references/skill.md`, relative to the `sam-declarative-config` skill root.

### Supplying reference files

A skill is a directory: `SKILL.md` holds the instruction content and, in its frontmatter, the `name` and `description`; `references/` holds one markdown file per topic; `assets/` holds templates and other files. Write the files — there is no inline-content or artifact-reference choice to make; a `.samt` template is copied into `assets/` as-is. Layout and rules: `references/skill.md`; templates: `references/skill-asset-templates.md`; the template contract: the `sam-docs` skill, `building/skills.md` (*Asset Templates*).

### Attaching a skill to an agent

```yaml
spec:
  skillRefs:
    - my-api-reference
```

Agent card capabilities are `spec.skills[]`; knowledge skills are `spec.skillRefs[]`. Do not put a knowledge skill under `spec.skills`.

### Validating

`sam config plan` checks the bundle, its manifest entry and every `skillRefs` reference before anything is applied. The frontmatter `name` must match both the directory name and the manifest entry, and a `skillRefs` entry that names no skill in the manifest is a plan-time error.
