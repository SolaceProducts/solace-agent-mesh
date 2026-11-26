// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { MouseEvent } from "react";

import type { UsageDetailTarget } from "./usageDetailTarget";

export function openFromRow(event: MouseEvent<HTMLElement>, target: UsageDetailTarget, onOpen: (target: UsageDetailTarget) => void) {
    if (event.target instanceof Element && event.target.closest("button, a, input")) return;
    event.currentTarget.querySelector("button")?.focus({ preventScroll: true });
    onOpen(target);
}
