// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { Session } from "@/types";

const navigate = vi.fn();
const updateSessionName = vi.fn(() => Promise.resolve());
const openSessionDeleteModal = vi.fn();
const addNotification = vi.fn();
const generateTitle = vi.fn(() => Promise.resolve());
const renameMutate = vi.fn();
let renameIsPending = false;

vi.mock("react-router-dom", async () => {
    const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
    return { ...actual, useNavigate: () => navigate };
});

vi.mock("@/api/sessions", () => ({
    useRenameSessionWithAI: () => ({ mutate: renameMutate, isPending: renameIsPending }),
}));

vi.mock("@/hooks", () => ({
    useTitleGeneration: () => ({ generateTitle }),
}));

vi.mock("@/providers/contexts", () => ({
    useChatContext: () => ({ updateSessionName, openSessionDeleteModal, addNotification }),
}));

import { useSessionActions } from "./useSessionActions";

const session = { id: "s-1", name: "Old Name", createdAt: "", updatedAt: "", project: { id: "p-1", name: "Proj" } } as Session;

describe("useSessionActions", () => {
    beforeEach(() => {
        navigate.mockReset();
        updateSessionName.mockReset();
        openSessionDeleteModal.mockReset();
        addNotification.mockReset();
        generateTitle.mockReset();
        renameMutate.mockReset();
        renameIsPending = false;
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    test("move dispatches open-move-session-dialog with the session", () => {
        const spy = vi.spyOn(window, "dispatchEvent");
        const { result } = renderHook(() => useSessionActions());
        act(() => result.current.move(session));
        const event = spy.mock.calls.at(-1)?.[0] as CustomEvent;
        expect(event.type).toBe("open-move-session-dialog");
        expect(event.detail.session).toBe(session);
    });

    test("share dispatches open-share-session-dialog with the session", () => {
        const spy = vi.spyOn(window, "dispatchEvent");
        const { result } = renderHook(() => useSessionActions());
        act(() => result.current.share(session));
        const event = spy.mock.calls.at(-1)?.[0] as CustomEvent;
        expect(event.type).toBe("open-share-session-dialog");
        expect(event.detail.session).toBe(session);
    });

    test("remove opens the delete modal for the session", () => {
        const { result } = renderHook(() => useSessionActions());
        act(() => result.current.remove(session));
        expect(openSessionDeleteModal).toHaveBeenCalledWith(session);
    });

    test("goToProject navigates to the session's project", () => {
        const { result } = renderHook(() => useSessionActions());
        act(() => result.current.goToProject(session));
        expect(navigate).toHaveBeenCalledWith("/projects/p-1");
    });

    test("beginRename then commitRename persists the edited name", async () => {
        const { result } = renderHook(() => useSessionActions());
        act(() => result.current.beginRename(session));
        expect(result.current.editingSessionId).toBe("s-1");
        act(() => result.current.setEditingSessionName("New Name"));
        await act(async () => {
            await result.current.commitRename();
        });
        expect(updateSessionName).toHaveBeenCalledWith("s-1", "New Name");
        expect(result.current.editingSessionId).toBeNull();
    });

    test("cancelRename clears editing without persisting", () => {
        const { result } = renderHook(() => useSessionActions());
        act(() => result.current.beginRename(session));
        act(() => result.current.cancelRename());
        expect(result.current.editingSessionId).toBeNull();
        expect(updateSessionName).not.toHaveBeenCalled();
    });

    test("commitRename after cancelRename is a no-op (trailing onBlur on unmount)", async () => {
        const { result } = renderHook(() => useSessionActions());
        act(() => result.current.beginRename(session));
        act(() => result.current.setEditingSessionName("Discarded"));
        act(() => result.current.cancelRename());
        await act(async () => {
            await result.current.commitRename();
        });
        expect(updateSessionName).not.toHaveBeenCalled();
    });

    test("commitRename skips empty and unchanged names", async () => {
        const { result } = renderHook(() => useSessionActions());
        act(() => result.current.beginRename(session));
        act(() => result.current.setEditingSessionName("   "));
        await act(async () => {
            await result.current.commitRename();
        });
        act(() => result.current.beginRename(session));
        act(() => result.current.setEditingSessionName("Old Name"));
        await act(async () => {
            await result.current.commitRename();
        });
        expect(updateSessionName).not.toHaveBeenCalled();
    });

    test("renameWithAI generates a title from session messages on success", async () => {
        renameMutate.mockImplementation((_id, { onSuccess }) => onSuccess({ data: [{ messageBubbles: JSON.stringify([{ text: "user question" }, { text: "agent answer" }]) }] }));
        const { result } = renderHook(() => useSessionActions());
        await act(async () => {
            result.current.renameWithAI(session);
        });
        expect(generateTitle).toHaveBeenCalledWith("s-1", "user question", "agent answer", "Old Name", true);
        expect(result.current.regeneratingTitleForSession).toBeNull();
    });

    test("renameWithAI warns and resets when the session has no messages", async () => {
        renameMutate.mockImplementation((_id, { onSuccess }) => onSuccess({ data: [] }));
        const { result } = renderHook(() => useSessionActions());
        await act(async () => {
            result.current.renameWithAI(session);
        });
        expect(generateTitle).not.toHaveBeenCalled();
        expect(addNotification).toHaveBeenCalledWith("No messages found in this session", "warning");
        expect(result.current.regeneratingTitleForSession).toBeNull();
    });

    test("renameWithAI warns when tasks contain no text content", async () => {
        renameMutate.mockImplementation((_id, { onSuccess }) => onSuccess({ data: [{ messageBubbles: JSON.stringify([]) }] }));
        const { result } = renderHook(() => useSessionActions());
        await act(async () => {
            result.current.renameWithAI(session);
        });
        expect(generateTitle).not.toHaveBeenCalled();
        expect(addNotification).toHaveBeenCalledWith("No text content found in session", "warning");
        expect(result.current.regeneratingTitleForSession).toBeNull();
    });

    test("renameWithAI surfaces a warning and resets on error", async () => {
        vi.spyOn(console, "error").mockImplementation(() => {});
        renameMutate.mockImplementation((_id, { onError }) => onError(new Error("boom")));
        const { result } = renderHook(() => useSessionActions());
        await act(async () => {
            result.current.renameWithAI(session);
        });
        expect(addNotification).toHaveBeenCalledWith("Failed to regenerate title: boom", "warning");
        expect(result.current.regeneratingTitleForSession).toBeNull();
    });

    test("renameWithAI dedupes while a rename is already pending", () => {
        renameIsPending = true;
        const { result } = renderHook(() => useSessionActions());
        act(() => result.current.renameWithAI(session));
        expect(renameMutate).not.toHaveBeenCalled();
        expect(addNotification).toHaveBeenCalledWith("AI rename already in progress", "info");
    });
});
