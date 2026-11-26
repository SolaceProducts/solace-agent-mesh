// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/client";

import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { ArtifactInfo } from "@/types";

import { resolveArtifactDownloadEndpoint, useDownload } from "./useDownload";

const artifact = (overrides: Partial<ArtifactInfo> = {}): ArtifactInfo => ({
    filename: "sam-offline-evaluations.pptx",
    mimeType: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
    size: 1024,
    lastModified: "2026-06-17T00:00:00Z",
    ...overrides,
});

describe("resolveArtifactDownloadEndpoint", () => {
    it("downloads a session/task artifact by its session even when a project is active (the reported bug)", () => {
        const ep = resolveArtifactDownloadEndpoint(artifact(), { sessionId: "task-session-1", projectId: "proj-123", explicitProjectScope: false });
        expect(ep).toBe("/api/v1/artifacts/task-session-1/sam-offline-evaluations.pptx");
    });

    it("downloads a project Knowledge file via project scope even with a leftover chat session", () => {
        const ep = resolveArtifactDownloadEndpoint(artifact(), { sessionId: "leftover-chat-session", projectId: "proj-knowledge", explicitProjectScope: true });
        expect(ep).toBe("/api/v1/artifacts/null/sam-offline-evaluations.pptx?projectId=proj-knowledge");
    });

    it("downloads a source=project artifact via project scope (surfaced in chat)", () => {
        const ep = resolveArtifactDownloadEndpoint(artifact({ source: "project" }), { sessionId: "chat-session", projectId: "active-proj", explicitProjectScope: false });
        expect(ep).toBe("/api/v1/artifacts/null/sam-offline-evaluations.pptx?projectId=active-proj");
    });

    it("uses project scope as a last resort when there is no session context", () => {
        const ep = resolveArtifactDownloadEndpoint(artifact(), { sessionId: null, projectId: "proj-123", explicitProjectScope: false });
        expect(ep).toBe("/api/v1/artifacts/null/sam-offline-evaluations.pptx?projectId=proj-123");
    });

    it("treats sentinel session strings ('null'/'undefined') as no session", () => {
        const ep = resolveArtifactDownloadEndpoint(artifact(), { sessionId: "null", projectId: "proj-x", explicitProjectScope: false });
        expect(ep).toBe("/api/v1/artifacts/null/sam-offline-evaluations.pptx?projectId=proj-x");
    });

    it("throws when neither session nor project context is available", () => {
        expect(() => resolveArtifactDownloadEndpoint(artifact(), { sessionId: null, projectId: null, explicitProjectScope: false })).toThrow(/No valid session or project context/);
    });
});

describe("useDownload — reports a failed download", () => {
    afterEach(() => {
        vi.restoreAllMocks();
    });

    it("surfaces the failure via displayError instead of throwing out of the caller", async () => {
        const displayError = vi.fn();
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        vi.spyOn(api.webui, "get").mockImplementation((endpoint: unknown): any => (typeof endpoint === "string" && endpoint.startsWith("/api/v1/artifacts/session-1/") ? Promise.reject(new Error("artifact gone")) : Promise.resolve({ data: [] })));

        const wrapper = ({ children }: { children: React.ReactNode }) => <StoryProvider chatContextValues={{ sessionId: "session-1", displayError }}>{children}</StoryProvider>;
        const { result } = renderHook(() => useDownload(), { wrapper });

        await act(async () => {
            await result.current.onDownload(artifact());
        });

        expect(displayError).toHaveBeenCalledWith({ title: "Failed to Download Artifact", error: "artifact gone", errorSource: "api" });
    });
});
