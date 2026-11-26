// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { CanvasViewProvider } from "./canvasViewContext";
import { TestTargetButton } from "./TestTargetButton";

function renderInTestMode(ui: React.ReactNode, onSelectTestTarget = vi.fn()) {
    return {
        onSelectTestTarget,
        ...render(
            <CanvasViewProvider builderActiveTab="test" testTargetName={null} onSelectTestTarget={onSelectTestTarget} isApplyingChanges={false}>
                {ui}
            </CanvasViewProvider>
        ),
    };
}

describe("TestTargetButton", () => {
    test("is enabled in test mode and selects the target on click", () => {
        const { onSelectTestTarget } = renderInTestMode(<TestTargetButton componentName="ResearchReporter" />);

        const btn = screen.getByRole("button");
        expect(btn).not.toBeDisabled();

        fireEvent.click(btn);
        expect(onSelectTestTarget).toHaveBeenCalledWith("ResearchReporter");
    });

    test("renders nothing outside test mode", () => {
        const { container } = render(
            <CanvasViewProvider builderActiveTab="build" testTargetName={null} onSelectTestTarget={vi.fn()} isApplyingChanges={false}>
                <TestTargetButton componentName="ResearchReporter" />
            </CanvasViewProvider>
        );
        expect(container.querySelector("button")).toBeNull();
    });

    test("renders nothing when not eligible (e.g. connector cards)", () => {
        const { container } = renderInTestMode(<TestTargetButton componentName="MyConnector" eligible={false} />);
        expect(container.querySelector("button")).toBeNull();
    });
});
