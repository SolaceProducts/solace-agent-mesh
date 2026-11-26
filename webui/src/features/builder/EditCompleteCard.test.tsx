// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { type ApplyComplete, EditCompleteCard } from "./EditCompleteCard";

function complete(count: number): ApplyComplete {
    return {
        summary: "Changes are done.",
        changesApplied: Array.from({ length: count }, (_, i) => ({
            component: `Component${i}`,
            area: "tools" as const,
            change: `Change number ${i}`,
        })),
    };
}

describe("EditCompleteCard", () => {
    test("renders every change row (the list scrolls, it does not truncate the set)", () => {
        render(<EditCompleteCard applyComplete={complete(20)} onSwitchToTest={vi.fn()} />);
        expect(screen.getAllByRole("listitem")).toHaveLength(20);
        expect(screen.getByText("Change number 0")).toBeTruthy();
        expect(screen.getByText("Change number 19")).toBeTruthy();
    });

    test("caps the change list height so a long set can't fill the whole conversation", () => {
        const { container } = render(<EditCompleteCard applyComplete={complete(20)} onSwitchToTest={vi.fn()} />);
        const list = container.querySelector("ul");
        expect(list).not.toBeNull();
        expect(list!.className).toContain("max-h-72");
        expect(list!.className).toContain("overflow-y-auto");
        expect(list!.className).toContain("overscroll-contain");
    });

    test("still shows the summary and the Switch to Test Mode CTA (which fires onSwitchToTest)", () => {
        const onSwitch = vi.fn();
        render(<EditCompleteCard applyComplete={complete(3)} onSwitchToTest={onSwitch} />);
        expect(screen.getByText("Changes are done.")).toBeTruthy();
        const cta = screen.getByRole("button", { name: /Switch to Test Mode/i });
        fireEvent.click(cta);
        expect(onSwitch).toHaveBeenCalledTimes(1);
    });

    test("renders the area badge label for each change (maps the enum to a human label)", () => {
        render(<EditCompleteCard applyComplete={complete(3)} onSwitchToTest={vi.fn()} />);
        expect(screen.getAllByText("Tools")).toHaveLength(3);
    });

    test("renders no list when there are no changes (summary-only)", () => {
        const { container } = render(<EditCompleteCard applyComplete={complete(0)} onSwitchToTest={vi.fn()} />);
        expect(container.querySelector("ul")).toBeNull();
        expect(screen.getByText("Changes are done.")).toBeTruthy();
    });
});
