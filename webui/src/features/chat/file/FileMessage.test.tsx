// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { artifactKeys } from "@/api/artifacts";

import { ChatContext, type ChatContextValue, defaultChatContextValue } from "@/providers/contexts/ChatContext";

import type { ArtifactInfo } from "@/types";

import { FileMessage } from "./FileMessage";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn(() => Promise.resolve({ data: [] as ArtifactInfo[] })) }));
vi.mock("@/api/client", () => ({ api: { webui: { get: getMock } }, getErrorFromResponse: vi.fn() }));

const SESSION_ID = "session_deadbeef";
const artifact: ArtifactInfo = { filename: "report.md", mimeType: "text/markdown", size: 5, lastModified: "2026-06-08T00:00:00Z" };

function renderWithCtx(ui: React.ReactElement, ctx: Partial<ChatContextValue> | null, seeded: ArtifactInfo[]) {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    qc.setQueryData(artifactKeys.session(SESSION_ID), seeded);
    const tree = ctx ? <ChatContext.Provider value={{ ...defaultChatContextValue, ...ctx }}>{ui}</ChatContext.Provider> : ui;
    return render(<QueryClientProvider client={qc}>{tree}</QueryClientProvider>);
}

describe("FileMessage — shared session gating", () => {
    test("read-only shared session resolves the artifact from the seeded cache and fires no fetch", () => {
        getMock.mockClear();
        renderWithCtx(<FileMessage filename="report.md" mimeType="text/markdown" />, { sessionId: SESSION_ID, isSharedSession: true }, [artifact]);

        expect(screen.getByText("report.md")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Preview" })).toBeInTheDocument();
        expect(getMock).not.toHaveBeenCalled();
    });

    test("renders read-only without a ChatContext (no fetch, no preview)", () => {
        getMock.mockClear();
        renderWithCtx(<FileMessage filename="data.csv" readOnly />, null, []);

        expect(screen.getByText("data.csv")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Preview" })).not.toBeInTheDocument();
        expect(getMock).not.toHaveBeenCalled();
    });
});
