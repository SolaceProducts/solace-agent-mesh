// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { Session } from "@/types";

import { useShareSession } from "./useShareSession";

const session = { id: "s-1", name: "Chat One", createdAt: "", updatedAt: "" } as Session;

const dispatchOpen = (s: Session) =>
    act(() => {
        globalThis.dispatchEvent(new CustomEvent("open-share-session-dialog", { detail: { session: s } }));
    });

describe("useShareSession", () => {
    test("opens with the dispatched session on the open-share-session-dialog event", () => {
        const { result } = renderHook(() => useShareSession());
        expect(result.current.isShareDialogOpen).toBe(false);
        dispatchOpen(session);
        expect(result.current.isShareDialogOpen).toBe(true);
        expect(result.current.sessionToShare).toBe(session);
    });

    test("setShareDialogOpen(false) closes and clears the session", () => {
        const { result } = renderHook(() => useShareSession());
        dispatchOpen(session);
        act(() => result.current.setShareDialogOpen(false));
        expect(result.current.isShareDialogOpen).toBe(false);
        expect(result.current.sessionToShare).toBeNull();
    });

    test("setShareDialogOpen(true) keeps the session", () => {
        const { result } = renderHook(() => useShareSession());
        dispatchOpen(session);
        act(() => result.current.setShareDialogOpen(true));
        expect(result.current.isShareDialogOpen).toBe(true);
        expect(result.current.sessionToShare).toBe(session);
    });

    test("closeShareDialog closes and clears the session", () => {
        const { result } = renderHook(() => useShareSession());
        dispatchOpen(session);
        act(() => result.current.closeShareDialog());
        expect(result.current.isShareDialogOpen).toBe(false);
        expect(result.current.sessionToShare).toBeNull();
    });

    test("removes the listener on unmount (no state update after unmount)", () => {
        const { result, unmount } = renderHook(() => useShareSession());
        unmount();
        dispatchOpen(session);
        expect(result.current.isShareDialogOpen).toBe(false);
    });
});
