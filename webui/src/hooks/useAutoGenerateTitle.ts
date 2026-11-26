// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";

import { api } from "@/api/client";

import { useIsAutoTitleGenerationEnabled } from "./useIsAutoTitleGenerationEnabled";
import { useTitleGeneration } from "./useTitleGeneration";

interface BackgroundTask {
    taskId: string;
    sessionId?: string;
}

const generatedSessions = new Set<string>();

export function useAutoGenerateTitle() {
    const autoTitleGenerationEnabled = useIsAutoTitleGenerationEnabled();
    const { generateTitle } = useTitleGeneration();

    const autoGenerateTitle = useCallback(
        async (sessionId: string, userText: string, agentText: string): Promise<void> => {
            if (!autoTitleGenerationEnabled) return;
            if (!sessionId || generatedSessions.has(sessionId)) return;
            if (!userText) return;

            generatedSessions.add(sessionId);
            try {
                await generateTitle(sessionId, userText, agentText);
            } catch (error) {
                generatedSessions.delete(sessionId);
                console.error("[useAutoGenerateTitle] Title generation failed:", error);
            }
        },
        [autoTitleGenerationEnabled, generateTitle]
    );

    const autoGenerateTitleForTask = useCallback(
        async (taskId: string, backgroundTasks: BackgroundTask[]): Promise<void> => {
            if (!autoTitleGenerationEnabled) return;

            const bgTask = backgroundTasks.find(t => t.taskId === taskId);
            let taskSessionId = bgTask?.sessionId;

            if (!taskSessionId || taskSessionId.trim() === "") {
                try {
                    const statusData = await api.webui.get(`/api/v1/tasks/${taskId}/status`);
                    taskSessionId = statusData?.task?.context_id || statusData?.task?.contextId;
                } catch {
                    return;
                }
            }

            if (!taskSessionId || taskSessionId.trim() === "" || generatedSessions.has(taskSessionId)) {
                return;
            }

            generatedSessions.add(taskSessionId);

            try {
                const titleData = await api.webui.get(`/api/v1/tasks/${taskId}/titleData`);
                const userMessageText = titleData?.userMessage || "";
                const agentResponseText = titleData?.agentResponse || "";

                if (userMessageText) {
                    await generateTitle(taskSessionId, userMessageText, agentResponseText);
                }
            } catch (error) {
                generatedSessions.delete(taskSessionId);
                console.error("[useAutoGenerateTitle] Error generating title for task:", error);
            }
        },
        [autoTitleGenerationEnabled, generateTitle]
    );

    return {
        autoTitleGenerationEnabled,
        autoGenerateTitle,
        autoGenerateTitleForTask,
    };
}
