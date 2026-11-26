// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";

import type { ProductAnalyticsErrorSource } from "@/common/product-analytics/types";

import { getErrorMessage } from "@/utils/api";

import { downloadBlob } from "@/utils";

interface DownloadStimNotifiers {
    addNotification: (message: string, type?: "success" | "info" | "warning") => void;
    displayError: (error: { title: string; error: string; errorSource?: ProductAnalyticsErrorSource }) => void;
}

export async function downloadStim(taskId: string, { addNotification, displayError }: DownloadStimNotifiers): Promise<void> {
    try {
        const response = await api.webui.get(`/api/v1/tasks/${taskId}`, { fullResponse: true });
        if (!response.ok) {
            throw new Error(`Failed to download task log: ${await refusalDetail(response)}`);
        }
        const blob = await response.blob();
        downloadBlob(blob, `${taskId}.stim`);
        addNotification("Task log downloaded", "success");
    } catch (error) {
        displayError({ title: "Failed to Download Task Log", error: getErrorMessage(error, "An unknown error occurred while downloading the task log."), errorSource: "api" });
    }
}

async function refusalDetail(response: Response): Promise<string> {
    const fallback = response.statusText || `HTTP ${response.status}`;
    const body = (await response.json().catch(() => null)) as { message?: unknown } | null;
    return typeof body?.message === "string" && body.message !== "" ? body.message : fallback;
}
