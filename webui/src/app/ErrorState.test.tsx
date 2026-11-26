// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import ErrorState from "./ErrorState";

describe("ErrorState", () => {
    it("renders the message and runs a button action", () => {
        const onClick = vi.fn();
        render(<ErrorState message="boom happened" actions={[{ kind: "button", label: "Retry", onClick }]} />);

        expect(screen.getByText("boom happened")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Retry" }));
        expect(onClick).toHaveBeenCalledTimes(1);
    });

    it("renders a link action with an href", () => {
        render(<ErrorState message="m" actions={[{ kind: "link", label: "Back to chat", href: "/#/chat" }]} />);

        expect(screen.getByRole("link", { name: "Back to chat" })).toHaveAttribute("href", "/#/chat");
    });

    it("does not expose any error detail affordance", () => {
        render(<ErrorState message="m" actions={[]} />);

        expect(screen.queryByText(/error details/i)).not.toBeInTheDocument();
    });
});
