// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect, useRef } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, waitFor, within } from "storybook/test";

import PanZoomCanvas, { type PanZoomCanvasRef } from "@/features/activities/flowChart/PanZoomCanvas";

const meta = {
    title: "Activities/ActivitiesVisualization/PanZoomCanvas",
    component: PanZoomCanvas,
    decorators: [
        Story => (
            <div style={{ width: 400, height: 300, background: "var(--background-w10)" }}>
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof PanZoomCanvas>;

export default meta;
type Story = StoryObj<typeof meta>;

const Content = () => <div style={{ width: 200, height: 100 }}>canvas-child</div>;

const transformedEl = (canvasElement: HTMLElement) => canvasElement.querySelector('[style*="cursor: grab"] > div') as HTMLElement;

export const RendersChildren: Story = {
    args: { children: <Content /> },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("canvas-child")).toBeInTheDocument();
        expect(transformedEl(canvasElement).style.transform).toContain("scale(1)");
    },
};

export const RespectsInitialScale: Story = {
    args: { children: <Content />, initialScale: 1.5 },
    play: async ({ canvasElement }) => {
        expect(transformedEl(canvasElement).style.transform).toContain("scale(1.5)");
    },
};

export const NotifiesTransformChangeOnMount: Story = {
    args: { children: <Content />, onTransformChange: fn(), initialScale: 2 },
    play: async ({ args }) => {
        await waitFor(() => expect(args.onTransformChange).toHaveBeenCalledWith({ scale: 2, x: 0, y: 0 }));
    },
};

export const DoubleClickZoomsIn: Story = {
    args: { children: <Content />, onUserInteraction: fn() },
    play: async ({ canvasElement, args }) => {
        const target = await within(canvasElement).findByText("canvas-child");
        await userEvent.dblClick(target);
        await waitFor(() => expect(transformedEl(canvasElement).style.transform).toMatch(/scale\(1\.1\)/));
        expect(args.onUserInteraction).toHaveBeenCalled();
    },
};

export const DoubleClickSkippedOnButton: Story = {
    args: {
        children: (
            <button type="button" style={{ width: 100, height: 40 }}>
                ignore-me
            </button>
        ),
        onUserInteraction: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const btn = await within(canvasElement).findByRole("button", { name: "ignore-me" });
        await userEvent.dblClick(btn);
        await waitFor(() => expect(transformedEl(canvasElement).style.transform).toContain("scale(1)"));
        expect(args.onUserInteraction).not.toHaveBeenCalled();
    },
};

const RefDriver = ({ action, children = <Content /> }: { action: (ref: PanZoomCanvasRef) => void; children?: ReactNode }) => {
    const ref = useRef<PanZoomCanvasRef>(null);
    const hasRunAction = useRef(false);
    useEffect(() => {
        if (hasRunAction.current || !ref.current) return;
        hasRunAction.current = true;
        action(ref.current);
    }, [action]);
    return (
        <PanZoomCanvas ref={ref} initialScale={1}>
            {children}
        </PanZoomCanvas>
    );
};

const PaddedWideWorkflow = () => (
    <div style={{ minWidth: "100%", minHeight: "100%", padding: "40px" }}>
        <div data-testid="workflow" style={{ width: 600, height: 100, background: "var(--primary-w40)" }}>
            wide workflow
        </div>
    </div>
);

export const FitToContentCentresPaddedWorkflow: Story = {
    args: { children: <></> },
    render: () => (
        <RefDriver action={ref => ref.fitToContent()}>
            <PaddedWideWorkflow />
        </RefDriver>
    ),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const container = canvasElement.querySelector('[style*="cursor: grab"]') as HTMLElement;
        await waitFor(() => {
            const workflow = canvas.getByTestId("workflow").getBoundingClientRect();
            const viewport = container.getBoundingClientRect();
            expect(workflow.left + workflow.width / 2).toBeCloseTo(viewport.left + viewport.width / 2, 0);
            expect(workflow.width).toBeLessThan(viewport.width);
        });
    },
};

export const ZoomInViaRefRoundsToNextTenPercent: Story = {
    args: { children: <></> },
    render: () => <RefDriver action={ref => ref.zoomIn()} />,
    play: async ({ canvasElement }) => {
        await waitFor(() => expect(transformedEl(canvasElement).style.transform).toMatch(/scale\(1\.1\)/));
    },
};

export const ResetTransformRestoresInitialScale: Story = {
    args: { children: <></> },
    render: () => (
        <RefDriver
            action={ref => {
                ref.zoomIn();
                ref.resetTransform();
            }}
        />
    ),
    play: async ({ canvasElement }) => {
        await waitFor(() => {
            const transform = transformedEl(canvasElement).style.transform;
            expect(transform).toContain("scale(1)");
            expect(transform).toContain("translate(0px, 0px)");
        });
    },
};

export const ZoomToClampsToMaxScale: Story = {
    args: { children: <></> },
    render: () => <RefDriver action={ref => ref.zoomTo(99)} />,
    play: async ({ canvasElement }) => {
        await waitFor(() => expect(transformedEl(canvasElement).style.transform).toMatch(/scale\(4\)/));
    },
};
