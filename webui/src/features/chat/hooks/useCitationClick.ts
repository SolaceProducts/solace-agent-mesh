// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";

import { useChatMessageData } from "@/providers/contexts";

import type { Citation } from "@/utils/citations";

export function useCitationClick(taskId?: string) {
    const { setTaskIdInSidePanel, setExpandedDocumentFilename, openSidePanelTab } = useChatMessageData();

    return useCallback(
        (citation: Citation) => {
            if (taskId) {
                setTaskIdInSidePanel(taskId);
                if (citation.source?.filename) {
                    setExpandedDocumentFilename(citation.source.filename);
                }
                openSidePanelTab("rag");
            }
        },
        [taskId, setTaskIdInSidePanel, setExpandedDocumentFilename, openSidePanelTab]
    );
}
