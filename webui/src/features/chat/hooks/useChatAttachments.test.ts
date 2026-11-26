// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act } from "react";
import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { ArtifactWithSession } from "@/api/artifacts";
import { api } from "@/api/client";

import { useChatAttachments } from "./useChatAttachments";

const artifact = (overrides: Partial<ArtifactWithSession> = {}): ArtifactWithSession => ({
    filename: "report.pdf",
    mimeType: "application/pdf",
    size: 100,
    lastModified: "2026-06-17T00:00:00Z",
    sessionId: "session-1",
    sessionName: "My Chat",
    ...overrides,
});

function setup() {
    const addNotification = vi.fn();
    const displayError = vi.fn();
    const handleSwitchSession = vi.fn();
    const navigate = vi.fn();

    const { result } = renderHook(() =>
        useChatAttachments({
            isResponding: false,
            addNotification,
            displayError,
            handleSwitchSession,
            navigate,
        })
    );

    return { result, addNotification, displayError };
}

describe("useChatAttachments — preview artifact download failure", () => {
    afterEach(() => {
        vi.restoreAllMocks();
    });

    it("reports the download failure via displayError instead of throwing", async () => {
        vi.spyOn(api.webui, "get").mockRejectedValueOnce(new Error("blob store unreachable"));
        const { result, displayError, addNotification } = setup();

        await act(async () => {
            await result.current.handlePreviewArtifactDownload(artifact());
        });

        expect(displayError).toHaveBeenCalledWith({ title: "Download Failed", error: "blob store unreachable", errorSource: "api" });
        expect(addNotification).not.toHaveBeenCalled();
    });
});
