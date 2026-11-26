// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { BuilderTabToggle } from "./BuilderTabToggle";

describe("BuilderTabToggle", () => {
    function setup(overrides: Partial<Parameters<typeof BuilderTabToggle>[0]> = {}) {
        const props = {
            builderActiveTab: "build" as const,
            canTest: true,
            hasTestTarget: true,
            isCompact: false,
            onRequestTest: vi.fn(),
            onRequestBuild: vi.fn(),
            ...overrides,
        };
        render(<BuilderTabToggle {...props} />);
        return props;
    }

    test("clicking Test from Build requests the test switch", async () => {
        const user = userEvent.setup();
        const props = setup({ builderActiveTab: "build" });
        await user.click(screen.getByRole("tab", { name: /test/i }));
        expect(props.onRequestTest).toHaveBeenCalled();
        expect(props.onRequestBuild).not.toHaveBeenCalled();
    });

    test("clicking Build from Test requests the build switch", async () => {
        const user = userEvent.setup();
        const props = setup({ builderActiveTab: "test" });
        await user.click(screen.getByRole("tab", { name: /build/i }));
        expect(props.onRequestBuild).toHaveBeenCalled();
        expect(props.onRequestTest).not.toHaveBeenCalled();
    });

    test("Test tab is disabled and does not request a switch when canTest is false", async () => {
        const user = userEvent.setup();
        const props = setup({ canTest: false });
        const testTab = screen.getByRole("tab", { name: /test/i });
        expect(testTab).toBeDisabled();
        await user.click(testTab);
        expect(props.onRequestTest).not.toHaveBeenCalled();
        const span = testTab.closest("span");
        expect(span).toHaveAttribute("tabIndex", "0");
        expect(span?.className).toContain("cursor-not-allowed");
    });

    test("Test tab is enabled (no disabled affordance) when canTest is true", () => {
        setup({ canTest: true });
        const testTab = screen.getByRole("tab", { name: /test/i });
        expect(testTab).not.toBeDisabled();
        const span = testTab.closest("span");
        expect(span).not.toHaveAttribute("tabIndex", "0");
        expect(span?.className).not.toContain("cursor-not-allowed");
    });
});
