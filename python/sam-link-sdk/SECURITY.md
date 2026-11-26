# Security Policy

If you find a security vulnerability in sam-link-sdk, report it privately by email to
[support@solace.com](mailto:support@solace.com). Please do not open a public GitHub issue or discussion for it.

Include:

- The sam-link-sdk version (`pip show sam-link-sdk`) and the Python version.
- The framework adapter involved, if any, and its version.
- The steps to reproduce the vulnerability, and its impact.

Solace Support triages the report and passes anything concrete to the security team. You will get an acknowledgement
from Support.

## Scope

sam-link-sdk is the client side of the agent link. Solace Agent Mesh verifies users and the agent's token, checks the
agent's required scopes and enforces the stop. The SDK enforces each tool's scopes and asks for approvals inside the
agent's own process; see [docs/security.md](docs/security.md) for what it does and does not promise. A weakness in Solace Agent Mesh itself should be reported the same way.
