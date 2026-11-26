// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AgentDeploymentAction } from "./types";

export function actionLabel(action: AgentDeploymentAction, tense: "noun" | "past" = "noun"): string {
    if (tense === "past") {
        if (action === "deploy") return "Deployed";
        if (action === "undeploy") return "Undeployed";
        return "Updated";
    }
    if (action === "deploy") return "Deployment";
    if (action === "undeploy") return "Undeployment";
    return "Update";
}

export function deployFailureMessage(resp: { status?: string; errorMessage?: string } | null | undefined, label: string): string | null {
    if (!resp?.status || resp.status === "success") return null;
    return resp.errorMessage ?? `${label} reported status: ${resp.status}`;
}
