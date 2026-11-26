// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import * as peopleService from "@/api/people/service";

import { computeVisibleChipCount, UserMultiTypeahead } from "./UserMultiTypeahead";

const fakePeople = [
    { id: "1", workEmail: "olive@example.org", displayName: "Olive" },
    { id: "2", workEmail: "oliver@example.org", displayName: "Oliver" },
    { id: "3", workEmail: "olly@example.org", displayName: "Olly" },
];

function renderWithClient(ui: React.ReactNode) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

vi.spyOn(peopleService, "searchPeople").mockImplementation(async (query: string) => {
    const q = query.toLowerCase();
    const matches = fakePeople.filter(p => p.workEmail.toLowerCase().includes(q));
    return {
        data: matches,
        meta: { pagination: { pageNumber: 1, pageSize: matches.length, totalPages: 1, count: matches.length, nextPage: null } },
    };
});

describe("UserMultiTypeahead", () => {
    test("renders selected emails as chips", () => {
        renderWithClient(<UserMultiTypeahead id="t1" selectedEmails={["a@example.com", "b@example.com"]} onAdd={vi.fn()} onRemove={vi.fn()} excludeEmails={[]} />);
        expect(screen.getByText("a@example.com")).toBeInTheDocument();
        expect(screen.getByText("b@example.com")).toBeInTheDocument();
    });

    test("clicking the chip X calls onRemove with that email", async () => {
        const user = userEvent.setup();
        const onRemove = vi.fn();
        renderWithClient(<UserMultiTypeahead id="t1" selectedEmails={["a@example.com"]} onAdd={vi.fn()} onRemove={onRemove} excludeEmails={[]} />);
        await user.click(screen.getByLabelText(/remove a@example.com/i));
        expect(onRemove).toHaveBeenCalledWith("a@example.com");
    });

    test("typing shows matching people and clicking adds them as a chip", async () => {
        const user = userEvent.setup();
        const onAdd = vi.fn();
        renderWithClient(<UserMultiTypeahead id="t1" selectedEmails={[]} onAdd={onAdd} onRemove={vi.fn()} excludeEmails={[]} />);
        await user.type(screen.getByPlaceholderText(/search/i), "oli");
        const option = await screen.findByText("olive@example.org");
        await user.click(option);
        expect(onAdd).toHaveBeenCalledWith("olive@example.org");
    });

    test("free-entry: pressing Enter on a typed email that does not match any user still adds it", async () => {
        const user = userEvent.setup();
        const onAdd = vi.fn();
        renderWithClient(<UserMultiTypeahead id="t1" selectedEmails={[]} onAdd={onAdd} onRemove={vi.fn()} excludeEmails={[]} />);
        await user.type(screen.getByPlaceholderText(/search/i), "stranger@external.com{Enter}");
        expect(onAdd).toHaveBeenCalledWith("stranger@external.com");
    });

    test("free-entry: pressing Enter on a typed string that is NOT a valid email does nothing", async () => {
        const user = userEvent.setup();
        const onAdd = vi.fn();
        renderWithClient(<UserMultiTypeahead id="t1" selectedEmails={[]} onAdd={onAdd} onRemove={vi.fn()} excludeEmails={[]} />);
        await user.type(screen.getByPlaceholderText(/search/i), "not an email{Enter}");
        expect(onAdd).not.toHaveBeenCalled();
    });

    test("excludeEmails removes those people from the search results", async () => {
        const user = userEvent.setup();
        renderWithClient(<UserMultiTypeahead id="t1" selectedEmails={[]} onAdd={vi.fn()} onRemove={vi.fn()} excludeEmails={["olive@example.org"]} />);
        await user.type(screen.getByPlaceholderText(/search/i), "oli");
        await screen.findByText("oliver@example.org");
        expect(screen.queryByText("olive@example.org")).not.toBeInTheDocument();
    });

    test("pasting comma-separated emails adds each as a separate chip", async () => {
        const user = userEvent.setup();
        const onAdd = vi.fn();
        renderWithClient(<UserMultiTypeahead id="t1" selectedEmails={[]} onAdd={onAdd} onRemove={vi.fn()} excludeEmails={[]} />);
        const input = screen.getByPlaceholderText(/search/i);
        input.focus();
        await user.paste("a@example.com, b@example.com, c@example.com");
        expect(onAdd).toHaveBeenCalledWith("a@example.com");
        expect(onAdd).toHaveBeenCalledWith("b@example.com");
        expect(onAdd).toHaveBeenCalledWith("c@example.com");
    });

    test("pasting newline-separated emails adds each as a separate chip", async () => {
        const user = userEvent.setup();
        const onAdd = vi.fn();
        renderWithClient(<UserMultiTypeahead id="t1" selectedEmails={[]} onAdd={onAdd} onRemove={vi.fn()} excludeEmails={[]} />);
        const input = screen.getByPlaceholderText(/search/i);
        input.focus();
        await user.paste("a@example.com\nb@example.com;c@example.com");
        expect(onAdd).toHaveBeenCalledWith("a@example.com");
        expect(onAdd).toHaveBeenCalledWith("b@example.com");
        expect(onAdd).toHaveBeenCalledWith("c@example.com");
    });

    describe("computeVisibleChipCount", () => {
        test("returns full length when container has no width measurement (e.g. jsdom)", () => {
            expect(computeVisibleChipCount([60, 60, 60], 0)).toBe(3);
        });

        test("returns 1 when no chips fit so a single chip always stays visible", () => {
            expect(computeVisibleChipCount([200, 200, 200], 250)).toBe(1);
        });

        test("returns the count that fits before the input and pill reserves bite", () => {
            const widths = [60, 60, 60, 60, 60, 60, 60, 60];
            const count = computeVisibleChipCount(widths, 600);
            expect(count).toBeGreaterThan(0);
            expect(count).toBeLessThan(widths.length);
        });

        test("returns full length when every chip fits and pill is not needed", () => {
            expect(computeVisibleChipCount([50], 400)).toBe(1);
        });
    });

    test("pasting a single email without delimiters falls through to normal typing", async () => {
        const user = userEvent.setup();
        const onAdd = vi.fn();
        renderWithClient(<UserMultiTypeahead id="t1" selectedEmails={[]} onAdd={onAdd} onRemove={vi.fn()} excludeEmails={[]} />);
        const input = screen.getByPlaceholderText(/search/i);
        input.focus();
        await user.paste("a@example.com");
        expect(onAdd).not.toHaveBeenCalled();
        expect((input as HTMLInputElement).value).toBe("a@example.com");
    });
});
