// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";

import type { FeedbackSummaryRow } from "@/api/feedback";

import { NegativeTrendChart } from "./NegativeTrendChart";

const dates = ["2026-09-01", "2026-09-02", "2026-09-03", "2026-09-04", "2026-09-05"];

function makeRow(id: string, name: string, ratios: number[]): FeedbackSummaryRow {
    const trend = ratios.map((ratio, i) => ({
        date: dates[i],
        totalActivities: 1000,
        positiveFeedback: 40,
        negativeFeedback: Math.round(ratio * 1000),
        negativeRatio: ratio,
    }));
    return {
        subject: { id, name, type: "agent" },
        totalActivities: 5000,
        positiveFeedback: 200,
        negativeFeedback: trend.reduce((sum, p) => sum + p.negativeFeedback, 0),
        negativeRatio: ratios[ratios.length - 1],
        trend,
    };
}

const fiveSubjects: FeedbackSummaryRow[] = [
    makeRow("a1", "Procurement Intake Router", [0.004, 0.006, 0.005, 0.009, 0.011]),
    makeRow("b2", "Vendor Q&A Assistant", [0.003, 0.004, 0.004, 0.005, 0.006]),
    makeRow("c3", "Budget Approval Workflow", [0.002, 0.002, 0.003, 0.003, 0.004]),
    makeRow("d4", "Contract Summariser", [0.001, 0.002, 0.002, 0.002, 0.003]),
    makeRow("e5", "Invoice Triage Agent", [0.001, 0.001, 0.001, 0.002, 0.002]),
];

const meta = {
    title: "Signals & Usage/NegativeTrendChart",
    component: NegativeTrendChart,
    parameters: {
        layout: "padded",
        docs: { description: { component: "Negative-feedback rate over time for the top offending agents, with a Ratio/Count toggle." } },
    },
    args: { granularity: "day" },
    decorators: [
        Story => (
            <div style={{ width: 900 }}>
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof NegativeTrendChart>;

export default meta;
type Story = StoryObj<typeof meta>;

export const FiveSeries: Story = {
    args: { rows: fiveSubjects },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        for (const name of ["Procurement Intake Router", "Invoice Triage Agent"]) {
            await expect(canvas.getAllByText(name).length).toBeGreaterThanOrEqual(1);
        }
    },
};

export const TogglesToCount: Story = {
    args: { rows: fiveSubjects },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const count = canvas.getByRole("tab", { name: "Count" });
        await userEvent.click(count);
        await expect(count).toHaveAttribute("aria-selected", "true");
        await expect(canvas.getByRole("tab", { name: "Ratio" })).toHaveAttribute("aria-selected", "false");
    },
};

export const SingleSeries: Story = {
    name: "Single series (filtered to one agent)",
    args: { rows: [fiveSubjects[0]] },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getAllByText("Procurement Intake Router").length).toBeGreaterThanOrEqual(1);
    },
};

export const SinglePoint: Story = {
    name: "Single point",
    args: { rows: [makeRow("a1", "Procurement Intake Router", [0.004])] },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getAllByText("Procurement Intake Router").length).toBeGreaterThanOrEqual(1);
    },
};

export const NoNegativeFeedback: Story = {
    name: "No negative feedback (flat at zero)",
    args: { rows: fiveSubjects.map(r => ({ ...r, trend: r.trend.map(p => ({ ...p, negativeFeedback: 0, negativeRatio: 0 })) })) },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.queryByText(/No feedback recorded yet/i)).toBeNull();

        await expect(canvas.queryByText(/100%/)).toBeNull();
        await expect(canvas.queryByText(/400%/)).toBeNull();
    },
};

export const SmallRatioBand: Story = {
    name: "Small ratio band (distinct axis ticks)",
    args: {
        rows: [makeRow("a1", "Procurement Intake Router", [0.0007, 0.0008, 0.0009, 0.0011, 0.0012]), makeRow("b2", "Vendor Q&A Assistant", [0.0006, 0.0006, 0.0007, 0.0008, 0.0009])],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const tickLabels = canvas.getAllByText(/%$/).map(el => el.textContent);

        expect(new Set(tickLabels).size).toBe(tickLabels.length);
    },
};

export const CountModeSmallIntegers: Story = {
    name: "Count mode with small integers (no fractional ticks)",
    args: { rows: [makeRow("a1", "Procurement Intake Router", [0.001, 0.002, 0.001, 0.003, 0.001])] },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.click(canvas.getByRole("tab", { name: "Count" }));

        const tickLabels = canvas.getAllByText(/^\d+(\.\d+)?$/).map(el => el.textContent ?? "");
        expect(tickLabels.some(label => label.includes("."))).toBe(false);
    },
};

export const Empty: Story = {
    args: { rows: [] },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByText(/No feedback recorded yet/i)).toBeInTheDocument();
    },
};

export const Loading: Story = {
    args: { rows: [], isLoading: true },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByText(/Loading trend/i)).toBeInTheDocument();
    },
};

export const Failed: Story = {
    args: { rows: [], error: new Error("date range must not exceed 366 days") },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByText(/date range must not exceed 366 days/i)).toBeInTheDocument();
    },
};
