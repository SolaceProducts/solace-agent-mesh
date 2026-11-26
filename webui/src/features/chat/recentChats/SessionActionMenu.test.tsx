// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { Session } from "@/types";

vi.mock("@/providers/contexts", () => ({
    useUIModeContext: () => ({ allowedSessionActions: ["goToProject", "rename", "renameWithAI", "move", "share", "delete"] }),
}));

import { SessionActionMenu } from "./SessionActionMenu";

const session = { id: "s-1", name: "Chat", createdAt: "", updatedAt: "", project: { id: "p-1", name: "Proj" } } as Session;

function renderMenu(overrides: Partial<Record<string, (s: Session) => void>> = {}) {
    const handlers = {
        onRename: vi.fn(),
        onRenameWithAI: vi.fn(),
        onMove: vi.fn(),
        onDelete: vi.fn(),
        onGoToProject: vi.fn(),
        onShare: vi.fn(),
        ...overrides,
    };
    render(<SessionActionMenu session={session} {...handlers} />);
    return handlers;
}

describe("SessionActionMenu", () => {
    test("clicking the trigger opens the menu with the allowed items and stops row propagation", async () => {
        const rowClick = vi.fn();
        const handlers = {
            onRename: vi.fn(),
            onRenameWithAI: vi.fn(),
            onMove: vi.fn(),
            onDelete: vi.fn(),
            onGoToProject: vi.fn(),
            onShare: vi.fn(),
        };
        render(
            <div onClick={rowClick}>
                <SessionActionMenu session={session} {...handlers} />
            </div>
        );
        await userEvent.click(screen.getByRole("button"));
        expect(rowClick).not.toHaveBeenCalled();
        expect(await screen.findByRole("menuitem", { name: /rename with ai/i })).toBeInTheDocument();
        for (const name of [/go to project/i, /^rename$/i, /move to project/i, /^share$/i, /^delete$/i]) {
            expect(screen.getByRole("menuitem", { name })).toBeInTheDocument();
        }
    });

    test("selecting an item invokes its handler with the session", async () => {
        const handlers = renderMenu();
        await userEvent.click(screen.getByRole("button"));
        await userEvent.click(await screen.findByRole("menuitem", { name: /^delete$/i }));
        expect(handlers.onDelete).toHaveBeenCalledWith(session);
    });

    test("Enter/Space on the trigger does not bubble to a clickable row", () => {
        const rowKeyDown = vi.fn();
        const handlers = {
            onRename: vi.fn(),
            onRenameWithAI: vi.fn(),
            onMove: vi.fn(),
            onDelete: vi.fn(),
            onGoToProject: vi.fn(),
            onShare: vi.fn(),
        };
        render(
            <div onKeyDown={rowKeyDown}>
                <SessionActionMenu session={session} {...handlers} />
            </div>
        );
        const trigger = screen.getByRole("button");
        fireEvent.keyDown(trigger, { key: "Enter" });
        fireEvent.keyDown(trigger, { key: " " });
        expect(rowKeyDown).not.toHaveBeenCalled();
        fireEvent.keyDown(trigger, { key: "a" });
        expect(rowKeyDown).toHaveBeenCalledTimes(1);
    });
});
