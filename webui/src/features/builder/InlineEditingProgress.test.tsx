// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";
import { InlineEditingProgress } from "./InlineEditingProgress";

function renderProgress(components: NonNullable<BuildManifest["components"]>) {
    const manifest: BuildManifest = { name: "build", display_name: "Orders", components };
    const componentData = new Map<string, ComponentCanvasData>(components.map(c => [c.name, { isGenerating: true } as ComponentCanvasData]));
    return render(<InlineEditingProgress manifest={manifest} componentData={componentData} isResponding onSwitchToTest={vi.fn()} />);
}

describe("InlineEditingProgress — component type label", () => {
    test.each([
        ["gateway", "Entrypoint"],
        ["a2a_agent", "Remote Agent"],
        ["agent", "Agent"],
        ["workflow", "Workflow"],
        ["connector", "Connector"],
        ["skill", "Skill"],
    ])("labels a %s component %s", (type, label) => {
        renderProgress([{ filename: "c.yaml", type, name: "order_events_entrypoint", status: "validated", origin: "session" }] as NonNullable<BuildManifest["components"]>);
        expect(screen.getByText(label)).toBeInTheDocument();
        expect(screen.queryByText(/gateway/i)).toBeNull();
    });
});
