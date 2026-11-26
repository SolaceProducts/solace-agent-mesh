# Contributing

Thank you for your interest in sam-link-sdk.

The public repository is a copy of Solace's source and does not accept issues, pull requests or other outside
contributions. Use this table to find the right place for your request:

| To | Where |
|---|---|
| Report a bug | [support@solace.com](mailto:support@solace.com) |
| Report a security vulnerability | [support@solace.com](mailto:support@solace.com); see [SECURITY.md](SECURITY.md) for what to include |
| Request a feature, ask a question or discuss Agent Mesh with other users | [Solace Community: Solace Agent Mesh](https://community.solace.com/c/solace-agent-mesh) |

The rest of this page is for working on the SDK in Solace's source repository.

## Development setup

You need Python 3.11 or later and [uv](https://docs.astral.sh/uv/).

```bash
uv sync --all-extras
uv run pytest -q
```

The default run is the unit suite and needs nothing else. The contract and end-to-end suites run agents against a real
Solace Agent Mesh build; [docs/contributing/testing.md](docs/contributing/testing.md) says how to run them. 

| Page | What it covers |
|---|---|
| [docs/contributing/testing.md](docs/contributing/testing.md) | The unit, contract and end-to-end suites, lint, types and CI. |
| [docs/contributing/layout.md](docs/contributing/layout.md) | The repository layout and the commands. |
| [docs/contributing/adapters.md](docs/contributing/adapters.md) | What every framework adapter guarantees and how each framework changes it. |
| [docs/contributing/framework-releases.md](docs/contributing/framework-releases.md) | What versions are supported, how CI finds a break and what happens then. |
| [docs/architecture.md](docs/architecture.md) | The layers inside the SDK. |

The user documentation starts at [docs/README.md](docs/README.md).

## Pull requests

- Keep a pull request to one change, and add or update the tests that show it.
- Add a line to the Unreleased section of [CHANGELOG.md](CHANGELOG.md) for anything a user of the package can see.
- `uv run --only-group lint ruff format .` leaves nothing to change and `uv run --only-group lint ruff check .` passes; the `Lint` job in CI runs it (see [testing](docs/contributing/testing.md#lint)).
- `uv run --group dev --group typecheck mypy` passes; the `Type check` job in CI runs it (see [testing](docs/contributing/testing.md#type-check)).
- No code comments, as the repository's `commentcheck` requires: a reason goes in a docstring (or an attribute docstring), anything else is deleted. Shebangs and tool directives such as `# noqa` and `# type: ignore` are the only `#` allowed. `tests/test_comments.py` checks it (see [testing](docs/contributing/testing.md#code-comments)).
- A new runtime dependency needs a licence that is compatible with this project's; say what it is in the pull request.

## Versioning and deprecation

sam-link-sdk follows [Semantic Versioning](https://semver.org). Before 1.0 that means: a minor release (0.1 to 0.2) may
break the public API, and a patch release (0.1.0 to 0.1.1) never does. From 1.0 only a major release breaks it.

**What is public API.** The names in the `__all__` of the modules in `WITH_ALL` in `tests/test_public_api.py` (the
package root, the adapters, `sam_link_sdk.link` and the other documented modules), and the public classes and functions of
the documented modules that have no `__all__` (`WITHOUT_ALL` there). The agent link protocol version
(`sam_link_sdk.link.config.PROTOCOL_VERSION`, the subprotocol `sam-agent-link.v2`) is public too: a new protocol version is a
breaking change. Anything that starts with an underscore, the modules and names not listed there, the examples and the
tests are not public and may change in any release.

**What counts as a change.** Removing or renaming a public name; removing, renaming, reordering or retyping a
parameter; adding a required parameter; changing a default; changing a constant's value. Adding a name or an optional
parameter is not breaking. `tests/test_public_api.py` compares all of this to `tests/fixtures/public_api.json` and
fails on any difference. When the change is intended, add the CHANGELOG entry, decide the version, and regenerate the
snapshot with `SAM_LINK_SDK_UPDATE_API_SNAPSHOT=1 uv run --locked pytest tests/test_public_api.py`; commit the snapshot with
the change so a reviewer sees it in the diff.

**Deprecating.** A name is not removed in the release that announces it. Deprecate it first: make it emit a
`DeprecationWarning` that names what to use instead, and list it in the CHANGELOG under Deprecated. Remove it no earlier
than the next minor release, and list the removal under Removed.

**CHANGELOG.** [CHANGELOG.md](CHANGELOG.md) has a `## Unreleased` section at the top. Each pull request with a change a
user can see adds its line there, under Added, Changed, Deprecated, Removed or Fixed. A release renames the section to
the version.
