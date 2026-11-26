// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test } from "vitest";

import { defineStepper } from "./stepper";

const { Stepper } = defineStepper({ id: "config", title: "Config" }, { id: "tools", title: "Tools" }, { id: "review", title: "Review" });

type StepId = "config" | "tools" | "review";

function Wizard({ defaultStep, onStepClick }: Readonly<{ defaultStep?: StepId; onStepClick?: (id: StepId) => void }>) {
    return (
        <Stepper.Provider defaultStep={defaultStep}>
            {({ methods }) => (
                <>
                    <Stepper.Navigation>
                        {(["config", "tools", "review"] as const).map(id => (
                            <Stepper.Step key={id} of={id} onClick={() => onStepClick?.(id)}>
                                <Stepper.Title>{id}</Stepper.Title>
                            </Stepper.Step>
                        ))}
                    </Stepper.Navigation>
                    <Stepper.Panel>current:{methods.current.id}</Stepper.Panel>
                    <button type="button" onClick={() => void methods.goTo("review")}>
                        jump to review
                    </button>
                    <button type="button" onClick={() => void methods.goTo("config")}>
                        jump to config
                    </button>
                    <button type="button" onClick={() => void methods.next()}>
                        next
                    </button>
                    <button type="button" onClick={() => void methods.prev()}>
                        prev
                    </button>
                </>
            )}
        </Stepper.Provider>
    );
}

function stepStates() {
    return screen.getAllByRole("tab").map(tab => tab.parentElement?.getAttribute("data-state"));
}

describe("defineStepper Provider", () => {
    test("the container render prop and the Step components share one stepper instance", async () => {
        render(<Wizard />);

        expect(screen.getByText("current:config")).toBeInTheDocument();
        expect(stepStates()).toEqual(["active", "inactive", "inactive"]);

        await userEvent.click(screen.getByRole("button", { name: "next" }));

        expect(screen.getByText("current:tools")).toBeInTheDocument();
        expect(stepStates()).toEqual(["completed", "active", "inactive"]);
        expect(screen.getAllByRole("tab")[1]).toHaveAttribute("aria-selected", "true");
    });

    test("starts at defaultStep", () => {
        render(<Wizard defaultStep="review" />);

        expect(screen.getByText("current:review")).toBeInTheDocument();
        expect(stepStates()).toEqual(["completed", "completed", "active"]);
    });

    test("goTo moves freely forward and backward, as the wizards rely on", async () => {
        render(<Wizard />);

        await userEvent.click(screen.getByRole("button", { name: "jump to review" }));
        expect(screen.getByText("current:review")).toBeInTheDocument();

        await userEvent.click(screen.getByRole("button", { name: "jump to config" }));
        expect(screen.getByText("current:config")).toBeInTheDocument();
        expect(stepStates()).toEqual(["active", "inactive", "inactive"]);
    });

    test("prev and next stop at the boundaries without throwing", async () => {
        render(<Wizard />);

        await userEvent.click(screen.getByRole("button", { name: "prev" }));
        expect(screen.getByText("current:config")).toBeInTheDocument();

        await userEvent.click(screen.getByRole("button", { name: "jump to review" }));
        await userEvent.click(screen.getByRole("button", { name: "next" }));
        expect(screen.getByText("current:review")).toBeInTheDocument();
    });

    test("step indicator clicks reach the caller's handler", async () => {
        const clicked: StepId[] = [];
        render(<Wizard onStepClick={id => clicked.push(id)} />);

        await userEvent.click(screen.getAllByRole("tab")[0]);

        expect(clicked).toEqual(["config"]);
    });
});
