// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import { ReactFlowProvider } from "@xyflow/react";
import { describe, expect, test } from "vitest";

import { makeNodeProps } from "@/testing/helpers/nodeProps";

import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuilderActiveTab } from "../useBuilderPageState";

import { CanvasViewProvider } from "./canvasViewContext";
import { GatewayCardNode } from "./GatewayCardNode";

function canvasData(over: Partial<ComponentCanvasData> = {}): ComponentCanvasData {
    return {
        name: "OrderEvents",
        filename: "order-events.yaml",
        type: "gateway",
        status: "deployed",
        origin: "session",
        yamlText: null,
        isLoading: false,
        isGenerating: false,
        isQueued: false,
        isWaitingForInput: false,
        isBuildingDeps: false,
        isBuilderResponding: false,
        dependencyProgress: [],
        ...over,
    };
}

function renderCompact(status: string, data: Partial<ComponentCanvasData> = {}, opts: { tab?: BuilderActiveTab; isApplyingChanges?: boolean } = {}) {
    const props = makeNodeProps({
        component: { name: "OrderEvents", filename: "order-events.yaml", type: "gateway", status },
        canvasData: canvasData({ status, ...data }),
        compact: true,
    }) as ComponentProps<typeof GatewayCardNode>;
    return render(
        <CanvasViewProvider builderActiveTab={opts.tab ?? "build"} testTargetName={null} onSelectTestTarget={() => {}} isApplyingChanges={opts.isApplyingChanges ?? false}>
            <ReactFlowProvider>
                <GatewayCardNode {...props} />
            </ReactFlowProvider>
        </CanvasViewProvider>
    );
}

describe("compact entrypoint pill inside a workflow group", () => {
    test("a deployed entrypoint reads Active", () => {
        renderCompact("deployed");
        expect(screen.getByText("Active")).toBeInTheDocument();
    });

    test("a validated, undeployed entrypoint reads Ready, not Active", () => {
        renderCompact("validated");
        expect(screen.getByText("Ready")).toBeInTheDocument();
        expect(screen.queryByText("Active")).toBeNull();
    });

    test("a failed entrypoint reads Error and not a success state", () => {
        renderCompact("error");
        expect(screen.getByText("Error")).toBeInTheDocument();
        expect(screen.queryByText("Active")).toBeNull();
        expect(screen.queryByText("Ready")).toBeNull();
    });

    test("a deployed entrypoint being regenerated reads Editing, not Creating", () => {
        renderCompact("deployed", { isGenerating: true });
        expect(screen.getByText("Editing")).toBeInTheDocument();
        expect(screen.queryByText("Creating")).toBeNull();
    });

    test("an entrypoint being generated for the first time reads Creating", () => {
        renderCompact("draft", { isGenerating: true });
        expect(screen.getByText("Creating")).toBeInTheDocument();
    });

    test("an unbuilt entrypoint reads Draft", () => {
        renderCompact("draft");
        expect(screen.getByText("Draft")).toBeInTheDocument();
    });

    test("a queued entrypoint reads Creating, not Draft", () => {
        renderCompact("draft", { isQueued: true });
        expect(screen.getByText("Creating")).toBeInTheDocument();
        expect(screen.queryByText("Draft")).toBeNull();
    });

    test("an entrypoint waiting for input reads the waiting text", () => {
        renderCompact("deployed", { isWaitingForInput: true });
        expect(screen.getByText("Waiting for Input...")).toBeInTheDocument();
        expect(screen.queryByText("Active")).toBeNull();
    });

    test("Test mode shows no status pill on a deployed entrypoint", () => {
        renderCompact("deployed", {}, { tab: "test" });
        expect(screen.queryByText("Active")).toBeNull();
        expect(screen.queryByText("Ready")).toBeNull();
    });
});
