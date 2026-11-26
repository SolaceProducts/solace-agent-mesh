// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";

export interface SubmitPlanResponseInput {
    planId: string;
    agentName: string;
    steps?: string[];
}

export const submitPlanResponse = async ({ planId, agentName, steps }: SubmitPlanResponseInput): Promise<void> => {
    await api.webui.post("/api/v1/research/planResponse", {
        planId,
        agentName,
        action: "start",
        steps,
    });
};
