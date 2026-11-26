// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test } from "vitest";

import type { WorkflowNodeConfig } from "@/utils/agentUtils";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import WorkflowNodeDetailPanel from "./WorkflowNodeDetailPanel";
import meta, { AgentNode, DecisionNode, LoopNode, MapNode, SwitchNode, ToolNode, ToolNodeWithConnector, ToolNodeWithConnectorOnly, ToolNodeWithToolset } from "./WorkflowNodeDetailPanel.stories";

describe("WorkflowNodeDetailPanel", () => {
    describe("View Toggles", () => {
        test("toggles between Details and Code view", async () => {
            const Story = composeStory(AgentNode, meta);
            render(<Story />);
            const user = userEvent.setup();

            expect(await screen.findByRole("complementary", { name: "Node details panel" })).toBeInTheDocument();

            const detailsButton = screen.getByRole("tab", { name: "Details view" });
            const codeButton = screen.getByRole("tab", { name: "Code view" });

            expect(detailsButton).toBeInTheDocument();
            expect(codeButton).toBeInTheDocument();

            await user.click(codeButton);

            expect(screen.getByTestId("Copy")).toBeInTheDocument();

            await user.click(detailsButton);

            expect(screen.queryByTestId("Copy")).not.toBeInTheDocument();
        });
    });

    describe("Loop Node", () => {
        test("displays loop node properties", async () => {
            const Story = composeStory(LoopNode, meta);
            render(<Story />);

            expect(await screen.findByText("Max Iterations")).toBeInTheDocument();
            expect(screen.getByText("10")).toBeInTheDocument();
            expect(screen.getByText("Condition")).toBeInTheDocument();
            expect(screen.getByText("Delay")).toBeInTheDocument();
            expect(screen.getByText("5s")).toBeInTheDocument();
        });
    });

    describe("Agent Node", () => {
        test("displays agent node properties", async () => {
            const Story = composeStory(AgentNode, meta);
            render(<Story />);

            expect(await screen.findByText("OrderValidator")).toBeInTheDocument();
            expect(screen.getByText("validate_order")).toBeInTheDocument();
        });

        test("displays input and output tabs", async () => {
            const Story = composeStory(AgentNode, meta);
            render(<Story />);
            const user = userEvent.setup();

            const inputTab = await screen.findByRole("tab", { name: "Input" });
            const outputTab = screen.getByRole("tab", { name: "Output" });

            expect(inputTab).toBeInTheDocument();
            expect(outputTab).toBeInTheDocument();

            await user.click(outputTab);

            expect(outputTab).toHaveAttribute("aria-selected", "true");
        });
    });

    describe("Tool Node", () => {
        test("displays tool node properties", async () => {
            const Story = composeStory(ToolNode, meta);
            render(<Story />);

            expect(await screen.findByText("Tool", { selector: "span" })).toBeInTheDocument();
            expect(screen.queryByText("Toolset", { selector: "span" })).not.toBeInTheDocument();
            expect(screen.queryByText("Connector", { selector: "span" })).not.toBeInTheDocument();
            expect(screen.getAllByText("create_pptx").length).toBeGreaterThan(0);
        });

        test("shows the connector and tool a connector-bound tool node calls", async () => {
            const Story = composeStory(ToolNodeWithConnector, meta);
            render(<Story />);

            expect(await screen.findByText("Connector", { selector: "span" })).toBeInTheDocument();
            expect(screen.getByText("PetStoreAPI", { selector: "code" })).toBeInTheDocument();
            expect(screen.getByText("find_pets_by_status", { selector: "code" })).toBeInTheDocument();
            expect(screen.queryByText("Toolset", { selector: "span" })).not.toBeInTheDocument();
            expect(screen.getByTitle("PetStoreAPI / find_pets_by_status")).toBeInTheDocument();
        });

        test("shows only the connector for a connector-bound node that names no tool", async () => {
            const Story = composeStory(ToolNodeWithConnectorOnly, meta);
            render(<Story />);

            expect(await screen.findByText("Connector", { selector: "span" })).toBeInTheDocument();
            expect(screen.getByText("SlackConn", { selector: "code" })).toBeInTheDocument();
            expect(screen.queryByText("Tool", { selector: "span" })).not.toBeInTheDocument();
            expect(screen.queryByText("Toolset", { selector: "span" })).not.toBeInTheDocument();
            expect(screen.getByTitle("SlackConn")).toBeInTheDocument();
        });

        test("splits a toolset-bundled tool name into Toolset and Tool fields", async () => {
            const Story = composeStory(ToolNodeWithToolset, meta);
            render(<Story />);

            expect(await screen.findByText("Toolset", { selector: "span" })).toBeInTheDocument();
            expect(screen.getByText("Tool", { selector: "span" })).toBeInTheDocument();
            expect(screen.getByText("WeatherToolset", { selector: "code" })).toBeInTheDocument();
            expect(screen.getByText("get_current_weather", { selector: "code" })).toBeInTheDocument();
            expect(screen.queryByText("Connector", { selector: "span" })).not.toBeInTheDocument();
        });

        test.each([
            ["a number", 123],
            ["a list", ["create_pptx"]],
            ["a map", { name: "create_pptx" }],
        ])("renders a tool node whose tool_name is %s without a Tool field", async (_label, toolName) => {
            const node = createWorkflowLayoutNode({
                id: "malformed_tool",
                type: "tool",
                data: { label: "malformed_tool", originalConfig: { id: "malformed_tool", type: "tool", tool_name: toolName } as unknown as WorkflowNodeConfig },
            });
            render(<WorkflowNodeDetailPanel node={node} workflowConfig={null} agents={[]} />);

            expect(await screen.findByRole("complementary", { name: "Node details panel" })).toBeInTheDocument();
            expect(screen.getByTitle("malformed_tool")).toBeInTheDocument();
            expect(screen.queryByText("Tool", { selector: "span" })).not.toBeInTheDocument();
        });

        test("uses the published config's tool name for the title and the Tool field when the node data holds a malformed one", async () => {
            const node = createWorkflowLayoutNode({
                id: "fallback_tool",
                type: "tool",
                data: { toolName: 123 as unknown as string, label: "fallback_tool", originalConfig: { id: "fallback_tool", type: "tool", tool_name: "create_pptx" } },
            });
            render(<WorkflowNodeDetailPanel node={node} workflowConfig={null} agents={[]} />);

            expect(await screen.findByTitle("create_pptx")).toBeInTheDocument();
            expect(screen.getByText("create_pptx", { selector: "code" })).toBeInTheDocument();
        });

        test("prefers the node data's tool name over a different one in the published config", async () => {
            const node = createWorkflowLayoutNode({
                id: "both_tool",
                type: "tool",
                data: { toolName: "from_data", label: "both_tool", originalConfig: { id: "both_tool", type: "tool", tool_name: "from_config" } },
            });
            render(<WorkflowNodeDetailPanel node={node} workflowConfig={null} agents={[]} />);

            expect(await screen.findByTitle("from_data")).toBeInTheDocument();
            expect(screen.getByText("from_data", { selector: "code" })).toBeInTheDocument();
            expect(screen.queryByText("from_config")).not.toBeInTheDocument();
        });

        test("displays input and output tabs from tool schemas", async () => {
            const Story = composeStory(ToolNode, meta);
            render(<Story />);
            const user = userEvent.setup();

            const inputTab = await screen.findByRole("tab", { name: "Input" });
            const outputTab = screen.getByRole("tab", { name: "Output" });

            expect(inputTab).toBeInTheDocument();
            expect(outputTab).toBeInTheDocument();

            await user.click(outputTab);
            expect(outputTab).toHaveAttribute("aria-selected", "true");
        });
    });

    describe("Map Node", () => {
        test("displays map node properties", async () => {
            const Story = composeStory(MapNode, meta);
            render(<Story />);

            expect(screen.getAllByText("process_items").length).toBeGreaterThan(0);
            expect(screen.getByText("Items")).toBeInTheDocument();
        });
    });

    describe("Switch Node", () => {
        test("displays switch node cases", async () => {
            const Story = composeStory(SwitchNode, meta);
            render(<Story />);

            expect(screen.getAllByText("check_priority").length).toBeGreaterThan(0);
            expect(screen.getByText("Cases")).toBeInTheDocument();
            expect(screen.queryByText(/^→/)).not.toBeInTheDocument();
        });
    });

    describe("Decision Node", () => {
        test("displays questions and escalation without a routes table", async () => {
            const Story = composeStory(DecisionNode, meta);
            render(<Story />);

            expect(await screen.findByText("Questions")).toBeInTheDocument();
            expect(screen.getByRole("button", { name: "Routes on this question" })).toBeInTheDocument();
            expect(screen.queryByRole("table", { name: "Decision routes" })).not.toBeInTheDocument();
            expect(screen.getByText("Escalation Threshold")).toBeInTheDocument();
            expect(screen.queryByText("Escalation Choice")).not.toBeInTheDocument();
            expect(screen.queryByText("Cases")).not.toBeInTheDocument();
        });

        test("explains the input in a tooltip on the help icon", async () => {
            const Story = composeStory(DecisionNode, meta);
            render(<Story />);
            const user = userEvent.setup();

            expect(screen.queryByText("The decision model answers the questions below using this input.")).not.toBeInTheDocument();
            await user.hover(screen.getByRole("button", { name: "Input help" }));

            expect(await screen.findByRole("tooltip")).toHaveTextContent("The decision model answers the questions below using this input.");
        });

        test("explains the route icon in a tooltip", async () => {
            const Story = composeStory(DecisionNode, meta);
            render(<Story />);
            const user = userEvent.setup();

            await user.hover(screen.getByRole("button", { name: "Routes on this question" }));

            expect(await screen.findByRole("tooltip")).toHaveTextContent("The decision node routes on the answer to this question.");
        });

        test("explains on_provider_error in a tooltip on the help icon", async () => {
            const Story = composeStory(DecisionNode, meta);
            render(<Story />);
            const user = userEvent.setup();

            await user.hover(screen.getByRole("button", { name: "On Provider Error help" }));

            expect(await screen.findByRole("tooltip")).toHaveTextContent("escalate sends a transient provider failure to the escalate target");
        });

        test("lists the routed-on question first", () => {
            const Story = composeStory(DecisionNode, meta);
            const config = DecisionNode.args!.node!.data.originalConfig!;
            const reordered = { ...config, questions: [...(config.questions as unknown[])].reverse() } as typeof config;
            render(<Story node={{ ...DecisionNode.args!.node!, data: { ...DecisionNode.args!.node!.data, originalConfig: reordered } }} workflowConfig={null} />);

            const questionKeys = within(screen.getByTestId("decisionNodeDetails"))
                .getAllByRole("code")
                .map(el => el.textContent);
            expect(questionKeys[0]).toBe(config.route_on);
        });

        describe("escalation mode", () => {
            const renderWithMode = (overrides: Record<string, unknown>) => {
                const Story = composeStory(DecisionNode, meta);
                const node = DecisionNode.args!.node!;
                const config = { ...node.data.originalConfig!, ...overrides } as typeof node.data.originalConfig;
                render(<Story node={{ ...node, data: { ...node.data, originalConfig: config } }} workflowConfig={null} />);
                return within(screen.getByTestId("decisionNodeDetails"))
                    .getAllByRole("code")
                    .map(el => el.textContent);
            };

            test("shows reroute when escalation_mode is unset", () => {
                expect(renderWithMode({ escalation_mode: undefined })).toContain("reroute");
                expect(screen.getByText("Escalation Mode")).toBeInTheDocument();
            });

            test("shows handoff when set", () => {
                const codes = renderWithMode({ escalation_mode: "handoff" });
                expect(codes).toContain("handoff");
                expect(codes).not.toContain("reroute");
            });

            test("shows an unrecognised value as written", () => {
                expect(renderWithMode({ escalation_mode: "Handoff" })).toContain("Handoff");
            });

            test("is not shown without an escalate target", () => {
                renderWithMode({ escalate: undefined, min_confidence: undefined, escalation_mode: "handoff" });
                expect(screen.queryByText("Escalation Mode")).not.toBeInTheDocument();
            });
        });

        test("shows only the decision fields that are set", () => {
            const Story = composeStory(DecisionNode, meta);
            const config = DecisionNode.args!.node!.data.originalConfig!;
            const bareNode = { ...DecisionNode.args!.node!, data: { label: "bare", originalConfig: { id: config.id, type: config.type, questions: config.questions } } };
            render(<Story node={bareNode} workflowConfig={null} />);

            expect(screen.getByText("Questions")).toBeInTheDocument();
            expect(screen.queryByRole("button", { name: "Routes on this question" })).not.toBeInTheDocument();
            expect(screen.queryByText("Escalation Threshold")).not.toBeInTheDocument();
            expect(screen.queryByText("On Provider Error")).not.toBeInTheDocument();
        });
    });
});
