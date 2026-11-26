// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { PinButton } from "./PinButton";

describe("PinButton", () => {
    test("renders with unpinned state (outline star)", () => {
        render(<PinButton isPinned={false} onClick={() => {}} />);

        const button = screen.getByRole("button", { name: "Add to favorites" });
        expect(button).toBeInTheDocument();

        const star = button.querySelector(".lucide-star");
        expect(star).toBeInTheDocument();
        expect(star).toHaveAttribute("fill", "none");
    });

    test("renders with pinned state (filled star)", () => {
        render(<PinButton isPinned={true} onClick={() => {}} />);

        const button = screen.getByRole("button", { name: "Remove from favorites" });
        expect(button).toBeInTheDocument();

        const star = button.querySelector(".lucide-star");
        expect(star).toBeInTheDocument();
        expect(star).toHaveAttribute("fill", "currentColor");
    });

    test("calls onClick when clicked", async () => {
        const user = userEvent.setup();
        const handleClick = vi.fn();

        render(<PinButton isPinned={false} onClick={handleClick} />);

        const button = screen.getByRole("button", { name: "Add to favorites" });
        await user.click(button);

        expect(handleClick).toHaveBeenCalledTimes(1);
    });

    test("does not call onClick when disabled", async () => {
        const user = userEvent.setup();
        const handleClick = vi.fn();

        render(<PinButton isPinned={false} onClick={handleClick} disabled={true} />);

        const button = screen.getByRole("button", { name: "Add to favorites" });
        expect(button).toBeDisabled();

        await user.click(button);
        expect(handleClick).not.toHaveBeenCalled();
    });
});
