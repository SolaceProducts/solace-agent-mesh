// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import type { FeedbackTrendPoint } from "@/api/feedback";

import { TrendSparkline } from "./TrendSparkline";

const series = (...ratios: number[]): FeedbackTrendPoint[] =>
    ratios.map((negativeRatio, i) => ({
        date: `2026-09-0${i + 1}`,
        totalActivities: 100,
        positiveFeedback: 0,
        negativeFeedback: Math.round(negativeRatio * 100),
        negativeRatio,
    }));

function pathPoints(d: string): Array<{ x: number; y: number }> {
    const numbers = (d.match(/-?\d+(\.\d+)?/g) ?? []).map(Number);
    const points: Array<{ x: number; y: number }> = [];
    for (let i = 0; i + 1 < numbers.length; i += 2) {
        points.push({ x: numbers[i], y: numbers[i + 1] });
    }
    return points;
}

const FLAT_LINE_MIDPOINT_Y = 14;

function expectWellFormedFlatLine(canvasElement: HTMLElement) {
    const path = canvasElement.querySelector(".recharts-line-curve");
    const d = path?.getAttribute("d") ?? "";
    expect(d).not.toMatch(/NaN|Infinity/);
    const ys = pathPoints(d).map(point => point.y);
    expect(ys.length).toBeGreaterThan(0);
    expect(new Set(ys).size).toBe(1);
    expect(ys[0]).toBeCloseTo(FLAT_LINE_MIDPOINT_Y, 0);
}

const meta = {
    title: "Signals & Usage/TrendSparkline",
    component: TrendSparkline,
    parameters: {
        layout: "padded",
        docs: { description: { component: "One row's negative-rate trend, coloured and labelled by direction." } },
    },
    decorators: [
        Story => (
            <div style={{ width: 220 }}>
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof TrendSparkline>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Worsening: Story = {
    args: { trend: series(0.01, 0.01, 0.05, 0.06), subjectName: "Procurement Intake Router" },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByLabelText("Negative rate rising for Procurement Intake Router")).toBeInTheDocument();
    },
};

export const Improving: Story = {
    args: { trend: series(0.06, 0.05, 0.01, 0.01), subjectName: "Vendor Q&A Assistant" },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByLabelText("Negative rate falling for Vendor Q&A Assistant")).toBeInTheDocument();
    },
};

export const Steady: Story = {
    args: { trend: series(0.03, 0.03, 0.03, 0.03), subjectName: "Budget Approval Workflow" },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByLabelText("Negative rate steady for Budget Approval Workflow")).toBeInTheDocument();
        expectWellFormedFlatLine(canvasElement);
    },
};

export const AllZero: Story = {
    name: "All zero (healthy agent)",
    args: { trend: series(0, 0, 0, 0), subjectName: "Invoice Triage Agent" },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByLabelText("Negative rate steady for Invoice Triage Agent")).toBeInTheDocument();
        expectWellFormedFlatLine(canvasElement);
    },
};

export const SinglePoint: Story = {
    name: "Single point",
    args: { trend: series(0.02), subjectName: "Contract Summariser" },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByLabelText("Negative rate steady for Contract Summariser")).toBeInTheDocument();
    },
};

export const NoPoints: Story = {
    args: { trend: [], subjectName: "Newly Deployed Agent" },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByLabelText("Negative rate steady for Newly Deployed Agent")).toBeInTheDocument();
    },
};
