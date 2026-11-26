// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { TooltipProvider } from "@/ui/tooltip";

import { ComponentChip } from "./ComponentChip";

const renderChip = (props: React.ComponentProps<typeof ComponentChip>) =>
    render(
        <TooltipProvider>
            <ComponentChip {...props} />
        </TooltipProvider>
    );

describe("ComponentChip", () => {
    test("renders the component name and capitalized type", () => {
        renderChip({ component: { name: "sample-geography", type: "skill", origin: "platform" } });
        expect(screen.getByText("sample-geography")).toBeInTheDocument();
        expect(screen.getByText("Skill")).toBeInTheDocument();
    });

    test("labels a gateway component 'Entrypoint', never 'Gateway'", () => {
        renderChip({ component: { name: "order_events_entrypoint", type: "gateway", origin: "session" } });
        expect(screen.getByText("Entrypoint")).toBeInTheDocument();
        expect(screen.queryByText("Gateway")).toBeNull();
    });

    test("shows the 'new' badge for a session-origin component, not for a platform one", () => {
        const { rerender } = renderChip({ component: { name: "A", type: "agent", origin: "session" } });
        expect(screen.getByText("new")).toBeInTheDocument();
        rerender(
            <TooltipProvider>
                <ComponentChip component={{ name: "A", type: "agent", origin: "platform" }} />
            </TooltipProvider>
        );
        expect(screen.queryByText("new")).toBeNull();
    });

    test("calls onRemove when the remove button is clicked", () => {
        const onRemove = vi.fn();
        renderChip({ component: { name: "A", type: "agent", origin: "session" }, onRemove });
        fireEvent.click(screen.getByRole("button", { name: /remove/i }));
        expect(onRemove).toHaveBeenCalledTimes(1);
    });

    test("renders the connector service icon for a known icon key", () => {
        renderChip({ component: { name: "Slack", type: "connector", icon: "slack", origin: "session" } });
        expect(screen.getByRole("img", { name: "slack" })).toBeInTheDocument();
    });

    test("falls back to the type icon for a connector with icon 'other'", () => {
        const { container } = renderChip({ component: { name: "X", type: "connector", icon: "other", origin: "session" } });
        expect(container.querySelector("img")).toBeNull();
    });

    test("renders no remove button when onRemove is omitted", () => {
        const { container } = renderChip({ component: { name: "A", type: "agent" } });
        expect(container.querySelector("button")).toBeNull();
    });
});
