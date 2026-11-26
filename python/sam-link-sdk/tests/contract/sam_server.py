# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Builds or finds a SAM-Go `sam` binary and runs it embedded, with the agent link entrypoint, for the contract tests.

Hermetic by construction: every port is chosen free at run time (never one of FORBIDDEN_PORTS), the data directory,
SAM home and HOME live under the test's temporary directory, the environment is built from scratch (no developer
secrets reach SAM), and only the process this module started is ever signalled.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import secrets
import signal
import socket
import subprocess
import time
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from pathlib import Path

from sam_link_sdk.cli import link_token

REPO_ROOT = Path(__file__).resolve().parents[2]
CACHE_DIR = REPO_ROOT / ".contract-cache"
FORBIDDEN_PORTS = frozenset({8849, 8869, 8091, 9000, 55433, 55435})
"""Ports other stacks on a developer machine are known to hold; never handed to SAM even when free."""
BUILD_TIMEOUT_SECONDS = 900
BOOT_TIMEOUT_SECONDS = 120
STOP_GRACE_SECONDS = 15
LOG_TAIL_LINES = 150
TEST_USER = "alice@example.com"
SECOND_USER = "bob@example.com"
"""A second user with the same roles, for what one user must not see of another's: a request names them with SAM's
X-SAM-User-ID header (SamClient(user=...)), which SAM honours with login off.
"""
LINK_GATEWAY_ID = "agent-link"
WEB_GATEWAY_ID = "contract-gw"
PASSTHROUGH_ENV = ("PATH", "TMPDIR", "LANG", "LC_ALL", "SYSTEMROOT")
"""The keys of SAM's environment the harness passes through from the caller; everything else is set explicitly."""


class SamUnavailable(Exception):
    """No SAM-Go binary could be found or built: the contract tests skip with this reason."""


@dataclass(frozen=True)
class LinkAgent:
    """One agent configured on the link entrypoint."""

    name: str
    peers: tuple[str, ...] = ()
    owner: str = ""
    team: str = ""
    wake_url: str = ""
    wake_timeout_seconds: int = 0


def locate_binary() -> Path:
    """SAM_GO_BIN (a built `sam`), else SAM_GO_DIR (a SAM-Go checkout, built here and cached by commit)."""
    explicit = os.environ.get("SAM_GO_BIN", "").strip()
    if explicit:
        path = Path(explicit).expanduser().resolve()
        if not (path.is_file() and os.access(path, os.X_OK)):
            raise SamUnavailable(f"SAM_GO_BIN={explicit} is not an executable file")
        return path
    checkout = os.environ.get("SAM_GO_DIR", "").strip()
    if not checkout:
        raise SamUnavailable(
            "set SAM_GO_BIN (a built SAM-Go `sam` binary) or SAM_GO_DIR (a SAM-Go checkout) to run the contract tests"
        )
    return _build(Path(checkout).expanduser().resolve())


def _build(checkout: Path) -> Path:
    """Build SAM-Go from a checkout.

    The flags are those of SAM-Go's `make build-backend`: no frontend and no Solace client library, since the dev broker
    is enough.
    """
    if not (checkout / "go.mod").is_file() or not (checkout / "cmd" / "sam").is_dir():
        raise SamUnavailable(f"SAM_GO_DIR={checkout} is not a SAM-Go checkout (no go.mod or cmd/sam)")
    out = CACHE_DIR / "sam-go" / _source_key(checkout) / "sam"
    if out.is_file():
        return out
    out.parent.mkdir(parents=True, exist_ok=True)
    _ignore_cache_dir()
    command = ["go", "-C", str(checkout), "build", "-tags", "nosolace", "-o", str(out), "./cmd/sam"]
    try:
        done = subprocess.run(command, capture_output=True, text=True, timeout=BUILD_TIMEOUT_SECONDS)
    except FileNotFoundError as exc:
        raise SamUnavailable("SAM_GO_DIR is set but `go` is not on PATH") from exc
    except subprocess.TimeoutExpired as exc:
        raise SamUnavailable(f"building SAM-Go took longer than {BUILD_TIMEOUT_SECONDS} s") from exc
    if done.returncode != 0:
        out.unlink(missing_ok=True)
        raise RuntimeError(f"go build of SAM-Go failed:\n{done.stderr[-4000:]}")
    return out


def _source_key(checkout: Path) -> str:
    """The checkout's commit, plus a digest of its uncommitted changes, so a dirty tree is rebuilt."""

    def git(*args: str) -> str:
        done = subprocess.run(["git", "-C", str(checkout), *args], capture_output=True, text=True, timeout=60)
        return done.stdout if done.returncode == 0 else ""

    head = git("rev-parse", "HEAD").strip() or "unknown"
    dirty = git("status", "--porcelain")
    if not dirty:
        return head[:16]
    return f"{head[:16]}-{hashlib.sha256((dirty + git('diff', 'HEAD')).encode()).hexdigest()[:12]}"


def _ignore_cache_dir() -> None:
    marker = CACHE_DIR / ".gitignore"
    if not marker.exists():
        CACHE_DIR.mkdir(parents=True, exist_ok=True)
        marker.write_text("*\n")


def free_port() -> int:
    for _ in range(50):
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        if port not in FORBIDDEN_PORTS:
            return port
    raise RuntimeError("no free port outside the forbidden set")


@dataclass
class SamServer:
    """One embedded SAM: the web gateway (HTTP API), the platform, the dev broker and the agent link entrypoint."""

    binary: Path
    root: Path
    agents: list[LinkAgent]
    namespace: str = field(default_factory=lambda: f"sam-contract-{secrets.token_hex(4)}")
    task_idle_seconds: int = 0
    system_purpose: str = ""
    gateway_port: int = 0
    health_port: int = 0
    env: dict[str, str] = field(default_factory=dict)
    process: subprocess.Popen | None = None
    tokens: dict[str, Path] = field(default_factory=dict)
    digests: dict[str, str] = field(default_factory=dict)
    _secrets: list[str] = field(default_factory=list)
    _log_from: int = 0

    def __post_init__(self) -> None:
        self.binary = Path(self.binary).resolve()
        self.root = Path(self.root).resolve()

    @property
    def base_url(self) -> str:
        return f"http://127.0.0.1:{self.gateway_port}"

    @property
    def link_url(self) -> str:
        return f"ws://127.0.0.1:{self.gateway_port}/gw/{LINK_GATEWAY_ID}/v1/connect"

    @property
    def log_path(self) -> Path:
        return self.root / "sam.log"

    @property
    def home(self) -> Path:
        return self.root / "sam-home"

    def token(self, agent: str) -> str:
        return self.tokens[agent].read_text().strip()

    def reissue_token(self, agent: str) -> Path:
        """Issue the agent a new token, in a file of its own. The entrypoint reads its configuration once, at boot, so
        only the SAM that `restart()` boots accepts it (and no longer accepts the agent's earlier token)."""
        path = self.root / "tokens" / f"{agent}.link-token.{len(self._secrets)}"
        self.digests[agent] = link_token(path)
        self.tokens[agent] = path
        self._secrets.append(path.read_text().strip())
        return path

    def restart(self) -> None:
        """Stop this SAM and boot it again on the same ports and data, with the tokens as they are now: every link to
        it drops, and an agent that comes back is checked against the current tokens."""
        self.stop()
        self.start()

    def start(self) -> None:
        """Start SAM-Go with RBAC but no identity provider, as SAM-Go's `dev-up --rbac` does.

        The trust manager signs each task's user token, the link entrypoint verifies it, and the platform resolves the
        user's scopes from the YAML. The spend ledger and its read API are on. No model is ever called, because the
        agents under test are scripted.
        """
        self.root.mkdir(parents=True, exist_ok=True)
        configs = self.root / "configs"
        configs.mkdir(exist_ok=True)
        (self.home / "data").mkdir(parents=True, exist_ok=True)
        (self.root / "user-home").mkdir(exist_ok=True)
        tokens_dir = self.root / "tokens"
        tokens_dir.mkdir(mode=0o700, exist_ok=True)
        for agent in self.agents:
            if agent.name in self.tokens:
                continue
            path = tokens_dir / f"{agent.name}.link-token"
            self.digests[agent.name] = link_token(path)
            self.tokens[agent.name] = path
            self._secrets.append(path.read_text().strip())
        digests = self.digests
        session_secret = secrets.token_hex(32)
        self._secrets.append(session_secret)

        (configs / "gateway.yaml").write_text(
            _gateway_yaml(self.namespace, self.task_idle_seconds, self.system_purpose)
        )
        (configs / "platform.yaml").write_text(_platform_yaml(self.namespace))
        (configs / "agent_link.yaml").write_text(_agent_link_yaml(self.namespace, self.agents, digests))
        roles, users = self.root / "roles.yaml", self.root / "users.yaml"
        roles.write_text(ROLES_YAML)
        users.write_text("users:\n" + "".join(USER_YAML.format(user=u) for u in (TEST_USER, SECOND_USER)))

        if not self.gateway_port:
            self.gateway_port, self.health_port = free_port(), free_port()
            while self.health_port == self.gateway_port:
                self.health_port = free_port()
        env = {k: os.environ[k] for k in PASSTHROUGH_ENV if k in os.environ}
        env.update(
            HOME=str(self.root / "user-home"),
            SAM_HOME=str(self.home),
            SAM_DATA_DIR=str(self.home / "data"),
            FASTAPI_PORT=str(self.gateway_port),
            SESSION_SECRET_KEY=session_secret,
            TRUST_MANAGER_ENABLED="true",
            AUTHORIZATION_TYPE="default_rbac",
            ROLE_DEFINITIONS_PATH=str(roles),
            USER_ROLES_PATH=str(users),
            SAM_DEV_USER_ID=TEST_USER,
            SAM_DEV_USER_EMAIL=TEST_USER,
            SAM_DEV_USER_NAME="Alice",
            LLM_SERVICE_API_KEY="placeholder",
        )
        env.update(self.env)
        command = [
            str(self.binary),
            "run",
            "--embedded",
            "--system-env",
            "--no-onboarding",
            "--sam-home",
            str(self.home),
            "--listen",
            f"127.0.0.1:{self.gateway_port}",
            "--health-addr",
            f"127.0.0.1:{self.health_port}",
            str(configs),
        ]
        self._log_from = self.log_path.stat().st_size if self.log_path.exists() else 0
        log = open(self.log_path, "ab")  # noqa: SIM115
        try:
            self.process = subprocess.Popen(
                command,
                cwd=self.root,
                env=env,
                stdout=log,
                stderr=subprocess.STDOUT,
                stdin=subprocess.DEVNULL,
                start_new_session=True,
            )
        finally:
            log.close()
        (self.root / "sam.pid").write_text(str(self.process.pid))
        self._wait_ready()

    def _wait_ready(self) -> None:
        deadline = time.monotonic() + BOOT_TIMEOUT_SECONDS
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise RuntimeError(f"SAM exited during boot (code {self.process.returncode}):\n{self.log_tail()}")
            if (
                self._http_ok("/api/v1/agentCards")
                and b"embedded SAM started" in self.log_path.read_bytes()[self._log_from :]
            ):
                return
            time.sleep(0.5)
        self.stop()
        raise RuntimeError(f"SAM was not ready within {BOOT_TIMEOUT_SECONDS} s:\n{self.log_tail()}")

    def _http_ok(self, path: str) -> bool:
        try:
            with urllib.request.urlopen(self.base_url + path, timeout=2) as resp:
                return resp.status == 200
        except (urllib.error.URLError, OSError):
            return False

    def stop(self) -> None:
        """SIGTERM the SAM this server started (its own process group), then SIGKILL after a grace period."""
        proc, self.process = self.process, None
        if proc is None or proc.poll() is not None:
            return
        self._signal(proc, signal.SIGTERM)
        try:
            proc.wait(timeout=STOP_GRACE_SECONDS)
        except subprocess.TimeoutExpired:
            self._signal(proc, signal.SIGKILL)
            proc.wait(timeout=STOP_GRACE_SECONDS)

    @staticmethod
    def _signal(proc: subprocess.Popen, sig: int) -> None:
        """Signal SAM's process group.

        `start_new_session` made the child the leader of a group holding only what it spawned.
        """
        try:
            if os.getpgid(proc.pid) == proc.pid:
                os.killpg(proc.pid, sig)
            else:
                proc.send_signal(sig)
        except ProcessLookupError:
            pass

    def log_tail(self, lines: int = LOG_TAIL_LINES) -> str:
        try:
            text = self.log_path.read_text(errors="replace")
        except OSError:
            return "(no SAM log)"
        return self.scrub("\n".join(text.splitlines()[-lines:]))

    def log_text(self) -> str:
        try:
            return self.scrub(self.log_path.read_text(errors="replace"))
        except OSError:
            return ""

    def scrub(self, text: str) -> str:
        for value in self._secrets:
            if value:
                text = text.replace(value, "<redacted>")
        return _BEARER.sub("Bearer <redacted>", text)


_BEARER = re.compile(r"Bearer\s+[A-Za-z0-9._~+/=-]+")

_DEV_BROKER = """    broker:
      dev_mode: true
      broker_url: ""
      broker_username: default
      broker_password: default
      broker_vpn: default
"""

TOOL_SCOPE_HELD = "tool:ct-claims:pay"
"""The test user holds every scope of SAM's catalog categories but the tool scopes, of which it holds only TOOL_SCOPE_HELD:
so a scenario sees one tool scope SAM grants the user and one it does not (`*` would hold them all).
"""
TOOL_SCOPE_NOT_HELD = "tool:ct-claims:void"
SCOPE_CATEGORIES = (
    "activity",
    "agent",
    "agent_builder",
    "analytics",
    "budget",
    "builder",
    "connector",
    "deployment",
    "entrypoint",
    "evaluation",
    "evidence",
    "evidence_proposal",
    "model_config",
    "notify",
    "profile_provider",
    "project",
    "prompt",
    "rbac",
    "skill",
    "toolset",
    "webui_settings",
    "workflow",
    "workflow_builder",
)
ROLES_YAML = (
    """roles:
  contract_user:
    description: The contract tests' user, allowed everything but one tool scope.
    scopes:
"""
    + "".join(f'      - "{c}:*:*"\n' for c in SCOPE_CATEGORIES)
    + f"""      - "*:*:read_usage"
      - "tool:*:read"
      - "{TOOL_SCOPE_HELD}"
"""
)

USER_YAML = """  {user}:
    roles:
      - contract_user
"""


def _gateway_yaml(namespace: str, task_idle_seconds: int = 0, system_purpose: str = "") -> str:
    """The web gateway: SAM's HTTP API, as SAM-Go's bundled default_gateway.yaml with login off. With
    `task_idle_seconds` its idle timers are short: it warns a task silent that long and cancels one silent twice as
    long (SAM-Go's own are 300 s and 3600 s). With `system_purpose` it tells the agents what the gateway is for, which
    SAM sends with every task as request context."""
    idle = (
        (f"      task_timeout_seconds: {task_idle_seconds}\n      task_hard_timeout_seconds: {2 * task_idle_seconds}\n")
        if task_idle_seconds
        else ""
    )
    purpose = (
        f"      system_purpose: {json.dumps(system_purpose)}\n      inject_system_purpose: true\n"
        if system_purpose
        else ""
    )
    return f"""apps:
  - name: contract_gateway
{_DEV_BROKER}    app_config:
      namespace: {namespace}
      session_secret_key: "${{SESSION_SECRET_KEY}}"
      model:
        model: openai/placeholder
        api_base: http://127.0.0.1:9/v1
        api_key: placeholder
      session_service:
        type: sql
        database_url: "sqlite:///${{SAM_DATA_DIR}}/sam.db"
        default_behavior: PERSISTENT
      artifact_service:
        type: filesystem
        base_path: ${{SAM_DATA_DIR}}
      task_logging:
        enabled: true
      gateway_id: {WEB_GATEWAY_ID}
{idle}{purpose}      fastapi_host: 127.0.0.1
      fastapi_port: ${{FASTAPI_PORT}}
      frontend_use_authorization: false
      authorization_service:
        type: ${{AUTHORIZATION_TYPE}}
"""


def _platform_yaml(namespace: str) -> str:
    """The platform service (RBAC authority, agent control, spend ledger), as SAM-Go's default_platform.yaml."""
    return f"""trust_manager:
  enabled: ${{TRUST_MANAGER_ENABLED}}
  card_publish_interval_seconds: 2
  card_expiration_days: 7
  verification_mode: "strict"
  clock_skew_tolerance_seconds: 300
  jwt_default_ttl_seconds: 3600
  jwt_max_ttl_seconds: 86400
  startup_wait_timeout_seconds: 10
  fail_on_timeout: false

apps:
  - name: contract_platform
{_DEV_BROKER}    app_config:
      component_type: platform
      namespace: {namespace}
      database_url: ""
      frontend_use_authorization: false
      seed_builtin_agents: false
      authorization_service:
        type: ${{AUTHORIZATION_TYPE}}
        role_to_scope_definitions_path: ${{ROLE_DEFINITIONS_PATH}}
        user_to_role_assignments_path: ${{USER_ROLES_PATH}}
"""


def _agent_link_yaml(namespace: str, agents: list[LinkAgent], digests: dict[str, str]) -> str:
    """The agent link entrypoint, configured as docs/agent-link.md describes."""
    entries = []
    for agent in agents:
        entry = [f"            - name: {agent.name}", f"              token_sha256: {digests[agent.name]}"]
        if agent.owner:
            entry.append(f"              owner: {agent.owner}")
        if agent.team:
            entry.append(f"              team: {agent.team}")
        entry.append(f"              peers: [{', '.join(agent.peers)}]")
        if agent.wake_url:
            entry.append(f"              wake:\n                url: {agent.wake_url}")
        if agent.wake_timeout_seconds:
            entry.append(f"                timeout_seconds: {agent.wake_timeout_seconds}")
        entries.append("\n".join(entry))
    return f"""apps:
  - name: contract_agent_link
{_DEV_BROKER}    app_config:
      namespace: {namespace}
      gateway_id: {LINK_GATEWAY_ID}
      frontend_use_authorization: false
      authorization_service:
        type: ${{AUTHORIZATION_TYPE}}
      gateway_adapter:
        type: agent_link
        adapter_config:
          max_inflight: 16
          task_ttl_seconds: 600
          agents:
{chr(10).join(entries)}
"""
