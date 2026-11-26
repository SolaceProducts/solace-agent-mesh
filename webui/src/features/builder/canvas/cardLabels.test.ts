// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Node } from "@xyflow/react";
import { describe, expect, test } from "vitest";

import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";

import { agentCardName, cardAccessibleName } from "./cardLabels";

const node = (type: string, data: Record<string, unknown>): Node => ({ id: "n", position: { x: 0, y: 0 }, type, data });
const canvasData = (overrides: Partial<ComponentCanvasData>) => ({ name: "", filename: "a.yaml", type: "agent", ...overrides }) as ComponentCanvasData;

describe("agentCardName", () => {
    test.each([
        { name: "prefers the name in the agent's own config", data: canvasData({ name: "Canvas", agentConfig: { name: "mood_to_playlist" } as ComponentCanvasData["agentConfig"] }), component: "MoodToPlaylist", expected: "mood_to_playlist" },
        { name: "unless that name is blank", data: canvasData({ name: "Canvas", agentConfig: { name: "  " } as ComponentCanvasData["agentConfig"] }), component: "Manifest", expected: "Canvas" },
        { name: "then the canvas data name", data: canvasData({ name: "Canvas" }), component: "Manifest", expected: "Canvas" },
        { name: "then the manifest name when the canvas data name is empty", data: canvasData({ name: "" }), component: "Manifest", expected: "Manifest" },
        { name: "then the manifest name", data: null, component: "Manifest", expected: "Manifest" },
        { name: "and a generic label when nothing is named", data: undefined, component: undefined, expected: "Agent" },
    ])("$name", ({ data, component, expected }) => {
        expect(agentCardName(data, component)).toBe(expected);
    });
});

describe("cardAccessibleName follows what each card shows", () => {
    test.each([
        {
            name: "an agent card shows its config name, humanized",
            n: node("agent", { component: { name: "MoodToPlaylist" }, canvasData: canvasData({ agentConfig: { name: "mood_to_playlist" } as ComponentCanvasData["agentConfig"] }) }),
            expected: "Mood To Playlist",
        },
        { name: "an agent card with no config shows the manifest name, humanized", n: node("agent", { component: { name: "order_router" }, canvasData: null }), expected: "Order Router" },
        { name: "a workflow step shows its agent name as written", n: node("wf-agent", { layoutNode: { data: { agentName: "weather_agent", label: "Step" } } }), expected: "weather_agent" },
        { name: "a workflow step falls back to its label", n: node("wf-agent", { layoutNode: { data: { label: "fetch_weather" } } }), expected: "fetch_weather" },
        { name: "a workflow start card shows the workflow name, humanized", n: node("wf-start", { workflowComponent: { name: "support_flow" } }), expected: "Support Flow" },
        { name: "any other card shows the component name, humanized", n: node("gateway", { component: { name: "my_gateway" } }), expected: "My Gateway" },
    ])("$name", ({ n, expected }) => {
        expect(cardAccessibleName(n)).toBe(expected);
    });

    test.each([
        { name: "an empty component name", n: node("gateway", { component: { name: "" } }) },
        { name: "a blank component name", n: node("skill", { component: { name: "  " } }) },
        { name: "a workflow start card without its workflow", n: node("wf-start", {}) },
        { name: "a workflow step without a name or label", n: node("wf-agent", { layoutNode: { data: { agentName: " ", label: "" } } }) },
        { name: "a non-string name", n: node("connector", { component: { name: 42 } }) },
    ])("gives no name for $name", ({ n }) => {
        expect(cardAccessibleName(n)).toBeUndefined();
    });
});
