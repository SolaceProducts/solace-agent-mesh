// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { type MouseEvent } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { MultiSelect } from "./MultiSelect";

const EVALUATORS = ["Summary", "LLM Judge", "Test Judge"];

async function openAndClick(option: string) {
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { expanded: false }));
    await user.click(screen.getByRole("button", { name: option }));
}

describe("MultiSelect — emptyMeansAll toggle (filter semantics)", () => {
    test("clicking an option from the empty 'all' state narrows to all-except-that-option", async () => {
        const onChange = vi.fn();
        render(<MultiSelect summaryMode emptyMeansAll placeholder="All Evaluators" options={EVALUATORS} value={[]} onChange={onChange} />);
        await openAndClick("LLM Judge");
        expect(onChange).toHaveBeenCalledWith(["Summary", "Test Judge"]);
    });

    test("clicking a selected option removes just that option", async () => {
        const onChange = vi.fn();
        render(<MultiSelect summaryMode emptyMeansAll placeholder="All Evaluators" options={EVALUATORS} value={["Summary", "LLM Judge"]} onChange={onChange} />);
        await openAndClick("Summary");
        expect(onChange).toHaveBeenCalledWith(["LLM Judge"]);
    });

    test("without emptyMeansAll, clicking from an empty selection selects just that option", async () => {
        const onChange = vi.fn();
        render(<MultiSelect placeholder="Select..." options={EVALUATORS} value={[]} onChange={onChange} />);
        await openAndClick("LLM Judge");
        expect(onChange).toHaveBeenCalledWith(["LLM Judge"]);
    });
});

describe("MultiSelect — renderChip", () => {
    const renderChip = (option: string, remove: (e?: MouseEvent) => void) => (
        <span data-testid={`chip-${option}`}>
            {option}
            <button type="button" aria-label={`Remove ${option}`} onClick={remove}>
                x
            </button>
        </span>
    );

    test("renders custom chips and remove deselects without opening the dropdown", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        render(<MultiSelect options={EVALUATORS} value={["Summary", "LLM Judge"]} onChange={onChange} renderChip={renderChip} />);

        expect(screen.getByTestId("chip-Summary")).toBeInTheDocument();
        expect(screen.getByTestId("chip-LLM Judge")).toBeInTheDocument();

        await user.click(screen.getByRole("button", { name: "Remove Summary" }));

        expect(onChange).toHaveBeenCalledWith(["LLM Judge"]);
        expect(screen.queryByRole("button", { expanded: true })).not.toBeInTheDocument();
    });
});
