// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { RuntimeStatusValue } from "@/types";

export const STATUS_COLOUR_MAP: Record<string, string> = {
    not_deployed: "bg-(--secondary-text-wMain)",
    deployed: "bg-(--success-wMain)",
    deploy_failed: "bg-(--error-wMain)",
    stopped: "bg-(--warning-wMain)",
    disconnected: "bg-(--warning-wMain)",
    active: "bg-(--success-wMain)",
    degraded: "bg-(--warning-wMain)",
    unknown: "bg-(--secondary-text-wMain)",
    registered: "bg-(--success-wMain)",
};

export const STATUS_LABEL_MAP: Record<string, string> = {
    not_deployed: "Not Deployed",
    deployed: "Deployed",
    deploy_failed: "Deployment Failed",
    stopped: "Not Running",
    disconnected: "Disconnected",
    active: "Running",
    degraded: "Degraded",
    unknown: "Unknown",
    registered: "Registered",
};

export function isOperationalStatus(status: RuntimeStatusValue | undefined): boolean {
    return status === "running" || status === "degraded";
}
