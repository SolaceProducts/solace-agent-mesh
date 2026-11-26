// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { MentionsCommand } from "@/features/chat/MentionsCommand";

import type { Person } from "@/types";

vi.mock("@/utils/recentMentions", () => ({
    getRecentMentions: vi.fn().mockReturnValue([]),
}));

const mockPerson: Person = {
    id: "person-1",
    displayName: "Alice Smith",
    workEmail: "alice@example.com",
    jobTitle: "Engineer",
};

function stubSelection() {
    const mockRange = { getBoundingClientRect: () => ({ top: 100, bottom: 120, left: 50, right: 200, width: 150, height: 20 }) };
    vi.stubGlobal("getSelection", () => ({ rangeCount: 1, getRangeAt: () => mockRange }));
    vi.stubGlobal("innerWidth", 1200);
    vi.stubGlobal("innerHeight", 800);
}

const mockFetch = vi.fn();
function makeTextBodyResponse(data: unknown) {
    return { ok: true, status: 200, text: async () => JSON.stringify(data) };
}
beforeEach(() => {
    mockFetch.mockResolvedValue(makeTextBodyResponse({ data: [] }));
    vi.stubGlobal("fetch", mockFetch);
});

afterEach(() => {
    vi.unstubAllGlobals();
});

function renderMentions(props: Partial<React.ComponentProps<typeof MentionsCommand>> = {}) {
    stubSelection();
    const Wrapper = () => {
        const textAreaRef = React.useRef<HTMLDivElement>(null);
        return (
            <>
                <div ref={textAreaRef} />
                <MentionsCommand onClose={vi.fn()} onPersonSelect={vi.fn()} searchQuery="" isOpen={true} {...props} textAreaRef={textAreaRef} />
            </>
        );
    };
    return render(
        <MemoryRouter>
            <Wrapper />
        </MemoryRouter>
    );
}

describe("MentionsCommand", () => {
    test("renders nothing when isOpen is false", () => {
        const { container } = renderMentions({ isOpen: false });
        expect(screen.queryByText("Recent mentions")).not.toBeInTheDocument();
        expect(container).toBeInTheDocument();
    });

    test("shows 'Recent mentions' header when searchQuery is empty", async () => {
        renderMentions({ searchQuery: "" });
        await screen.findByText("Recent mentions");
    });

    test("shows search header when searchQuery has content", async () => {
        renderMentions({ searchQuery: "alice" });
        await screen.findByText(/Searching for/);
        expect(screen.getByText("alice")).toBeInTheDocument();
    });

    test("shows 'No recent mentions' when recent list is empty and searchQuery is empty", async () => {
        renderMentions({ searchQuery: "" });
        await screen.findByText(/No recent mentions/);
    });

    test("pressing Escape calls onClose", async () => {
        const onClose = vi.fn();
        renderMentions({ onClose });
        await screen.findByText("Recent mentions");
        fireEvent.keyDown(window, { key: "Escape" });
        expect(onClose).toHaveBeenCalled();
    });

    test("backdrop click calls onClose", async () => {
        const onClose = vi.fn();
        renderMentions({ onClose });
        await screen.findByText("Recent mentions");
        const backdrop = document.querySelector('[role="presentation"]')!;
        fireEvent.click(backdrop);
        expect(onClose).toHaveBeenCalled();
    });

    test("renders search results when people are returned from API", async () => {
        mockFetch.mockResolvedValue(makeTextBodyResponse({ data: [mockPerson] }));
        renderMentions({ searchQuery: "alice" });
        await screen.findByText("Alice Smith");
        expect(screen.getByText("alice@example.com")).toBeInTheDocument();
    });

    test("clicking a person result calls onPersonSelect and onClose", async () => {
        mockFetch.mockResolvedValue(makeTextBodyResponse({ data: [mockPerson] }));
        const onPersonSelect = vi.fn();
        const onClose = vi.fn();
        renderMentions({ searchQuery: "alice", onPersonSelect, onClose });

        const nameEl = await screen.findByText("Alice Smith");
        fireEvent.click(nameEl.closest("button")!);

        expect(onPersonSelect).toHaveBeenCalledWith(mockPerson);
        expect(onClose).toHaveBeenCalled();
    });

    test("Enter key selects the first person from search results", async () => {
        mockFetch.mockResolvedValue(makeTextBodyResponse({ data: [mockPerson] }));
        const onPersonSelect = vi.fn();
        renderMentions({ searchQuery: "alice", onPersonSelect });

        await screen.findByText("Alice Smith");
        await act(async () => {});
        fireEvent.keyDown(window, { key: "Enter" });
        await waitFor(() => expect(onPersonSelect).toHaveBeenCalledWith(mockPerson));
    });

    test("hovering a search result does not throw (mouseEnter handler)", async () => {
        mockFetch.mockResolvedValue(makeTextBodyResponse({ data: [mockPerson] }));
        renderMentions({ searchQuery: "alice" });
        const nameEl = await screen.findByText("Alice Smith");
        const btn = nameEl.closest("button")!;
        fireEvent.mouseEnter(btn);
        expect(btn).toBeInTheDocument();
    });

    test("renders search results for a multi-word query and URL-encodes the space", async () => {
        const multiWordPerson: Person = {
            id: "maria.de.souza@example.com",
            displayName: "Maria De Souza",
            workEmail: "maria.de.souza@example.com",
            jobTitle: "Engineer",
        };
        mockFetch.mockClear();
        mockFetch.mockResolvedValue(makeTextBodyResponse({ data: [multiWordPerson] }));
        renderMentions({ searchQuery: "maria de" });

        await screen.findByText("Maria De Souza");

        const calledUrl = mockFetch.mock.calls.find(call => typeof call[0] === "string" && call[0].includes("/api/v1/people/search"))?.[0] as string | undefined;
        expect(calledUrl).toBeDefined();
        expect(calledUrl).toContain("q=maria%20de");
    });

    test("shows the standard circular Spinner while searching", async () => {
        mockFetch.mockReturnValue(new Promise(() => {}));
        const { container } = renderMentions({ searchQuery: "al" });
        await waitFor(() => expect(container.querySelector("svg.animate-spin")).toBeInTheDocument());
    });
});
