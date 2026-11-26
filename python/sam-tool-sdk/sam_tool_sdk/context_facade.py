# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""
SandboxToolContextFacade - Tool context interface for sandboxed execution.

This module provides a context facade that tools use inside the sandbox.
It matches the public API of ToolContextFacade so that the same tool code
can run in both in-process and sandbox environments.

Key differences from the in-process ToolContextFacade:
- Status messages go via named pipe (worker forwards to the broker)
- Artifacts are loaded from the local sandbox filesystem (preloaded by worker)
- No direct broker or artifact service access
- send_signal() raises NotImplementedError (the STR worker has no signal
  channel from the sandbox — only text status via the named pipe)
"""

import errno
import io
import json
import logging
import os
import sys
import threading
import time
from pathlib import Path
from typing import Any, Dict, List, Optional, Union

from .ipc import IPCClient, IPCError
from .tool_options import VolumeMount

log = logging.getLogger(__name__)

def _connect_ipc_eagerly_or_degrade(socket_path: str) -> Optional[IPCClient]:
    try:
        client = IPCClient(socket_path)
        client._ensure_connected()
        return client
    except IPCError as e:
        print(
            f"samtoolsdk: IPC connection failed: {e}",
            file=sys.stderr,
        )
        return None

class SandboxToolContextFacade:
    """
    Provides the same tool-facing interface as ToolContextFacade, backed by
    local filesystem and named pipes instead of broker communication.

    Portable API (matches ToolContextFacade):
        - session_id, user_id, app_name, task_id properties
        - get_config(key, default)
        - async load_artifact(filename, version, as_text)
        - async load_artifact_metadata(filename, version)
        - async list_artifacts() -> List[str]
        - async artifact_exists(filename) -> bool
        - send_status(message) -> bool
        - call_llm(system_prompt, user_prompt, temperature) -> str
        - state property
        - raw_tool_context property

    Legacy methods (backward compat for sandbox-only tools):
        - save_artifact(), save_artifact_text()
        - list_output_artifacts()
    """

    def __init__(
        self,
        *,
        status_pipe_path: str,
        tool_config: Dict[str, Any],
        artifacts: Dict[str, Dict[str, Any]],
        output_dir: str,
        user_id: str,
        session_id: str,
        app_name: str = "",
        artifact_paths: Optional[Dict[str, str]] = None,
        auth_token: str = "",
        task_id: str = "",
        ipc_socket_path: str = "",
        volume_mounts: Optional[List[VolumeMount]] = None,
        a2a_user_config: str = "",
    ):
        """
        Initialize the sandbox tool context.

        Args:
            status_pipe_path: Path to the named pipe for status messages
            tool_config: Tool-specific configuration dict
            artifacts: Mapping of filename to artifact info dict containing:
                       {"local_path": str, "mime_type": str, "version": int}
            output_dir: Directory for tool to write output artifacts
            user_id: User ID from the invocation context
            session_id: Session ID from the invocation context
            app_name: Application name from the invocation context
            artifact_paths: Legacy mapping of param_name to local path (backward compat)
            task_id: Agent task ID, used by HIL workflow to route the
                UserInputRequest back to the originating task.
        """
        self._status_pipe_path = status_pipe_path
        self._tool_config = tool_config or {}
        self._output_dir = Path(output_dir)
        self._user_id = user_id
        self._session_id = session_id
        self._app_name = app_name
        self._task_id = task_id
        self._ipc_socket_path = ipc_socket_path
        self._ipc_client: Optional[IPCClient] = None
        if ipc_socket_path:
            self._ipc_client = _connect_ipc_eagerly_or_degrade(ipc_socket_path)
        self._volume_mounts: List[VolumeMount] = volume_mounts or []

        self._unsigned_user_config: Dict[str, Any] = {}
        if a2a_user_config:
            try:
                parsed = json.loads(a2a_user_config)
                if isinstance(parsed, dict):
                    self._unsigned_user_config = parsed
            except (ValueError, TypeError) as e:
                log.warning("Failed to parse a2a_user_config blob: %s", e)

        self._state: Dict[str, Any] = {"a2a_context": {"a2a_user_config": self._unsigned_user_config}}
        self._auth_token = auth_token
        self._status_pipe_lock = threading.Lock()
        self._status_pipe_file: Optional[io.FileIO] = None

        self._artifacts: Dict[str, Dict[str, Any]] = artifacts or {}

        self._artifact_paths: Dict[str, str] = artifact_paths or {}

        self._output_dir.mkdir(parents=True, exist_ok=True)

    @property
    def session_id(self) -> str:
        """Get the current session ID."""
        return self._session_id

    @property
    def user_id(self) -> str:
        """Get the current user ID."""
        return self._user_id

    @property
    def app_name(self) -> str:
        """Get the application name."""
        return self._app_name

    @property
    def task_id(self) -> str:
        """Get the agent task ID for HIL response routing.

        Empty string when the invocation has no upstream task (e.g. direct
        tool invocation outside an agent task). Tools that issue HIL requests
        should pass this through so the response routes back to the right task.
        """
        return self._task_id

    @property
    def volume_mounts(self) -> List[VolumeMount]:
        """List of volumes mounted into the sandbox for this invocation.

        Empty when no volumes were provisioned. Mirrors the Go SDK's
        ``ToolContext.VolumeMounts`` field. Tools that need to read or write
        to a declared mount path should use ``get_volume_mount_path()``.
        """
        return list(self._volume_mounts)

    def get_volume_mount_path(self, mount_path: str) -> Optional[str]:
        """Return the resolved path for the volume declared at ``mount_path``.

        Returns ``None`` if the tool did not declare (or was not provisioned
        with) a volume at the requested mount path. In bwrap mode the
        returned path matches the declared mount path; in direct mode it is
        the physical host path.
        """
        for vm in self._volume_mounts:
            if vm.mount_path == mount_path:
                return vm.mount_path
        return None

    @property
    def auth_token(self) -> str:
        """Get the OAuth access token for this tool invocation.

        Returns an empty string if the tool has no auth config or no token
        is available. Tools should check for an empty string and return
        an AUTH_REQUIRED error if they need authentication.
        """
        return self._auth_token

    def get_auth_token(self) -> str:
        """Get the OAuth access token for this tool invocation.

        Returns an empty string if no token is available.
        """
        return self._auth_token

    @property
    def raw_tool_context(self):
        """Get the underlying ADK ToolContext. Not available in sandbox."""
        return None

    @property
    def state(self) -> Dict[str, Any]:
        """Get the tool context state dictionary."""
        return self._state

    @property
    def user_profile(self) -> Dict[str, Any]:
        """Get the authenticated user's profile from the gateway user-config blob.

        Returns the ``user_profile`` object the gateway projected into
        ``A2AUserConfig``, or an empty dict when no profile was forwarded. Keys
        are camelCase as the gateway emits them (``id``, ``workEmail``,
        ``displayName``, ``jobTitle``, ``department``, ``location``) plus any
        directory fields the profile provider passed through. Only ``id`` and
        the snake_case ``work_email`` are pinned to the caller's verified JWT
        by the STR worker; everything else is forwarded unsigned. Suitable for
        scoping, context and audit (prefer ``id``), never for authorization.
        """
        profile = self._unsigned_user_config.get("user_profile")
        return profile if isinstance(profile, dict) else {}

    def get_config(self, key: str, default: Any = None) -> Any:
        """
        Get a value from the tool configuration.

        Args:
            key: The configuration key to look up
            default: Default value if key is not found

        Returns:
            The configuration value or the default
        """
        return self._tool_config.get(key, default)

    def get_config_string(self, key: str, default: str = "") -> str:
        """Return a string-typed config value, falling back on type mismatch.

        Mirrors the Go SDK's ``ToolContext.GetConfigString``: when the
        value present at ``key`` is not a ``str`` the default is returned
        instead of coercing or raising. Useful for tools that read
        operator-supplied config and want a single safe accessor.
        """
        value = self._tool_config.get(key, default)
        if isinstance(value, str):
            return value
        return default

    async def load_artifact(
        self,
        filename: str,
        version: Union[int, str] = "latest",
        as_text: bool = False,
    ) -> Union[bytes, str]:
        """
        Load artifact content from the preloaded artifacts.

        Args:
            filename: The artifact filename to load
            version: Version to load (ignored in sandbox -- only preloaded version available)
            as_text: If True, decode bytes to string (UTF-8)

        Returns:
            The artifact content as bytes, or as string if as_text=True

        Raises:
            FileNotFoundError: If the artifact does not exist
            ValueError: If the artifact cannot be loaded
        """
        info = self._artifacts.get(filename)
        if not info:
            raise FileNotFoundError(
                f"Artifact '{filename}' not found in preloaded artifacts"
            )

        local_path = Path(info["local_path"])
        if not local_path.exists():
            raise FileNotFoundError(
                f"Artifact file not found on disk: {local_path}"
            )

        try:
            content = local_path.read_bytes()
        except Exception as e:
            raise ValueError(f"Failed to read artifact '{filename}': {e}") from e

        log.debug(
            "Loaded artifact: %s (%d bytes, version=%s)",
            filename,
            len(content),
            info.get("version", "?"),
        )

        if as_text:
            return content.decode("utf-8") if isinstance(content, bytes) else content
        return content

    async def load_artifact_metadata(
        self,
        filename: str,
        version: Union[int, str] = "latest",
    ) -> Dict[str, Any]:
        """
        Load artifact metadata without loading the content.

        Args:
            filename: The artifact filename
            version: Version (ignored in sandbox)

        Returns:
            Dictionary containing artifact metadata

        Raises:
            FileNotFoundError: If the artifact does not exist
        """
        info = self._artifacts.get(filename)
        if not info:
            raise FileNotFoundError(
                f"Artifact '{filename}' not found in preloaded artifacts"
            )

        local_path = Path(info["local_path"])
        size_bytes = local_path.stat().st_size if local_path.exists() else 0

        return {
            "mime_type": info.get("mime_type", "application/octet-stream"),
            "size_bytes": size_bytes,
            "version": info.get("version", 0),
        }

    async def list_artifacts(self) -> List[str]:
        """
        List all artifact filenames available in this session.

        Returns:
            List of artifact filenames
        """
        return list(self._artifacts.keys())

    async def artifact_exists(self, filename: str) -> bool:
        """
        Check if an artifact exists in the preloaded artifacts.

        Args:
            filename: The artifact filename to check

        Returns:
            True if the artifact exists, False otherwise
        """
        return filename in self._artifacts

    _STATUS_PIPE_OPEN_RETRIES = 5
    _STATUS_PIPE_OPEN_DELAY_S = 0.05

    def _open_status_pipe(self) -> Optional[io.FileIO]:
        """Open the status pipe non-blocking with ENXIO retry.

        Returns ``None`` if the reader never showed up (we silently skip
        status updates rather than block tool execution — same policy as
        the Go SDK's statusWriter).
        """
        for attempt in range(self._STATUS_PIPE_OPEN_RETRIES):
            try:
                fd = os.open(
                    self._status_pipe_path,
                    os.O_WRONLY | os.O_NONBLOCK,
                )
                return io.FileIO(fd, mode="w", closefd=True)
            except OSError as e:
                if e.errno == errno.ENXIO and attempt < self._STATUS_PIPE_OPEN_RETRIES - 1:
                    time.sleep(self._STATUS_PIPE_OPEN_DELAY_S)
                    continue
                log.debug("opening status pipe failed: %s", e)
                return None
        return None

    def send_status(self, message: str) -> bool:
        """
        Send a status message to be forwarded to the agent/user.

        Writes the status message to a named pipe. The pipe is opened
        lazily on the first call with non-blocking mode + ENXIO retry,
        and the handle is reused for subsequent writes. Matches the Go
        SDK's statusWriter behavior so a slow STR reader does not
        deadlock the tool.

        Args:
            message: The status message text

        Returns:
            True if the message was sent successfully, False otherwise
        """
        if not self._status_pipe_path:
            log.debug("No status pipe configured, skipping status: %s", message)
            return False

        with self._status_pipe_lock:
            if self._status_pipe_file is None:
                self._status_pipe_file = self._open_status_pipe()
                if self._status_pipe_file is None:
                    return False
            payload = (json.dumps({"status": message}) + "\n").encode("utf-8")
            try:
                self._status_pipe_file.write(payload)
            except BrokenPipeError:
                log.warning("Status pipe broken, worker may have stopped reading")
                return False
            except BlockingIOError:
                log.warning("Status pipe write would block, dropping message: %s", message)
                return False
            except Exception as e:
                log.warning("Failed to send status message: %s", e)
                return False
            return True

    def call_llm(
        self,
        system_prompt: str,
        user_prompt: str,
        temperature: float = 0.7,
    ) -> str:
        """Make an LLM call from inside the sandbox, proxied through the STR.

        The call runs on the STR's tool model, not the calling agent's:
        the ``_model_alias`` key of the tool's ``tool_config`` when set,
        otherwise the STR's ``tool_model_alias`` (``general`` by default).

        Routes a JSON-RPC request over the Unix socket the STR worker
        opened for this tool invocation when the facade was constructed.
        Raises IPCError if no socket was provided in runner_args.json, if
        the eager connect at construction failed, or if the call fails.

        Args:
            system_prompt: System-prompt text for the LLM call.
            user_prompt: User-prompt text for the LLM call.
            temperature: Sampling temperature (default 0.7).

        Returns:
            The generated text from the LLM.
        """
        if self._ipc_client is None:
            raise IPCError(
                "LLM calls not available: no IPC connection to STR"
            )
        return self._ipc_client.call_llm(system_prompt, user_prompt, temperature)

    def send_signal(self, signal_data: Any, skip_buffer_flush: bool = False) -> bool:
        """
        Send a custom structured signal. Not supported in the STR sandbox.

        The Go STR worker exposes only a text status channel to sandboxed
        tools (the named pipe behind ``send_status``); there is no wire
        contract for arbitrary signal payloads. A tool that genuinely
        depends on signals (e.g. a human-in-the-loop pause) would hang
        waiting on a response the runtime never solicits, so this fails
        loud rather than silently dropping the signal.

        Raises:
            NotImplementedError: always, in the sandbox environment.
        """
        raise NotImplementedError(
            "send_signal is not supported in the STR sandbox; use send_status() "
            "for user-visible progress updates"
        )

    def load_artifact_by_param(self, param_name: str) -> Optional[bytes]:
        """
        Load a preloaded artifact by its parameter name (legacy API).

        Args:
            param_name: The parameter name associated with the artifact

        Returns:
            The artifact content as bytes, or None if not found
        """
        file_path = self._artifact_paths.get(param_name)
        if not file_path:
            log.warning("No artifact found for parameter: %s", param_name)
            return None

        try:
            path = Path(file_path)
            if not path.exists():
                log.warning("Artifact file does not exist: %s", file_path)
                return None
            return path.read_bytes()
        except Exception as e:
            log.error("Failed to load artifact %s: %s", param_name, e)
            return None

    def save_artifact(
        self,
        filename: str,
        content: bytes,
        mime_type: str = "application/octet-stream",
    ) -> str:
        """
        Save an output artifact to the output directory (legacy API).

        Prefer using ToolResult with DataObject for portable tools.

        Args:
            filename: Name for the artifact file
            content: The artifact content
            mime_type: MIME type

        Returns:
            The local file path where the artifact was saved
        """
        file_path = self._output_dir / filename
        file_path.write_bytes(content)
        log.debug("Saved output artifact: %s (%d bytes)", filename, len(content))
        return str(file_path)

    def save_artifact_text(
        self,
        filename: str,
        content: str,
        encoding: str = "utf-8",
        mime_type: str = "text/plain",
    ) -> str:
        """
        Save a text output artifact (legacy API).

        Args:
            filename: Name for the artifact file
            content: The text content
            encoding: Text encoding (default: utf-8)
            mime_type: MIME type (default: text/plain)

        Returns:
            The local file path where the artifact was saved
        """
        return self.save_artifact(filename, content.encode(encoding), mime_type)

    def list_output_artifacts(self) -> list[str]:
        """
        List all output artifacts created so far (legacy API).

        Returns:
            List of filenames in the output directory
        """
        if not self._output_dir.exists():
            return []
        return [f.name for f in self._output_dir.iterdir() if f.is_file()]

    def __repr__(self) -> str:
        return (
            f"SandboxToolContextFacade(app={self._app_name}, "
            f"user={self._user_id}, session={self._session_id})"
        )
