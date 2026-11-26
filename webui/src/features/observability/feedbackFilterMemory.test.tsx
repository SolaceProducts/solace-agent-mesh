// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { FeedbackFilterMemoryProvider, type FeedbackTab, useFeedbackFilterMemory } from "./feedbackFilterMemory";

function Tab({ tab, seed }: Readonly<{ tab: FeedbackTab; seed: string }>) {
    const { read, remember } = useFeedbackFilterMemory();
    const [value, setValue] = useState(() => read<string>(tab) ?? seed);

    return (
        <button
            type="button"
            onClick={() => {
                const next = `${value}+`;
                setValue(next);
                remember(tab, next);
            }}
        >
            {`${tab}:${value}`}
        </button>
    );
}

describe("feedbackFilterMemory", () => {
    it("hands a tab back what it last remembered", async () => {
        const { rerender } = render(
            <FeedbackFilterMemoryProvider>
                <Tab tab="history" seed="a" />
            </FeedbackFilterMemoryProvider>
        );

        await userEvent.click(screen.getByRole("button"));
        expect(screen.getByRole("button")).toHaveTextContent("history:a+");

        rerender(
            <FeedbackFilterMemoryProvider>
                <span />
            </FeedbackFilterMemoryProvider>
        );
        rerender(
            <FeedbackFilterMemoryProvider>
                <Tab tab="history" seed="ignored" />
            </FeedbackFilterMemoryProvider>
        );

        expect(screen.getByRole("button")).toHaveTextContent("history:a+");
    });

    it("remembers a new value under the same key, overwriting what came before", async () => {
        render(
            <FeedbackFilterMemoryProvider>
                <Tab tab="history" seed="first" />
            </FeedbackFilterMemoryProvider>
        );

        await userEvent.click(screen.getByRole("button"));
        expect(screen.getByText("history:first+")).toBeInTheDocument();

        await userEvent.click(screen.getByRole("button"));
        expect(screen.getByText("history:first++")).toBeInTheDocument();
    });

    it("names the mistake when read outside the provider", () => {
        expect(() => render(<Tab tab="history" seed="a" />)).toThrow(/FeedbackFilterMemoryProvider/);
    });
});
