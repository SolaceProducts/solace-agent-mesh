// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Download } from "lucide-react";

import { useChatContext } from "@/providers/contexts";

import { useIsTaskLoggingEnabled } from "@/hooks";
import { Button } from "@/ui";

import { downloadStim } from "./downloadStim";

export function StimDownloadButton({ taskId, unavailableReason, className }: Readonly<{ taskId: string | null | undefined; unavailableReason?: string; className?: string }>) {
    const taskLoggingEnabled = useIsTaskLoggingEnabled();
    const { addNotification, displayError } = useChatContext();

    if (!taskLoggingEnabled) return null;

    return (
        <Button
            variant="ghost"
            size="icon"
            className={className}
            aria-label="Download Task Log (.stim)"
            tooltip={unavailableReason ?? "Download Task Log (.stim)"}
            disabled={!taskId}
            aria-disabled={unavailableReason !== undefined || undefined}
            data-testid="download-task-log"
            onClick={() => {
                if (taskId && unavailableReason === undefined) void downloadStim(taskId, { addNotification, displayError });
            }}
        >
            <Download />
        </Button>
    );
}
