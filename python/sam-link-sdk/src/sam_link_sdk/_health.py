# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The agent's open health problems, as its description reports them."""

from __future__ import annotations

from ._model import HEALTH_SEVERITIES, HealthProblem


class Health:
    def __init__(self) -> None:
        self._problems: dict[str, HealthProblem] = {}

    def set(self, id: str, severity: str, detail: str) -> None:
        if severity not in HEALTH_SEVERITIES:
            raise ValueError(f"severity must be one of {HEALTH_SEVERITIES}")
        self._problems[id] = HealthProblem(id, severity, detail)

    def clear(self, id: str) -> None:
        self._problems.pop(id, None)

    def problems(self) -> tuple[HealthProblem, ...]:
        return tuple(self._problems.values())
