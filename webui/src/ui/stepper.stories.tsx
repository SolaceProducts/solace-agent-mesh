// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { Button } from "./button";
import { defineStepper, type StepperLabelOrientation, type StepperVariant } from "./stepper";

const { Stepper } = defineStepper({ id: "step1", title: "Step 1", description: "First step" }, { id: "step2", title: "Step 2", description: "Second step" }, { id: "step3", title: "Step 3", description: "Third step" });

const StepperExample = ({ variant = "horizontal", labelOrientation = "horizontal" }: { variant?: StepperVariant; labelOrientation?: StepperLabelOrientation }) => {
    return (
        <Stepper.Provider variant={variant} labelOrientation={labelOrientation}>
            {({ methods }) => (
                <>
                    <Stepper.Navigation>
                        <Stepper.Step of="step1">
                            <Stepper.Title>Account</Stepper.Title>
                            <Stepper.Description>Create your account</Stepper.Description>
                        </Stepper.Step>
                        <Stepper.Step of="step2">
                            <Stepper.Title>Profile</Stepper.Title>
                            <Stepper.Description>Set up your profile</Stepper.Description>
                        </Stepper.Step>
                        <Stepper.Step of="step3">
                            <Stepper.Title>Complete</Stepper.Title>
                            <Stepper.Description>Review and finish</Stepper.Description>
                        </Stepper.Step>
                    </Stepper.Navigation>

                    <Stepper.Panel>
                        <div className="my-4 rounded-lg border border-(--secondary-w20) p-4">
                            <h3>Step Content: {methods.current.title}</h3>
                            <p>{methods.current.description}</p>
                        </div>
                    </Stepper.Panel>

                    <Stepper.Controls className="mt-4">
                        <Button onClick={() => methods.prev()} disabled={methods.current.id === "step1"}>
                            Previous
                        </Button>
                        <Button onClick={() => methods.next()} disabled={methods.current.id === "step3"}>
                            Next
                        </Button>
                    </Stepper.Controls>
                </>
            )}
        </Stepper.Provider>
    );
};

const meta = {
    title: "UI/Stepper",
    component: StepperExample,
    parameters: {
        docs: {
            description: {
                component: "A stepper component for multi-step workflows with support for horizontal, vertical, and circle variants.",
            },
        },
    },
    decorators: [
        Story => (
            <div className="flex p-8">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof StepperExample>;

export default meta;

type Story = StoryObj<typeof meta>;

export const HorizontalDefault: Story = {
    args: {
        variant: "horizontal",
        labelOrientation: "horizontal",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const navigation = canvas.getByRole("tablist");
        expect(navigation).toBeInTheDocument();

        const stepButtons = canvas.getAllByRole("tab");
        expect(stepButtons).toHaveLength(3);

        expect(canvas.getByText("Account")).toBeInTheDocument();
        expect(canvas.getByText("Profile")).toBeInTheDocument();
        expect(canvas.getByText("Complete")).toBeInTheDocument();

        expect(canvas.getByText("Previous")).toBeInTheDocument();
        expect(canvas.getByText("Next")).toBeInTheDocument();
    },
};

export const HorizontalVerticalLabels: Story = {
    args: {
        variant: "horizontal",
        labelOrientation: "vertical",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const navigation = canvas.getByRole("tablist");
        expect(navigation).toBeInTheDocument();

        expect(canvas.getByText("Account")).toBeInTheDocument();
        expect(canvas.getByText("Create your account")).toBeInTheDocument();
    },
};

export const Vertical: Story = {
    args: {
        variant: "vertical",
        labelOrientation: "horizontal",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const navigation = canvas.getByRole("tablist");
        expect(navigation).toBeInTheDocument();

        expect(canvas.getByText("Account")).toBeInTheDocument();
        expect(canvas.getByText("Profile")).toBeInTheDocument();
        expect(canvas.getByText("Complete")).toBeInTheDocument();
    },
};

export const Circle: Story = {
    args: {
        variant: "circle",
        labelOrientation: "horizontal",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const navigation = canvas.getByRole("tablist");
        expect(navigation).toBeInTheDocument();

        const progressIndicators = canvas.getAllByRole("progressbar");
        expect(progressIndicators).toHaveLength(3);

        expect(progressIndicators[0]).toHaveAttribute("aria-valuenow", "1");
        expect(progressIndicators[0]).toHaveAttribute("aria-valuemax", "3");
    },
};
