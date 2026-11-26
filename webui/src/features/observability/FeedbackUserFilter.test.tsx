// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import * as peopleService from "@/api/people/service";

import { FeedbackUserFilter } from "./FeedbackUserFilter";

const fakePeople = [
    { id: "1", workEmail: "olive@example.com", displayName: "Olive Anderson" },
    { id: "2", workEmail: "blake@example.com", displayName: "Blake Brennan" },
];

vi.spyOn(peopleService, "searchPeople").mockImplementation(async (query: string) => {
    const q = query.toLowerCase();
    const matches = fakePeople.filter(p => p.workEmail.toLowerCase().includes(q) || p.displayName.toLowerCase().includes(q));
    return { data: matches };
});

function renderFilter(over: Partial<React.ComponentProps<typeof FeedbackUserFilter>> = {}) {
    const props = { value: null, onChange: vi.fn(), ...over };
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
        <QueryClientProvider client={client}>
            <FeedbackUserFilter {...props} />
        </QueryClientProvider>
    );
    return props;
}

const trigger = () => screen.getByRole("button", { name: /^user$/i });

describe("FeedbackUserFilter", () => {
    it("reads as every submitter when no user is chosen", () => {
        renderFilter();

        expect(trigger()).toHaveTextContent(/all users/i);
    });

    it("names the chosen submitter on the trigger", () => {
        renderFilter({ value: "olive@example.com" });

        expect(trigger()).toHaveTextContent("olive@example.com");
    });

    it("does not steal focus before the menu is opened", () => {
        renderFilter();

        expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
        expect(document.activeElement).toBe(document.body);
    });

    it("reports the submitter picked from the directory", async () => {
        const props = renderFilter();

        await userEvent.click(trigger());
        await userEvent.type(screen.getByRole("textbox"), "olive");
        await userEvent.click(await screen.findByText("olive@example.com"));

        expect(props.onChange).toHaveBeenCalledWith("olive@example.com");
    });

    it("reports null when the filter is cleared", async () => {
        const props = renderFilter({ value: "olive@example.com" });

        await userEvent.click(trigger());
        await userEvent.click(screen.getByRole("button", { name: /all users/i }));

        expect(props.onChange).toHaveBeenCalledWith(null);
    });

    it("offers no clear action when nothing is filtered", async () => {
        renderFilter();

        await userEvent.click(trigger());

        expect(screen.queryByRole("button", { name: /all users/i })).not.toBeInTheDocument();
    });
});
