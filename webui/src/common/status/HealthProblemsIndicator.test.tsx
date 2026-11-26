// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { RuntimeProblem } from "@/types";

import { HealthProblemsIndicator } from "./HealthProblemsIndicator";

const problem = (overrides: Partial<RuntimeProblem> = {}): RuntimeProblem => ({
    id: "queue:x",
    severity: "degraded",
    detail: "temporary receive error; retrying automatically",
    since: "",
    ...overrides,
});

describe("HealthProblemsIndicator", () => {
    test("renders nothing when problems is absent or empty", () => {
        const { container: withoutProp } = render(<HealthProblemsIndicator />);
        expect(withoutProp).toBeEmptyDOMElement();

        const { container: withEmpty } = render(<HealthProblemsIndicator problems={[]} />);
        expect(withEmpty).toBeEmptyDOMElement();
    });

    test("renders the issue count", () => {
        render(<HealthProblemsIndicator problems={[problem()]} />);
        expect(screen.getByText("1 issue")).toBeInTheDocument();

        render(<HealthProblemsIndicator problems={[problem(), problem({ id: "queue:y" })]} testIdPrefix="multi" />);
        expect(screen.getByText("2 issues")).toBeInTheDocument();
    });

    test("the trigger is keyboard-focusable and exposed as a status region", () => {
        render(<HealthProblemsIndicator problems={[problem()]} testIdPrefix="agentHealth" />);
        const trigger = screen.getByTestId("agentHealth-indicator");
        expect(trigger).toHaveAttribute("tabIndex", "0");
        expect(trigger.tagName).toBe("OUTPUT");
        expect(screen.getByRole("status")).toBe(trigger);
    });

    test("a click on the trigger does not bubble to an ancestor's click handler", () => {
        const onAncestorClick = vi.fn();
        render(
            <div onClick={onAncestorClick}>
                <HealthProblemsIndicator problems={[problem()]} testIdPrefix="agentHealth" />
            </div>
        );

        fireEvent.click(screen.getByTestId("agentHealth-indicator"));

        expect(onAncestorClick).not.toHaveBeenCalled();
    });
});
