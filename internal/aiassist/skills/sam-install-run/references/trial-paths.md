# The desktop trial flow

All artifacts come from the product portal (https://products.solace.com/prods/Agent_Mesh). The only prerequisite: an LLM API key.

## Desktop app (default)

- Installers: macOS `.dmg`, Windows `.zip` wrapping the per-user NSIS `.exe` installer (extract the archive, then run the `.exe`); Linux ships as `.deb`/`.rpm` packages (nfpm) or a portable `.tar.gz`. (Rolling out — if the portal doesn't list an artifact for the user's OS yet, the Helm quickstart runs locally on minikube/Kind/Colima as a single-node trial — see `sam-deploy`.)
- **macOS has two DMGs — pick by chip.** `…-desktop-macos-apple-silicon.dmg` for Apple Silicon (M-series), `…-desktop-macos-intel.dmg` for Intel. Note the arch reads as `apple-silicon`/`intel` in the DMG name — *not* `arm64`/`amd64` (those name only the build directory, not the artifact). Wrong pick → it won't launch.
- First launch opens the chat UI with the **Orchestrator** and **Builder** agents already running; supply the LLM key when prompted and chat.
- **Model/LLM setup happens in the app UI, not a config file.** First run auto-opens a **Configure Your AI Models** dialog — supply the key there; add or change models later under the **Models** section. The **Settings** dialog has no model picker, so don't send users there for one. A built-in default model shows *"Cannot be changed"* on its **display-name/alias only** — provider, model, and key stay editable, and users can add their own (the model is *not* locked). *(That's the in-app path. The CLI has a separate onboarding wizard that auto-detects provider keys from environment variables or a `.env` file in the working dir and writes the `llm:` block of `settings.yaml`.)*
- Everything is embedded — in-process dev broker, on-disk SQLite, local filesystem artifact storage under the data dir. Nothing external to install.
- **The desktop install includes the `sam` CLI.** Binary locations: Windows — a console `sam.exe` in the install's `cli` subfolder (`%LOCALAPPDATA%\Programs\Solace Agent Mesh\cli`); macOS — the app binary itself (`/Applications/Solace Agent Mesh.app/Contents/MacOS/sam-desktop`); Linux — already on `PATH` as `solace-agent-mesh` (the package symlinks it). Once the binary is reachable on `PATH` the `sam` CLI is available; let the user expose it however they prefer (user `PATH` entry, symlink, or shell alias), favoring per-user methods that need no admin/sudo. No separate CLI download is needed alongside a desktop install.
- WebUI + API on a port chosen at launch; the startup log prints the exact URL. The embedded broker is in-process — no `SOLACE_BROKER_*` vars for a solo trial (those are only for connecting to an external broker).
- User config lives at `~/Library/Application Support/sam` (macOS), `~/.config/sam` (Linux), `%APPDATA%\sam` (Windows) — useful when resetting a trial.
- No project-scaffold step: the desktop boots from bundled built-in defaults. `sam init` / `sam config init` do not exist in the Go CLI. For a version-controlled config repo, see `sam-declarative-config`.

## Pre-flight & first-failure triage

`sam doctor` runs a local pre-flight by default (equivalently `SAM_DOCTOR_CONTEXT=local sam doctor`) — validating the LLM endpoint/key, port availability (8800 gateway, 8001 platform), and runtime before first start. Run it before assuming the product is broken; its output names the failing prerequisite. Persistent failures after a clean doctor run → `sam-troubleshoot` / `sam-operate`.
