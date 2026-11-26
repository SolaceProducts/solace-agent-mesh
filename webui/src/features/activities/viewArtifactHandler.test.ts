// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { NavigateFunction } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";

import type { ArtifactInfo } from "@/types/chat";

import { viewArtifactHandler } from "./viewArtifactHandler";

function mockChatContext(overrides: Partial<ChatContextValue> = {}): ChatContextValue {
    return {
        handleSwitchSession: vi.fn().mockResolvedValue(undefined),
        setActiveSidePanelTab: vi.fn(),
        setIsSidePanelCollapsed: vi.fn(),
        openArtifactForPreview: vi.fn().mockResolvedValue(null),
        navigateArtifactVersion: vi.fn().mockResolvedValue(null),
        openSidePanelTab: vi.fn(),
        ...overrides,
    } as unknown as ChatContextValue;
}

const ts = new Date().toISOString();

async function flushAnimationFrames(): Promise<void> {
    await new Promise(resolve => requestAnimationFrame(resolve));
    await new Promise(resolve => requestAnimationFrame(resolve));
}

describe("viewArtifactHandler", () => {
    let navigate: NavigateFunction;

    beforeEach(() => {
        navigate = vi.fn() as unknown as NavigateFunction;
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    test("switches session, sets side panel state, opens preview, navigates, and dispatches delayed event", async () => {
        const ctx = mockChatContext();

        await viewArtifactHandler({ filename: "report.md", mimeType: "text/markdown", size: 1024, lastModified: ts, sessionId: "session-1" } as ArtifactInfo, 2, ctx, navigate);

        expect(ctx.handleSwitchSession).toHaveBeenCalledWith("session-1");
        expect(ctx.setActiveSidePanelTab).toHaveBeenCalledWith("files");
        expect(ctx.setIsSidePanelCollapsed).toHaveBeenCalledWith(false);
        expect(ctx.openArtifactForPreview).toHaveBeenCalledWith("report.md", "session-1");
        expect(ctx.navigateArtifactVersion).toHaveBeenCalledWith("report.md", 2, "session-1");
        expect(navigate).toHaveBeenCalledWith("/chat");

        await flushAnimationFrames();
        expect(ctx.openSidePanelTab).toHaveBeenCalledWith("files");
    });

    test("does nothing when artifact has no sessionId", async () => {
        const ctx = mockChatContext();

        await viewArtifactHandler({ filename: "test.md", mimeType: "text/plain", size: 0, lastModified: ts } as ArtifactInfo, undefined, ctx, navigate);

        expect(ctx.handleSwitchSession).not.toHaveBeenCalled();
        expect(navigate).not.toHaveBeenCalled();
    });

    test("skips navigateArtifactVersion when version is undefined", async () => {
        const ctx = mockChatContext();

        await viewArtifactHandler({ filename: "report.md", mimeType: "text/markdown", size: 1024, lastModified: ts, sessionId: "session-1" } as ArtifactInfo, undefined, ctx, navigate);

        expect(ctx.handleSwitchSession).toHaveBeenCalledWith("session-1");
        expect(ctx.navigateArtifactVersion).not.toHaveBeenCalled();
        expect(navigate).toHaveBeenCalledWith("/chat");
    });

    test("calls openSidePanelTab after two animation frames", async () => {
        const ctx = mockChatContext();

        await viewArtifactHandler({ filename: "report.md", mimeType: "text/markdown", size: 1024, lastModified: ts, sessionId: "session-1" } as ArtifactInfo, undefined, ctx, navigate);

        expect(ctx.openSidePanelTab).not.toHaveBeenCalled();

        await flushAnimationFrames();
        expect(ctx.openSidePanelTab).toHaveBeenCalledWith("files");
    });
});
