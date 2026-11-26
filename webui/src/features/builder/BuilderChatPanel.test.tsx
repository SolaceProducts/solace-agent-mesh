// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { BuildManifest } from "./hooks/useBuilderManifest";

const VALUE_NOW = "aria-valuenow";
const VALUE_MAX = "aria-valuemax";
const PANEL_TEST_ID = "builder-chat-panel";
const ARROW_RIGHT = "ArrowRight";
const OFFSET_PARENT = "offsetParent";
const GET_RECT = "getBoundingClientRect";

describe("BuilderChatPanel — Test tab gating", () => {
    let BuilderChatPanel: typeof import("./BuilderChatPanel").BuilderChatPanel;
    const displayError = vi.fn();
    let componentDataOverride = new Map<string, unknown>();

    beforeEach(async () => {
        vi.resetModules();
        componentDataOverride = new Map();
        vi.clearAllMocks();

        vi.doMock("./canvas/canvasStyles.css", () => ({}));

        vi.doMock("./hooks/useBuilderManifest", () => ({
            useBuilderManifest: () => manifestOverride,
        }));

        vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: () => new Map() }));
        vi.doMock("./hooks/useBuilderCanvasData", () => ({
            useBuilderCanvasData: () => ({ componentData: componentDataOverride }),
        }));

        vi.doMock("./hooks/useTestArtifacts", () => ({
            useApplyComplete: () => ({ applyComplete: null, isLoading: false, runNumber: null }),
        }));

        vi.doMock("@/api/agent-cards", () => ({
            useAgentCards: () => ({ isLoading: false }),
        }));
        vi.doMock("@/api/workflows", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/workflows")>()), useWorkflowsQuery: () => ({ data: undefined }) }));

        vi.doMock("@openfeature/react-sdk", () => ({ useBooleanFlagValue: () => false, useBooleanFlagDetails: () => ({ value: false }) }));

        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({
                agents: [{ name: "Builder" }],
                handleSubmit: vi.fn(),
                sessionId: "test-session-id",
                isResponding: false,
                messages: [],
                activeSidePanelTab: "files",
                setActiveSidePanelTab: vi.fn(),
                setTaskIdInSidePanel: vi.fn(),
                openSidePanelTab: vi.fn(),
                displayError,
                allArtifacts: [],
            }),
            useTaskContext: () => ({
                monitoredTasks: {},
                loadTaskFromBackend: vi.fn(),
            }),
        }));

        vi.doMock("@/features/chat/ChatArea", () => ({
            ChatArea: () => <div data-testid="chat-area" />,
        }));
        vi.doMock("@/features/activities/flowChart/FlowChartPanel", () => ({
            default: () => <div data-testid="flow-chart-panel" />,
        }));
        vi.doMock("@/features/activities/FlowChartDetails", () => ({
            FlowChartDetails: () => <div data-testid="flow-chart-details" />,
        }));
        vi.doMock("@/features/chat/a2ui/UserInputMessage", () => ({
            UserInputMessage: () => null,
        }));
        vi.doMock("@/features/activities/taskVisualizerProcessor", () => ({
            processTaskForVisualization: () => null,
        }));

        vi.doMock("./TestModePanel", async importOriginal => {
            const real = await importOriginal<typeof import("./TestModePanel")>();
            return {
                ...real,
                TestModePanel: () => <div data-testid="test-mode-panel" />,
            };
        });

        const mod = await import("./BuilderChatPanel");
        BuilderChatPanel = mod.BuilderChatPanel;
    });

    function baseProps() {
        return {
            mode: "fullscreen" as const,
            onModeChange: vi.fn(),
            builderActiveTab: "build" as const,
            onBuilderActiveTabChange: vi.fn(),
            testTargetName: null,
            testRunning: false,
            onTestRunningChange: vi.fn(),
            stopTestRef: { current: null },
            onLatestRunTaskIdChange: vi.fn(),
            selectedTestActivityRun: null,
            onSelectTestActivityRun: vi.fn(),
            isApplyingChanges: false,
            onApplyingChangesChange: vi.fn(),
        };
    }

    function manifestState(manifest: BuildManifest | null) {
        return { manifest, isLoading: false, saveManifest: vi.fn() };
    }

    let manifestOverride: ReturnType<typeof manifestState>;

    test("Test tab is DISABLED and shows tooltip when manifest has no testable component", () => {
        manifestOverride = manifestState({
            name: "empty-build",
            components: [],
        });

        render(<BuilderChatPanel {...baseProps()} />);

        const testTab = screen.getByRole("tab", { name: /test/i });
        expect(testTab).toBeDisabled();

        const tooltipSpan = testTab.closest("span");
        expect(tooltipSpan).not.toBeNull();
        expect(tooltipSpan).toHaveAttribute("tabIndex", "0");
        expect(tooltipSpan?.className).toContain("cursor-not-allowed");
    });

    test("Test tab is DISABLED when manifest contains only platform-origin components", () => {
        manifestOverride = manifestState({
            name: "platform-only",
            components: [{ filename: "agent.yaml", type: "agent", name: "ExistingAgent", origin: "platform" }],
        });

        render(<BuilderChatPanel {...baseProps()} />);

        const testTab = screen.getByRole("tab", { name: /test/i });
        expect(testTab).toBeDisabled();
    });

    test("Test tab stays DISABLED when a session-origin agent exists but is not yet deployed", () => {
        manifestOverride = manifestState({
            name: "my-build",
            components: [{ filename: "agent.yaml", type: "agent", name: "MyAgent", origin: "session", status: "validated" }],
        });

        render(<BuilderChatPanel {...baseProps()} />);

        const testTab = screen.getByRole("tab", { name: /test/i });
        expect(testTab).toBeDisabled();
        const tooltipSpan = testTab.closest("span");
        expect(tooltipSpan).toHaveAttribute("tabIndex", "0");
        expect(tooltipSpan?.className).toContain("cursor-not-allowed");
    });

    test("Test tab is ENABLED and tooltip content is absent when the session-origin agent is deployed", () => {
        manifestOverride = manifestState({
            name: "my-build",
            components: [{ filename: "agent.yaml", type: "agent", name: "MyAgent", origin: "session", status: "deployed" }],
        });

        render(<BuilderChatPanel {...baseProps()} />);

        const testTab = screen.getByRole("tab", { name: /test/i });
        expect(testTab).not.toBeDisabled();

        const tooltipSpan = testTab.closest("span");
        expect(tooltipSpan).not.toBeNull();
        expect(tooltipSpan).not.toHaveAttribute("tabIndex", "0");
        expect(tooltipSpan?.className).not.toContain("cursor-not-allowed");
        expect(screen.queryByText(/before you can test it|Build components first/)).not.toBeInTheDocument();
    });

    test("Test tab is ENABLED when the session-origin workflow is deployed", () => {
        manifestOverride = manifestState({
            name: "workflow-build",
            components: [
                { filename: "agent.yaml", type: "agent", name: "SubAgent", origin: "platform" },
                { filename: "workflow.yaml", type: "workflow", name: "MyWorkflow", origin: "session", status: "deployed" },
            ],
        });

        render(<BuilderChatPanel {...baseProps()} />);

        const testTab = screen.getByRole("tab", { name: /test/i });
        expect(testTab).not.toBeDisabled();
    });

    describe("credential gate on the Test tab", () => {
        const deployedAgent = () =>
            manifestState({
                name: "my-build",
                components: [{ filename: "agent.yaml", type: "agent", name: "MyAgent", origin: "session", status: "deployed" }],
            });

        test("refuses the switch while the gate reports a component with placeholder credentials", async () => {
            manifestOverride = deployedAgent();
            const props = { ...baseProps(), componentsMissingCreds: new Map([["EntKeyAPI", 1]]) };

            render(<BuilderChatPanel {...props} />);
            await clickTestTab();

            expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ title: "Credentials required", error: expect.stringMatching(/EntKeyAPI \(1 credential\).*Testing is blocked/) }));
            expect(props.onBuilderActiveTabChange).not.toHaveBeenCalled();
        });

        const placeholderConnectorData = () => new Map<string, unknown>([["EntKeyAPI", { connectorMetadata: { name: "EntKeyAPI", type: "api", subtype: "openapi", values: { api_key: "<<__SAM_REQUIRED__>>" } } }]]);

        const clickTestTab = async () => {
            localStorage.setItem("sam_builder_skip_test_mode_confirm", "true");
            try {
                await userEvent.setup().click(screen.getByRole("tab", { name: /test/i }));
            } finally {
                localStorage.removeItem("sam_builder_skip_test_mode_confirm");
            }
        };

        const deployedAgentWithConnector = () =>
            manifestState({
                name: "my-build",
                components: [
                    { filename: "agent.yaml", type: "agent", name: "MyAgent", origin: "session", status: "deployed" },
                    { filename: "conn.json", type: "connector", name: "EntKeyAPI", origin: "session", status: "validated" },
                ],
            });

        test("switches when the gate reports nothing outstanding, though the artifact still holds a placeholder", async () => {
            manifestOverride = deployedAgentWithConnector();
            componentDataOverride = placeholderConnectorData();
            const props = { ...baseProps(), componentsMissingCreds: new Map<string, number>() };

            render(<BuilderChatPanel {...props} />);
            await clickTestTab();

            expect(displayError).not.toHaveBeenCalled();
            expect(props.onBuilderActiveTabChange).toHaveBeenCalledWith("test");
        });

        test("without a gate from the page, still refuses on a placeholder in the artifact", async () => {
            manifestOverride = deployedAgentWithConnector();
            componentDataOverride = placeholderConnectorData();
            const props = baseProps();

            render(<BuilderChatPanel {...props} />);
            await clickTestTab();

            expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ title: "Credentials required" }));
            expect(props.onBuilderActiveTabChange).not.toHaveBeenCalled();
        });
    });

    describe("split-mode sizing", () => {
        const splitProps = () => ({ ...baseProps(), mode: "split" as const });
        const splitPanel = () => screen.getByTestId(PANEL_TEST_ID);

        beforeEach(() => {
            manifestOverride = manifestState({ name: "empty-build", components: [] });
        });

        test("the split panel can shrink to make room for the canvas, down to 320px", () => {
            render(<BuilderChatPanel {...splitProps()} />);
            const panel = splitPanel();
            expect(panel.style.minWidth).toBe("320px");
            expect(panel.style.flexShrink).toBe("1");
        });

        test("dragging the resize handle starts from the width the panel is rendered at, not the stored width", () => {
            render(<BuilderChatPanel {...splitProps()} />);
            const panel = splitPanel();
            vi.spyOn(panel, GET_RECT).mockReturnValue({ width: 400 } as DOMRect);

            fireEvent.mouseDown(screen.getByTestId("builder-chat-resize-handle"), { clientX: 100 });
            fireEvent.mouseMove(document, { clientX: 150 });
            fireEvent.mouseUp(document);

            expect(panel.style.width).toBe("450px");
        });

        test("dragging never lets the chat take the room the canvas needs in the row", () => {
            render(<BuilderChatPanel {...splitProps()} />);
            const panel = splitPanel();
            vi.spyOn(panel, GET_RECT).mockReturnValue({ width: 400 } as DOMRect);
            Object.defineProperty(panel, OFFSET_PARENT, { value: { getBoundingClientRect: () => ({ width: 900 }) } });

            fireEvent.mouseDown(screen.getByTestId("builder-chat-resize-handle"), { clientX: 100 });
            fireEvent.mouseMove(document, { clientX: 1100 });
            fireEvent.mouseUp(document);

            expect(panel.style.width).toBe("500px");
        });
    });

    describe("split-mode resize handle keyboard operation", () => {
        const splitProps = () => ({ ...baseProps(), mode: "split" as const });
        const splitPanel = () => screen.getByTestId(PANEL_TEST_ID);
        const handle = () => screen.getByRole("separator", { name: "Resize chat panel" });
        const VIEWPORT = 1440;

        beforeEach(() => {
            manifestOverride = manifestState({ name: "empty-build", components: [] });
            vi.stubGlobal("innerWidth", VIEWPORT);
        });

        afterEach(() => {
            vi.unstubAllGlobals();
        });

        function renderWithPanelAt(width: number, rowWidth: number) {
            render(<BuilderChatPanel {...splitProps()} />);
            const panel = splitPanel();
            vi.spyOn(panel, GET_RECT).mockReturnValue({ width } as DOMRect);
            Object.defineProperty(panel, OFFSET_PARENT, { configurable: true, value: { getBoundingClientRect: () => ({ width: rowWidth }) } });
            return panel;
        }

        test("is a focusable vertical separator with an accessible name", () => {
            render(<BuilderChatPanel {...splitProps()} />);

            expect(handle()).toHaveAttribute("aria-orientation", "vertical");
            expect(handle()).toHaveAttribute("tabindex", "0");
        });

        test("reports the panel width with the same bounds the drag clamp enforces", () => {
            renderWithPanelAt(400, 1184);

            fireEvent.focus(handle());

            expect(handle()).toHaveAttribute(VALUE_NOW, "400");
            expect(handle()).toHaveAttribute("aria-valuemin", "320");
            expect(handle()).toHaveAttribute(VALUE_MAX, "784");
        });

        test("the largest width follows the room the canvas needs in a narrow row", () => {
            renderWithPanelAt(400, 900);

            fireEvent.focus(handle());

            expect(handle()).toHaveAttribute(VALUE_MAX, "500");
        });

        test.each([
            { name: "ArrowRight widens the panel", key: ARROW_RIGHT, width: 400, rowWidth: 1184, expected: "420px" },
            { name: "ArrowLeft narrows the panel", key: "ArrowLeft", width: 400, rowWidth: 1184, expected: "380px" },
            { name: "ArrowRight stops where the canvas would drop below its minimum in a mid-size row", key: ARROW_RIGHT, width: 780, rowWidth: 1184, expected: "784px" },
            { name: "ArrowRight stops at 60% of the viewport in a wide row", key: ARROW_RIGHT, width: 860, rowWidth: 1664, expected: "864px" },
            { name: "ArrowRight stops where the canvas would drop below its minimum", key: ARROW_RIGHT, width: 495, rowWidth: 900, expected: "500px" },
            { name: "ArrowLeft stops at the chat minimum", key: "ArrowLeft", width: 330, rowWidth: 1184, expected: "320px" },
        ])("$name", ({ key, width, rowWidth, expected }) => {
            const panel = renderWithPanelAt(width, rowWidth);

            const notPrevented = fireEvent.keyDown(handle(), { key });

            expect(panel.style.width).toBe(expected);
            expect(notPrevented).toBe(false);
        });

        test.each(["altKey", "ctrlKey", "metaKey"])("an arrow with %s held keeps its browser meaning and does not resize", modifier => {
            const panel = renderWithPanelAt(400, 1184);
            const before = panel.style.width;

            const notPrevented = fireEvent.keyDown(handle(), { key: "ArrowLeft", [modifier]: true });

            expect(notPrevented).toBe(true);
            expect(panel.style.width).toBe(before);
        });

        test("Shift with an arrow still resizes", () => {
            const panel = renderWithPanelAt(400, 1184);

            const notPrevented = fireEvent.keyDown(handle(), { key: "ArrowLeft", shiftKey: true });

            expect(panel.style.width).toBe("380px");
            expect(notPrevented).toBe(false);
        });

        test("keys that do not resize leave the panel and the page alone", () => {
            const panel = renderWithPanelAt(400, 1184);
            const before = panel.style.width;

            const results = ["ArrowUp", "ArrowDown", "Enter", "a"].map(key => fireEvent.keyDown(handle(), { key }));

            expect(panel.style.width).toBe(before);
            expect(results).toEqual([true, true, true, true]);
        });
    });

    describe("split-mode resize handle values without focus", () => {
        const handle = () => screen.getByRole("separator", { name: "Resize chat panel" });
        const layout = { panelWidth: 400, rowWidth: 1664 as number | null };
        const row = { getBoundingClientRect: () => ({ width: layout.rowWidth }) };
        const observers: { callback: ResizeObserverCallback; observe: ReturnType<typeof vi.fn>; disconnect: ReturnType<typeof vi.fn> }[] = [];
        const observerOf = (panel: HTMLElement) => observers.find(o => o.observe.mock.calls.some(([target]) => target === panel))!;

        beforeEach(() => {
            manifestOverride = manifestState({ name: "empty-build", components: [] });
            observers.length = 0;
            layout.panelWidth = 400;
            layout.rowWidth = 1664;
            vi.stubGlobal("innerWidth", 1440);
            vi.stubGlobal(
                "ResizeObserver",
                class {
                    readonly observe = vi.fn();
                    readonly disconnect = vi.fn();
                    constructor(callback: ResizeObserverCallback) {
                        observers.push({ callback, observe: this.observe, disconnect: this.disconnect });
                    }
                }
            );
            vi.spyOn(HTMLElement.prototype, GET_RECT).mockImplementation(function (this: HTMLElement) {
                return { width: this.dataset.testid === PANEL_TEST_ID ? layout.panelWidth : 0 } as DOMRect;
            });
            vi.spyOn(HTMLElement.prototype, OFFSET_PARENT, "get").mockImplementation(function (this: HTMLElement) {
                return (this.dataset.testid === PANEL_TEST_ID && layout.rowWidth !== null ? row : null) as unknown as Element;
            });
        });

        afterEach(() => {
            vi.unstubAllGlobals();
            vi.restoreAllMocks();
        });

        test("a panel mounted in split mode reports its width and bounds from first render", () => {
            render(<BuilderChatPanel {...baseProps()} mode="split" />);

            expect(handle()).toHaveAttribute(VALUE_NOW, "400");
            expect(handle()).toHaveAttribute("aria-valuemin", "320");
            expect(handle()).toHaveAttribute(VALUE_MAX, "864");
        });

        test("a panel with no offset parent is bounded by the viewport instead", () => {
            layout.rowWidth = null;

            render(<BuilderChatPanel {...baseProps()} mode="split" />);

            expect(handle()).toHaveAttribute(VALUE_MAX, "864");
            const panel = screen.getByTestId(PANEL_TEST_ID);
            expect(observerOf(panel).observe.mock.calls.map(([target]) => target)).toEqual([panel]);
        });

        test("follows the panel alone when only its width changes, as after a resize", () => {
            render(<BuilderChatPanel {...baseProps()} mode="split" />);
            const observer = observerOf(screen.getByTestId(PANEL_TEST_ID));

            layout.panelWidth = 520;
            act(() => observer.callback([], {} as ResizeObserver));

            expect(handle()).toHaveAttribute(VALUE_NOW, "520");
            expect(handle()).toHaveAttribute(VALUE_MAX, "864");
        });

        test("follows the row alone when only the room around the panel changes", () => {
            render(<BuilderChatPanel {...baseProps()} mode="split" />);
            const observer = observerOf(screen.getByTestId(PANEL_TEST_ID));

            layout.rowWidth = 900;
            act(() => observer.callback([], {} as ResizeObserver));

            expect(handle()).toHaveAttribute(VALUE_NOW, "400");
            expect(handle()).toHaveAttribute(VALUE_MAX, "500");
        });

        test("watches the panel and its row, and follows them as they resize without any focus", () => {
            render(<BuilderChatPanel {...baseProps()} mode="split" />);
            const observer = observerOf(screen.getByTestId(PANEL_TEST_ID));
            expect(observer.observe.mock.calls.map(([target]) => target)).toEqual([screen.getByTestId(PANEL_TEST_ID), row]);

            layout.panelWidth = 520;
            layout.rowWidth = 900;
            act(() => observer.callback([], {} as ResizeObserver));

            expect(handle()).toHaveAttribute(VALUE_NOW, "520");
            expect(handle()).toHaveAttribute(VALUE_MAX, "500");
        });

        test("stops watching when the panel goes away", () => {
            const { unmount } = render(<BuilderChatPanel {...baseProps()} mode="split" />);
            const observer = observerOf(screen.getByTestId(PANEL_TEST_ID));

            unmount();

            expect(observer.disconnect).toHaveBeenCalledTimes(1);
        });
    });
});
