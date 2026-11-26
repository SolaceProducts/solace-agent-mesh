# CI Pipeline Recipe

One recipe, three syntaxes (GitHub Actions, GitLab CI, Jenkins):

- Pull request → `sam config plan`. The plan is printed in the job log; nothing is written back to the PR.
- Merge to main → `sam config apply --no-interactive`, then `sam eval run $SAM_EVAL_EXPERIMENTS --threshold $SAM_EVAL_THRESHOLD`. The job fails below the threshold.
- One apply at a time via the CI system's own control: a `concurrency` group (GitHub), `resource_group` (GitLab), `disableConcurrentBuilds()` (Jenkins).
- Install a pinned CLI release asset, checksum-verified. Never build `sam` from source in CI.
- Credentials come from the CI secret store as environment variables; the key goes in `SAM_PLATFORM_TOKEN`; plan and apply pass `--no-dotenv` (add `--env-file <path>` for a secrets file the CI writes to disk — it overrides the environment).

Same recipe for a self-hosted deployment and Agent Mesh Cloud; only `SAM_URL` and the key change.

## Prerequisites

- RBAC enforced: `authorization_service.type: default_rbac`.
- API keys not turned off on the deployment (they are on by default).

## One-time key setup (super administrator)

The pipeline runs as a service account holding an API key. Declare the role and service account in the repository with **their own manifest**, never in the pipeline's manifest. A super administrator applies it once with their own login, then creates the key. Both steps stay outside the pipeline: the key's owner must exist before the key, and only a signed-in super administrator can create a key. Unset `SAM_PLATFORM_TOKEN` and `SAM_AUTH_TOKEN` in that shell; they override the login.

```yaml
# ci-deployer.yaml: RBAC role (rbac/roles/)
kind: rbacRole
name: ci-deployer
spec:
  scopes:
    - "*"
```

```yaml
# ci-deployer.yaml: service account (rbac/service-accounts/)
kind: serviceAccount
name: ci-deployer
spec:
  roleNames:
    - ci-deployer
```

```yaml
# rbac.yaml: manifest the super administrator applies (manifests/)
kind: manifest
name: rbac
target:
  url: https://sam.example.com
  auth:
    type: oauth
resources:
  rbacRoles:
    - ci-deployer
  serviceAccounts:
    - ci-deployer
```

```bash
unset SAM_PLATFORM_TOKEN SAM_AUTH_TOKEN
sam auth login https://sam.example.com
sam config apply -m manifests/rbac.yaml
key=$(sam api-key create --url https://sam.example.com --user ci-deployer \
  --label "ci-deployer-$(date +%Y%m%d)" --scopes '*' --expires-in 90d) \
  && printf '%s' "$key" | gh secret set SAM_PLATFORM_TOKEN
```

- Sign in **by URL**. `sam auth login <name> --url <url>` followed by `--target <name>` sends no token; the URL login caches under the host, which is the manifest target's default name.
- **Warn:** a manifest declaring `rbacRoles` or `serviceAccounts` manages every custom role and service account on the deployment, and `rbacRoles` also manages every user's role assignments, including ones made in the Agent Mesh UI; anything unlisted plans as `delete (needs --prune)`. Never apply it with `--prune` unless all of them are listed.
- `*` (stored as `*:*:*`) gives the key full control, RBAC included; it still cannot create, rename or delete API keys. To narrow it, list the same scopes in the role and `--scopes`: `<category>:*:create`, `<category>:*:read` and `<category>:*:update` per declared kind other than datasets, evaluators and experiments (agents and link agents use `agent_builder`, workflows `workflow_builder`, models `model_config`, entrypoints `entrypoint`; datasets, evaluators and experiments use `evaluation:*:create`, `evaluation:*:read` and `evaluation:*:update`); `<category>:*:deploy` for agents, link agents, workflows and entrypoints; `skill:*:read`, `connector:*:read` and `toolset:*:read` when agents are declared (plan checks their references); `agent_builder:*:read` for workflows and entrypoints, `workflow_builder:*:read` for entrypoints; `entrypoint:*:read` when link agents are declared (plan reads the entrypoint each one names); `evaluation:*:delete` when dataset examples change (apply deletes a removed example even without `--prune`); `evaluation:*:read` and `evaluation:*:invoke` for `sam eval run` (it lists experiments and polls runs). In a held scope `_` and `*` in segment 2 are equivalent, so a role that holds the older `_` spelling also covers these scopes; write `*` in new roles. Plan needs the same key as apply (secret compare needs the update scopes).
- A role's `description` goes at the top level next to `name`; under `spec` it is rejected. A role file with no `description` clears one set in the Agent Mesh UI.
- Stdout is only the key plus a newline; on failure it is empty. Capture it with `key=$(...) &&` so a failed create cannot overwrite the working secret with an empty value; `$(...)` also drops the newline, which the GitLab variables API rejects in a masked value. `--user` is the service account's `name`; key scopes must be covered by its roles. At most 10 active keys per service account.
- **Rotate** before expiry, as a signed-in super administrator with `SAM_PLATFORM_TOKEN` and `SAM_AUTH_TOKEN` unset (no key can create keys): rerun `sam api-key create`, replace the CI secret (GitLab: PUT the new value on the existing variable), then `sam api-key list --url <url> --user ci-deployer` and `sam api-key delete <old-id> --url <url> --force`.

## Variables and secrets

| Name | Store as | Value |
|---|---|---|
| `SAM_URL` | variable | Agent Mesh URL |
| `SAM_CLI_VERSION` | variable | CLI release to install; keep in step with the deployment |
| `SAM_CLI_BASE_URL` | variable | URL of the directory holding the mirrored archive |
| `SAM_CLI_SHA256` | variable | the archive's `file_checksum` from the release bill of materials, minus the `sha256:` prefix |
| `SAM_EVAL_EXPERIMENTS` | variable | experiment names, space-separated |
| `SAM_EVAL_THRESHOLD` | variable | minimum pass rate, 0.0–1.0 |
| `SAM_PLATFORM_TOKEN` | secret | the API key |

Plus every `${VAR}` the resource files reference. The examples pass `LLM_API_BASE`, `LLM_MODEL_NAME` and `LLM_API_KEY` to a model file:

```yaml
# ci-general.yaml: model (models/)
kind: model
name: ci-general
spec:
  provider: openai
  modelName: ${LLM_MODEL_NAME}
  apiBase: ${LLM_API_BASE}
  authConfig:
    type: apikey
    api_key: ${LLM_API_KEY}
```

Never set `SAM_AUTH_TOKEN` in the job: `sam eval run` prefers it over `SAM_PLATFORM_TOKEN`. The plan job runs unmerged code with the apply key, so anyone with push access can use it unreviewed; with `*` that means granting themselves super administrator, which outlives the branch. Push access should match who you would make super administrator, or narrow the scopes.

## Installer script

The Linux archive `solace-agent-mesh-<version>-cli-linux-amd64.tar.gz` ships in the release's `CLI/` folder on the Solace Product Portal; mirror it where runners can reach it. Commit as `ci/install-sam.sh`, executable:

```bash
#!/usr/bin/env bash
# Install the pinned sam CLI into .sam-cli/ after checking the archive's SHA-256 checksum.
set -euo pipefail
: "${SAM_CLI_VERSION:?set SAM_CLI_VERSION}" "${SAM_CLI_BASE_URL:?set SAM_CLI_BASE_URL}" "${SAM_CLI_SHA256:?set SAM_CLI_SHA256}"
archive="solace-agent-mesh-${SAM_CLI_VERSION}-cli-linux-amd64.tar.gz"
curl -fsSL -o "$archive" "${SAM_CLI_BASE_URL}/${archive}"
echo "${SAM_CLI_SHA256}  ${archive}" | sha256sum -c -
mkdir -p .sam-cli
tar -xzf "$archive" -C .sam-cli sam
rm "$archive"
.sam-cli/sam --version
```

Both `plan` and `apply` build toolsets, so the job needs each toolset's toolchain: `python3` + `pip` for Python, Go for Go, whatever a `build.sh` runs. Python builds target the platform's reported version; when the runner's minor version, OS or architecture differs, pip takes only binary wheels, so an sdist-only dependency fails (examples use 3.11). With nothing to build, any image with `bash`, `curl`, `sha256sum`, `tar` is enough.

## GitHub Actions

```yaml
# sam-config.yaml: GitHub Actions workflow
name: sam-config

on:
  pull_request:
    branches: [main]
  push:
    branches: [main]

permissions:
  contents: read

env:
  MANIFEST: manifests/prod.yaml
  SAM_URL: ${{ vars.SAM_URL }}
  SAM_CLI_VERSION: ${{ vars.SAM_CLI_VERSION }}
  SAM_CLI_BASE_URL: ${{ vars.SAM_CLI_BASE_URL }}
  SAM_CLI_SHA256: ${{ vars.SAM_CLI_SHA256 }}
  SAM_EVAL_EXPERIMENTS: ${{ vars.SAM_EVAL_EXPERIMENTS }}
  SAM_EVAL_THRESHOLD: ${{ vars.SAM_EVAL_THRESHOLD }}
  LLM_API_BASE: ${{ vars.LLM_API_BASE }}
  LLM_MODEL_NAME: ${{ vars.LLM_MODEL_NAME }}

jobs:
  plan:
    if: github.event_name == 'pull_request'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          persist-credentials: false
      - uses: actions/setup-python@a26af69be951a213d495a4c3e4e4022e16d87065 # v5.6.0
        with:
          python-version: "3.11"
      - run: ci/install-sam.sh && echo "$PWD/.sam-cli" >> "$GITHUB_PATH"
      - run: sam config plan -m "$MANIFEST" --url "$SAM_URL" --no-dotenv --no-interactive --no-color
        env:
          SAM_PLATFORM_TOKEN: ${{ secrets.SAM_PLATFORM_TOKEN }}
          LLM_API_KEY: ${{ secrets.LLM_API_KEY }}

  apply:
    if: github.event_name == 'push'
    runs-on: ubuntu-latest
    concurrency:
      group: sam-config-apply
      cancel-in-progress: false
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          persist-credentials: false
      - uses: actions/setup-python@a26af69be951a213d495a4c3e4e4022e16d87065 # v5.6.0
        with:
          python-version: "3.11"
      - run: ci/install-sam.sh && echo "$PWD/.sam-cli" >> "$GITHUB_PATH"
      - run: sam config apply -m "$MANIFEST" --url "$SAM_URL" --no-dotenv --no-interactive --no-color
        env:
          SAM_PLATFORM_TOKEN: ${{ secrets.SAM_PLATFORM_TOKEN }}
          LLM_API_KEY: ${{ secrets.LLM_API_KEY }}
      - run: sam eval run $SAM_EVAL_EXPERIMENTS --url "$SAM_URL" --threshold "$SAM_EVAL_THRESHOLD"
        env:
          SAM_PLATFORM_TOKEN: ${{ secrets.SAM_PLATFORM_TOKEN }}
```

Lives under `.github/workflows/`. Secrets go only on the steps that run `sam`; actions are pinned to a commit SHA. Secrets are not passed to fork PRs, so plan works only for in-repo branches. The concurrency group keeps one running and one waiting run; a newer waiting run cancels the older, which is safe because the newest commit contains the earlier merges.

## GitLab CI

```yaml
# .gitlab-ci.yml: GitLab CI pipeline
default:
  image: python:3.11
  before_script:
    - ci/install-sam.sh
    - export PATH="$PWD/.sam-cli:$PATH"

variables:
  MANIFEST: manifests/prod.yaml

plan:
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
  script:
    - sam config plan -m "$MANIFEST" --url "$SAM_URL" --no-dotenv --no-interactive --no-color

apply:
  rules:
    - if: $CI_PIPELINE_SOURCE == "push" && $CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH
  resource_group: sam-config-apply
  script:
    - sam config apply -m "$MANIFEST" --url "$SAM_URL" --no-dotenv --no-interactive --no-color
    - sam eval run $SAM_EVAL_EXPERIMENTS --url "$SAM_URL" --threshold "$SAM_EVAL_THRESHOLD"
```

Mark secrets **Masked**, not **Protected** — MR pipelines on unprotected branches cannot read protected variables, so every branch pipeline can read the key; warn that anyone who can push a branch can use it, and with `*` grant themselves super administrator. GitLab creates the resource group on the first apply with process mode `unordered`; switch it to `oldest_first` through GitLab's resource groups API so waiting applies run in merge order. The GitLab API calls (storing the key, rotating it, the process mode) need a personal or project access token with the `api` scope and at least the Maintainer role in the project.

## Jenkins

```groovy
// Jenkinsfile: Jenkins declarative pipeline
pipeline {
  agent {
    docker { image 'python:3.11' }
  }
  options {
    disableConcurrentBuilds()
  }
  environment {
    MANIFEST = 'manifests/prod.yaml'
  }
  stages {
    stage('Install sam') {
      steps {
        sh 'ci/install-sam.sh'
      }
    }
    stage('Plan') {
      when { not { branch 'main' } }
      environment {
        SAM_PLATFORM_TOKEN = credentials('sam-platform-token')
        LLM_API_KEY = credentials('llm-api-key')
      }
      steps {
        sh '.sam-cli/sam config plan -m "$MANIFEST" --url "$SAM_URL" --no-dotenv --no-interactive --no-color'
      }
    }
    stage('Apply') {
      when { branch 'main' }
      environment {
        SAM_PLATFORM_TOKEN = credentials('sam-platform-token')
        LLM_API_KEY = credentials('llm-api-key')
      }
      steps {
        sh '.sam-cli/sam config apply -m "$MANIFEST" --url "$SAM_URL" --no-dotenv --no-interactive --no-color'
        sh '.sam-cli/sam eval run $SAM_EVAL_EXPERIMENTS --url "$SAM_URL" --threshold "$SAM_EVAL_THRESHOLD"'
      }
    }
  }
}
```

Needs Docker on the agent, the Docker Pipeline plugin, and a **multibranch** pipeline (the `branch` conditions need it). Credentials are **Secret text**, with IDs matching the Jenkinsfile's `credentials()` calls; other values are global or folder environment variables. Each branch is its own job, so `disableConcurrentBuilds()` serialises the builds of `main` — the only ones that apply. Credentials are bound per stage so `ci/install-sam.sh` cannot read them; toolset build scripts run inside `sam` and still can. Plan runs every branch's files with the key, so anyone who can push a branch can use it; if the job discovers fork PRs, trust only forks from users with push access.

## Behaviour to warn about

- **A failed eval does not roll back the apply.** The job is red but the change stays applied and deployed. Recovery: revert the merge commit; the pipeline re-applies the reverted files and re-runs the eval. Without `--prune` the revert does not delete resources the merge created.
- **No manual `sam config apply` against a pipeline-managed deployment.** It skips review and the CI system's concurrency control, and the next pipeline run overwrites it.
- **A plan `update` shows no field diff without `-v`, and `-v` covers models and agents only.** Reviewers read other field changes from the PR or MR diff.
- **Most plans show changes with no file changed, so the plan cannot drive skip-on-no-change.** Resources seeded at install (built-in agents, the sample experiment) plan `delete (needs --prune)`. Optional refinement: path-filter the apply to the config directories and pipeline files; then re-run a failed apply job by hand, since a later non-config merge will not. GitHub: `paths:` on the `push` trigger (re-run the last apply job to pick up a variable-only change). GitLab: `changes:` on the apply rule (retry the last apply job). Jenkins: on the Apply stage, `allOf { branch 'main'; anyOf { changeset pattern: '^(manifests|models|toolsets|agents|datasets|evaluators|experiments|ci)/.*|^Jenkinsfile$', comparator: 'REGEXP'; triggeredBy 'UserIdCause' } }`. `changeset` compares with the previous build of the branch, so the first build of `main` skips; use **Build Now**, which `triggeredBy` lets through.
- **Give every model an explicit `authConfig.type`.** A model whose `authConfig` has no `type` is stored with auth type `none`, and its key is not handled as a secret.
