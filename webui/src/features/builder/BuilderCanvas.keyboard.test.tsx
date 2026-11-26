// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { BuilderCanvas } from "./BuilderCanvas";
import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";

class MeasuringResizeObserver {
    private readonly callback: ResizeObserverCallback;
    constructor(callback: ResizeObserverCallback) {
        this.callback = callback;
    }
    observe(target: Element) {
        this.callback([{ target, contentRect: { width: 200, height: 80 } } as unknown as ResizeObserverEntry], this as unknown as ResizeObserver);
    }
    unobserve() {}
    disconnect() {}
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = MeasuringResizeObserver;
class DOMMatrixReadOnlyStub {
    m22 = 1;
}
(globalThis as unknown as { DOMMatrixReadOnly: unknown }).DOMMatrixReadOnly = DOMMatrixReadOnlyStub;
Object.defineProperty(HTMLElement.prototype, "offsetWidth", { configurable: true, get: () => 200 });
Object.defineProperty(HTMLElement.prototype, "offsetHeight", { configurable: true, get: () => 80 });

const server = setupServer(http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })));
beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const SUPPORT_FLOW = "support_flow";
const CLASSIFIER = "Classifier";
const WEATHER_AGENT = "weather_agent";
const INBOUND_GATEWAY = "inbound_gw";
const MOOD_TO_PLAYLIST = "MoodToPlaylist";
const MANIFEST_NAME = "manifest_name";
const EDGE_SELECTOR = ".react-flow__edge";

type Comp = NonNullable<BuildManifest["components"]>[number];
type CanvasProps = Parameters<typeof BuilderCanvas>[0];

function data(overrides: Partial<ComponentCanvasData>): ComponentCanvasData {
    return {
        name: "x",
        filename: "x.yaml",
        type: "agent",
        status: "draft",
        origin: "session",
        yamlText: "ok",
        isLoading: false,
        isGenerating: false,
        isQueued: false,
        isWaitingForInput: false,
        isBuildingDeps: false,
        isBuilderResponding: false,
        dependencyProgress: [],
        ...overrides,
    };
}

interface Scenario {
    label: string;
    components: Comp[];
    componentData: Map<string, ComponentCanvasData>;
    nodeId: string;
    nodeName: string;
}

const workflowSteps = [
    { id: "classify", type: "agent" as const, agent_name: CLASSIFIER },
    { id: "route", type: "switch" as const, cases: [{ condition: "{{ a }}", node: "refund" }], default: "escalate", depends_on: ["classify"] },
    { id: "refund", type: "agent" as const, agent_name: "Refunder", depends_on: ["route"] },
    { id: "escalate", type: "agent" as const, agent_name: "Escalator", depends_on: ["route"] },
];

const workflow = {
    components: [
        { filename: "wf.yaml", type: "workflow", name: SUPPORT_FLOW, nodes: workflowSteps },
        { filename: "c.yaml", type: "agent", name: CLASSIFIER },
        { filename: "r.yaml", type: "agent", name: "Refunder" },
        { filename: "e.yaml", type: "agent", name: "Escalator" },
    ] as Comp[],
    componentData: new Map<string, ComponentCanvasData>([
        [SUPPORT_FLOW, data({ name: SUPPORT_FLOW, type: "workflow", workflowConfig: { nodes: workflowSteps } as ComponentCanvasData["workflowConfig"] })],
        [CLASSIFIER, data({ name: CLASSIFIER })],
        ["Refunder", data({ name: "Refunder" })],
        ["Escalator", data({ name: "Escalator" })],
    ]),
};

const titleCase = (name: string) =>
    name
        .split("_")
        .map(word => word[0].toUpperCase() + word.slice(1))
        .join(" ");

function standalone(type: "agent" | "workflow" | "gateway" | "connector" | "skill" | "a2a_agent", name = `my_${type.replace("_", "")}`, nodeName = titleCase(name)): Scenario {
    return { label: `${type} card ${name}`, components: [{ filename: `${name}.yaml`, type, name }], componentData: new Map([[name, data({ name, type })]]), nodeId: name, nodeName };
}

const weatherSteps = [{ id: "fetch", type: "agent" as const, agent_name: WEATHER_AGENT }];
const snakeCaseWorkflow = {
    components: [
        { filename: "wf.yaml", type: "workflow", name: "Weather", nodes: weatherSteps },
        { filename: "w.yaml", type: "agent", name: WEATHER_AGENT },
    ] as Comp[],
    componentData: new Map<string, ComponentCanvasData>([
        ["Weather", data({ name: "Weather", type: "workflow", workflowConfig: { nodes: weatherSteps } as ComponentCanvasData["workflowConfig"] })],
        [WEATHER_AGENT, data({ name: WEATHER_AGENT })],
    ]),
};

const compactGateway = {
    components: [...workflow.components, { filename: "g.yaml", type: "gateway", name: INBOUND_GATEWAY, depends_on: [SUPPORT_FLOW] }] as Comp[],
    componentData: new Map([...workflow.componentData, [INBOUND_GATEWAY, data({ name: INBOUND_GATEWAY, type: "gateway" })]]),
};

const agentWithOwnConfigName: Scenario = {
    label: "agent card whose config name differs from the manifest name",
    components: [{ filename: "m.yaml", type: "agent", name: MOOD_TO_PLAYLIST }],
    componentData: new Map([[MOOD_TO_PLAYLIST, data({ name: MOOD_TO_PLAYLIST, agentConfig: { name: "mood_to_playlist" } as ComponentCanvasData["agentConfig"] })]]),
    nodeId: MOOD_TO_PLAYLIST,
    nodeName: "Mood To Playlist",
};

const agentWithBlankConfigName: Scenario = {
    label: "agent card whose config name is blank",
    components: [{ filename: "b.yaml", type: "agent", name: MANIFEST_NAME }],
    componentData: new Map([[MANIFEST_NAME, data({ name: "canvas_name", agentConfig: { name: "  " } as ComponentCanvasData["agentConfig"] })]]),
    nodeId: MANIFEST_NAME,
    nodeName: "Canvas Name",
};

const operableScenarios: Scenario[] = [
    standalone("agent"),
    standalone("workflow"),
    standalone("gateway"),
    standalone("connector"),
    standalone("skill"),
    standalone("a2a_agent"),
    standalone("agent", "order_router"),
    standalone("a2a_agent", "remote_helper"),
    { label: "gateway card beside a workflow", ...compactGateway, nodeId: INBOUND_GATEWAY, nodeName: "Inbound Gw" },
    agentWithOwnConfigName,
    agentWithBlankConfigName,
    { label: "workflow agent step with a snake_case agent", ...snakeCaseWorkflow, nodeId: "wf:Weather:fetch", nodeName: WEATHER_AGENT },
    { label: "workflow start", ...workflow, nodeId: "wf:support_flow:__start__", nodeName: "Support Flow" },
    { label: "workflow agent step", ...workflow, nodeId: "wf:support_flow:classify", nodeName: CLASSIFIER },
];

const KEYS = [
    { key: "Enter", send: "{Enter}" },
    { key: "Space", send: " " },
];

function renderCanvas(scenario: Pick<Scenario, "components" | "componentData">) {
    const onNodeSelect = vi.fn();
    const props = { manifest: { name: "build", components: scenario.components }, componentData: scenario.componentData, selectedNodeId: null, onNodeSelect, onNodeDoubleClick: () => {}, isEmpty: false, isLoading: false } as CanvasProps;
    const view = render(
        <StoryProvider chatContextValues={{ sessionId: "s1" } as unknown as Partial<ChatContextValue>}>
            <div style={{ width: 800, height: 600 }}>
                <BuilderCanvas {...props} />
            </div>
        </StoryProvider>
    );
    const node = async (id: string) => {
        await waitFor(() => expect(view.container.querySelector(`.react-flow__node[data-id="${id}"]`)).not.toBeNull());
        return view.container.querySelector<HTMLElement>(`.react-flow__node[data-id="${id}"]`)!;
    };
    return { ...view, onNodeSelect, node };
}

async function tabStops(): Promise<HTMLElement[]> {
    const user = userEvent.setup();
    const stops: HTMLElement[] = [];
    for (let i = 0; i < 40; i++) {
        await user.tab();
        const active = document.activeElement as HTMLElement;
        if (active === document.body) break;
        stops.push(active);
    }
    return stops;
}

describe("BuilderCanvas keyboard operation", () => {
    describe.each(operableScenarios)("$label", scenario => {
        test.each(KEYS)("$key on the focused card opens its details once", async ({ send }) => {
            const user = userEvent.setup();
            const { node, onNodeSelect } = renderCanvas(scenario);
            (await node(scenario.nodeId)).focus();

            await user.keyboard(send);

            expect(onNodeSelect).toHaveBeenCalledTimes(1);
            expect(onNodeSelect).toHaveBeenCalledWith(scenario.nodeId);
        });

        test("a click still opens its details exactly once", async () => {
            const { node, onNodeSelect } = renderCanvas(scenario);

            fireEvent.click(await node(scenario.nodeId));

            expect(onNodeSelect).toHaveBeenCalledTimes(1);
            expect(onNodeSelect).toHaveBeenCalledWith(scenario.nodeId);
        });

        test("the card is a named, focusable group that says how to open it", async () => {
            const { node } = renderCanvas(scenario);
            const card = await node(scenario.nodeId);

            expect(card).toHaveAttribute("tabindex", "0");
            expect(card).toHaveAccessibleName(scenario.nodeName);
            expect(card).toHaveAccessibleDescription("Press Enter or Space to open details.");
        });

        test("the accessible name is text the card shows", async () => {
            const { node } = renderCanvas(scenario);
            const card = await node(scenario.nodeId);

            expect(card.textContent).toContain(card.getAttribute("aria-label"));
        });
    });

    test("other keys do nothing on a focused card", async () => {
        const user = userEvent.setup();
        const { node, onNodeSelect } = renderCanvas(standalone("agent"));
        (await node("my_agent")).focus();

        await user.keyboard("a{Escape}{ArrowRight}{Delete}");

        expect(onNodeSelect).not.toHaveBeenCalled();
    });

    test("holding Space neither activates the card again nor scrolls the page", async () => {
        const { node, onNodeSelect } = renderCanvas(standalone("agent"));
        const card = await node("my_agent");

        fireEvent.keyDown(card, { key: " " });
        const repeatNotPrevented = fireEvent.keyDown(card, { key: " ", repeat: true });

        expect(repeatNotPrevented).toBe(false);
        expect(onNodeSelect).toHaveBeenCalledTimes(1);
    });

    test("holding Enter does not activate the card again", async () => {
        const { node, onNodeSelect } = renderCanvas(standalone("agent"));
        const card = await node("my_agent");

        fireEvent.keyDown(card, { key: "Enter" });
        fireEvent.keyDown(card, { key: "Enter", repeat: true });

        expect(onNodeSelect).toHaveBeenCalledTimes(1);
    });

    test("a component of a type the canvas has no card for is not a tab stop", async () => {
        const { node } = renderCanvas({ components: [{ filename: "m.yaml", type: "mcp_server", name: "Mystery" } as Comp], componentData: new Map([["Mystery", data({ name: "Mystery" })]]) });
        const card = await node("Mystery");

        expect(card).not.toHaveAttribute("tabindex");
    });

    test("Space on a card does not scroll the page", async () => {
        const { node } = renderCanvas(standalone("agent"));
        const card = await node("my_agent");

        expect(fireEvent.keyDown(card, { key: " " })).toBe(false);
    });

    test("Enter on a control inside a card is left to that control", async () => {
        const { node, onNodeSelect } = renderCanvas(standalone("agent"));
        const card = await node("my_agent");
        const control = card.appendChild(document.createElement("button"));
        control.dataset.id = "nested-control";

        fireEvent.keyDown(control, { key: "Enter" });
        fireEvent.keyDown(control, { key: " " });

        expect(onNodeSelect).not.toHaveBeenCalled();
    });

    test("only the cards that open details are tab stops, in reading order", async () => {
        const { node } = renderCanvas(workflow);
        await node("wf:support_flow:__end__");

        const cardStops = (await tabStops()).filter(el => el.classList.contains("react-flow__node")).map(el => el.dataset.id);

        expect(cardStops).toEqual(["wf:support_flow:__start__", "wf:support_flow:classify", "wf:support_flow:refund", "wf:support_flow:escalate"]);
    });

    test("connector lines are not tab stops and are hidden from assistive technology", async () => {
        const { container, node } = renderCanvas(workflow);
        await node("wf:support_flow:__end__");
        await waitFor(() => expect(container.querySelectorAll(EDGE_SELECTOR).length).toBeGreaterThan(0));

        const stops = await tabStops();

        expect(stops.filter(el => el.closest(EDGE_SELECTOR))).toEqual([]);
        for (const edge of container.querySelectorAll(EDGE_SELECTOR)) {
            expect(edge).not.toHaveAttribute("tabindex");
            expect(edge).toHaveAttribute("aria-hidden", "true");
        }
    });
});
