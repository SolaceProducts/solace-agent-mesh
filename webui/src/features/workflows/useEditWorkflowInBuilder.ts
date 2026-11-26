// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";
import { useNavigate } from "react-router-dom";
import { v7 as uuidv7 } from "uuid";

import { agentsApi } from "@/api/agents";
import { workflowsApi } from "@/api/workflows";

import { SESSION_STORAGE_KEYS } from "@/utils/sessionStorageKeys";

import type { AgentCardInfo } from "@/types";

import { resolvePlatformWorkflow } from "./resolveWorkflow";
import { seedWorkflowIntoBuilder } from "./seedWorkflowIntoBuilder";

export function useEditWorkflowInBuilder() {
    const navigate = useNavigate();

    return useCallback(
        async (workflow: AgentCardInfo) => {
            const sessionId = uuidv7();
            const workflowName = workflow.displayName || workflow.name;

            let seeded = false;
            try {
                const [workflows, agents] = await Promise.all([workflowsApi.getAll(), agentsApi.getAll()]);
                const full = resolvePlatformWorkflow(workflows, workflow);
                if (full) {
                    await seedWorkflowIntoBuilder(sessionId, full, agents);
                    seeded = true;
                }
            } catch (err) {
                console.warn("[builder] failed to seed workflow into builder session; falling back to agent load", err);
            }

            sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID, sessionId);
            sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CHIP, JSON.stringify({ label: workflowName, kind: "workflow" }));
            const context = seeded
                ? `The workflow "${workflowName}" is already loaded in this builder session — its build manifest and component configs are present. Do NOT reload, re-fetch, or rebuild it. Briefly confirm it's loaded and ask what I'd like to change.`
                : `Load the existing deployed workflow "${workflow.name}" for editing. The workflow is called "${workflowName}". Fetch its current configuration from the platform and load all its components so I can modify them.`;
            sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT, context);
            sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT, "true");
            navigate("/builder");
        },
        [navigate]
    );
}
