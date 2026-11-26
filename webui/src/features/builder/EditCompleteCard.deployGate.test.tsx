// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { editCompleteCtaMode } from "./builderUtils";
import { EditCompleteCard } from "./EditCompleteCard";

const applyComplete = {
    summary: "Renamed the annotated image output field to match what the agent returns.",
    changesApplied: [],
};

describe("EditCompleteCard deploy gate", () => {
    test("offers Build & Activate, not Test mode, while a deploy is pending", () => {
        const onBuildActivate = vi.fn();
        const onSwitchToTest = vi.fn();
        render(<EditCompleteCard applyComplete={applyComplete} onSwitchToTest={onSwitchToTest} ctaMode="deploy" onBuildActivate={onBuildActivate} />);

        expect(screen.queryByRole("button", { name: /switch to test mode/i })).toBeNull();
        screen.getByRole("button", { name: /build & activate/i }).click();
        expect(onBuildActivate).toHaveBeenCalledTimes(1);
        expect(onSwitchToTest).not.toHaveBeenCalled();
    });

    test("has no way to remove anything: its one action is the deploy, and the card takes no removal", () => {
        const onBuildActivate = vi.fn();
        render(<EditCompleteCard applyComplete={applyComplete} onSwitchToTest={vi.fn()} ctaMode="deploy" onBuildActivate={onBuildActivate} />);

        expect(screen.queryByRole("button", { name: /remove|delete|undeploy/i })).toBeNull();
        expect(screen.getAllByRole("button").map(button => button.textContent)).toEqual(["Build & Activate"]);
    });

    test("says plainly that the changes are not live yet", () => {
        render(<EditCompleteCard applyComplete={applyComplete} onSwitchToTest={vi.fn()} ctaMode="deploy" onBuildActivate={vi.fn()} />);
        expect(screen.getByText(/not live yet/i)).toBeTruthy();
    });

    test("offers Test mode once nothing is pending", () => {
        const onSwitchToTest = vi.fn();
        render(<EditCompleteCard applyComplete={applyComplete} onSwitchToTest={onSwitchToTest} ctaMode="test" onBuildActivate={vi.fn()} />);

        expect(screen.queryByRole("button", { name: /build & activate/i })).toBeNull();
        screen.getByRole("button", { name: /switch to test mode/i }).click();
        expect(onSwitchToTest).toHaveBeenCalledTimes(1);
    });

    test("falls back to Test mode when the host supplied no deploy handler", () => {
        render(<EditCompleteCard applyComplete={applyComplete} onSwitchToTest={vi.fn()} ctaMode="deploy" />);
        expect(screen.getByRole("button", { name: /switch to test mode/i })).toBeTruthy();
    });
});

describe("editCompleteCtaMode", () => {
    test("a pending deploy selects the deploy CTA", () => {
        expect(editCompleteCtaMode({ pendingDeploy: true })).toBe("deploy");
    });

    test("nothing pending selects the test CTA", () => {
        expect(editCompleteCtaMode({ pendingDeploy: false })).toBe("test");
    });

    test("a refused check shows its verdict above a disabled Build & Activate, never Test mode", () => {
        const onCheckAgain = vi.fn();
        render(
            <EditCompleteCard
                applyComplete={applyComplete}
                onSwitchToTest={vi.fn()}
                ctaMode="deploy"
                onBuildActivate={vi.fn()}
                canBuildActivate={false}
                platformCheck={{ check: { kind: "refused", problems: [{ kind: "agent", name: "TestAgent", message: "model alias is unknown" }], forbidden: [] }, canAskBuilder: true }}
                onCheckAgain={onCheckAgain}
                onAskBuilder={vi.fn()}
            />
        );

        expect(screen.getByText("agent TestAgent: model alias is unknown")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Build & Activate" })).toBeDisabled();
        expect(screen.queryByRole("button", { name: /switch to test mode/i })).toBeNull();
        screen.getByRole("button", { name: "Check again" }).click();
        expect(onCheckAgain).toHaveBeenCalledTimes(1);
    });

    test("a passed check reads Ready above an enabled Build & Activate", () => {
        render(<EditCompleteCard applyComplete={applyComplete} onSwitchToTest={vi.fn()} ctaMode="deploy" onBuildActivate={vi.fn()} canBuildActivate platformCheck={{ check: { kind: "passed", warnings: [] }, canAskBuilder: false }} />);

        expect(screen.getByText("Ready")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Build & Activate" })).toBeEnabled();
    });
});
