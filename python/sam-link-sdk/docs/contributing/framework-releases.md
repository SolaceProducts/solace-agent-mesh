# Framework releases and compatibility

What the SDK supports, how CI finds out when a framework or SAM release breaks it, and what happens then. The
maintenance bot in sam-dev-automation follows this page; so does a person fixing a break by hand.

## What is supported

- The claim covers the SDK's `main` and its newest release. For an older release the answer is to install the newest
  patch. The release brief says when users should upgrade.
- Each framework is supported from the lower bound of its extra in `pyproject.toml` up to the newest release that
  passed the full suite. Both ends are tested every day, the lower bound on the oldest supported Python and the newest
  release on the newest Python. The versions in between rely on the framework's own versioning.
- Python 3.11 to 3.14. A version leaves support only after its upstream end of life, in a minor release.
- SAM from the first release with the agent link (`sam_oldest_supported` in `compat/pins.json`) up to `main`. A SAM
  change must keep working with every supported SDK release.

The [framework table](../frameworks/README.md) shows the lowest supported release, the bound in `pyproject.toml`.
The newest tested is the release in `uv.lock` on main. `tests/test_compat_record.py` holds the docs, each adapter's
`TESTED_VERSION` and the Hermes pin to those sources.

## How breaks are found

Most of it happens on pull requests, in `Link SDK CI`:

| What changes | What runs |
|---|---|
| a framework release, through Dependabot (after a 7-day cooldown) | the full suite on the new `uv.lock`: unit tests on the oldest and newest Python, unit tests at the lowest supported versions, contract tests and every end-to-end slice |
| the SDK's code or its dependencies | the same, plus `floor-cells`: each framework installed alone at its lowest supported release, unit and end to end; and `sam-releases`: the contract tests on the oldest supported and the newest SAM release |
| SAM code a link agent depends on | the contract and end-to-end tests, and `released-contract`: the newest SDK release's own contract tests, from its tag, against the pull request's SAM |

Every lock change passes the full suite before it merges, so `uv.lock` on main is the newest tested.

One thing a pull request cannot catch: a user who installs `sam-link-sdk[<extra>]` gets a framework release the day it
ships, a week before Dependabot proposes it. `.github/workflows/python-link-sdk-compat.yml` covers that week. Every
day, each framework that published a release in the last seven days is installed alone at that release and runs its
unit and end-to-end tests on the SAM of main. On Mondays it does the same with pre-releases (never a support claim),
and runs Hermes at the head of its main branch.

A cell, on a pull request or on the schedule, is defined in `compat/frameworks.json` (`cells`): the extras it installs,
the modules they provide, its unit test modules and its end-to-end framework. The same file maps each framework to its
PyPI packages, GitHub repository, release tags, changelog and docs, which is where the maintenance bot reads release
notes and source for a release no Dependabot pull request has proposed yet. Each cell uploads
`compat-<cell>-<point>.json`: the versions it installed, pass, fail or unresolvable, and the ids of the tests that
failed. Nothing a framework prints reaches it.

## What a failure means

| Failure | Meaning |
|---|---|
| a floor cell | the SDK needs something its lower bound does not have |
| a Dependabot pull request, or a release of the day | that framework's release changed something the adapter depends on |
| unresolvable | the extra's dependencies cannot be installed together at that point |
| `sam-releases` | the SDK relies on something an older SAM does not have |
| `released-contract` | a SAM change breaks the SDK users have installed |

## What happens next

In order of preference:

1. It works. The matrix records it, and Dependabot's bump merges after its cooldown.
2. The adapter needs a change. Add a translation layer: pick the hook by what the installed framework has, with
   alternatives in `require()` (`"pkg:Old.attr | pkg:New.attr"`) and `resolve()` in `sam_link_sdk._drift`, and keep
   any other difference in the adapter's own `_compat.py`. The fix is proven on the lower bound and on the new release,
   and ships in the next patch. This is the default.
3. The old and the new cannot both be supported. Raise the lower bound to the oldest release that has what the adapter
   needs, name the missing symbol in the pull request, add a Changed entry to the CHANGELOG, and the next release is a
   minor. This is the last resort.
4. The release is broken upstream (an open issue upstream names the failure). Exclude it with `!=` on the extra and
   hold it; the daily cells retest newer releases and the hold is lifted when one passes.
5. While 2 to 4 are in progress, a `<` bound on `main` keeps users off the release. It protects nobody until a release
   ships, so a breaking framework release asks for a release right away.

A framework that changes behaviour the suite expects is not accepted by editing the expectation. The adapter keeps the
old behaviour through a translation layer, or the framework is held at the last release that behaves as expected and
the change is listed for whoever releases next to decide.

Translation layers have a budget. Each records the oldest framework release it serves, and is deleted when the lower
bound rises past it. More than five in one adapter is a reason to propose raising the lower bound in the release brief.

## What the bot may not change

`ci/frozen-paths.txt` lists the files the maintenance bot may never change: the guardrail tests, the conformance
expectations, the end-to-end and contract suites, the public API snapshot, the compatibility record and source map, the
skip guard and these workflows. The
`frozen-files` job in the required check fails a pull request the bot opened or committed to that changes one, or the
`[tool]` settings of `pyproject.toml`. People change them in pull requests of their own.

## Moving Hermes

Hermes has no releases. `compat/pins.json` pins the commit its tests run against. Move it in a pull request of its own
once the `hermes` cell at `newest` passes, together with the commit quoted in `docs/frameworks/hermes.md`, the example
and `HERMES` in `src/sam_link_sdk/hermes/steps.py`.
