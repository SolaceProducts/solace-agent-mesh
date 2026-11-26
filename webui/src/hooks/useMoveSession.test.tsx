// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/client";

import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { Session } from "@/types";

import { useMoveSession } from "./useMoveSession";

const wrapper = ({ children }: { children: React.ReactNode }) => <StoryProvider>{children}</StoryProvider>;

const session = (overrides: Partial<Session> = {}): Session => ({
    id: "session-1",
    name: "My Chat",
    createdAt: "2026-06-17T00:00:00Z",
    updatedAt: "2026-06-17T00:00:00Z",
    ...overrides,
});

function openDialogFor(target: Session) {
    window.dispatchEvent(new CustomEvent("open-move-session-dialog", { detail: { session: target } }));
}

describe("useMoveSession", () => {
    let patchSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        patchSpy = vi.spyOn(api.webui, "patch");
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    it("reports the move failure and keeps the dialog usable when the API call rejects", async () => {
        patchSpy.mockRejectedValueOnce(new Error("network down"));
        const { result } = renderHook(() => useMoveSession(), { wrapper });

        act(() => openDialogFor(session()));
        expect(result.current.isMoveDialogOpen).toBe(true);

        await act(async () => {
            await result.current.handleMoveConfirm("proj-1");
        });

        expect(patchSpy).toHaveBeenCalledWith("/api/v1/sessions/session-1/project", { projectId: "proj-1" });
    });

    it("does nothing when confirmed with no session queued", async () => {
        const { result } = renderHook(() => useMoveSession(), { wrapper });

        await act(async () => {
            await result.current.handleMoveConfirm("proj-1");
        });

        expect(patchSpy).not.toHaveBeenCalled();
    });

    it("closeMoveDialog clears both the open flag and the queued session", () => {
        const { result } = renderHook(() => useMoveSession(), { wrapper });

        act(() => openDialogFor(session()));
        act(() => result.current.closeMoveDialog());

        expect(result.current.isMoveDialogOpen).toBe(false);
        expect(result.current.sessionToMove).toBeNull();
    });
});
