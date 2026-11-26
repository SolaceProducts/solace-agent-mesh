// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { LiveActivityLine } from "./LiveActivityLine";

describe("LiveActivityLine", () => {
    test("shows the step behind a working dot and an expand affordance", () => {
        render(<LiveActivityLine text="Loading agent schema skill..." onExpand={vi.fn()} />);

        screen.getByLabelText("Working");
        const toggle = screen.getByTitle("Show all activity steps");
        expect(toggle).toHaveAttribute("aria-expanded", "false");
        expect(screen.getByText("Loading agent schema skill...")).toHaveClass("thinking-shimmer");
    });

    test("reveals the expand chevron on keyboard focus, not hover alone", () => {
        const { container } = render(<LiveActivityLine text="Working on it..." onExpand={vi.fn()} />);

        expect(container.querySelector(".lucide-chevron-right")).toHaveClass("group-focus-within:opacity-100");
    });

    test("keeps the full step text in the label's title", () => {
        const long = "Receiving artifact `rc_edit_probe_agent.yaml`: RcEditProbe pasta Q&A agent config";
        render(<LiveActivityLine text={long} onExpand={vi.fn()} />);

        expect(screen.getByTitle(long)).toHaveTextContent(long);
    });

    test("clicking the line expands it", () => {
        const onExpand = vi.fn();
        render(<LiveActivityLine text="Working on it..." onExpand={onExpand} />);

        fireEvent.click(screen.getByTitle("Show all activity steps"));
        expect(onExpand).toHaveBeenCalledTimes(1);
    });

    test("renders trailing chrome inside the hover group", () => {
        const { container } = render(
            <LiveActivityLine text="Working on it..." onExpand={vi.fn()}>
                <span>Waiting for input</span>
            </LiveActivityLine>
        );

        const group = container.querySelector(".group");
        expect(group).not.toBeNull();
        expect(group).toContainElement(screen.getByText("Waiting for input"));
    });
});
