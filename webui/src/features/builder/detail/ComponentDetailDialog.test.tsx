// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { captureRadixDialogDiagnostics } from "@/testing/helpers/radixDialogDiagnostics";
import type { Agent } from "@/types";

import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";

import { ComponentDetailDialog } from "./ComponentDetailDialog";

vi.mock("@/features/agents/AgentView", () => ({
    AgentView: ({ agent }: { agent: Agent }) => <div data-testid="agent-view">AgentView: {agent.name}</div>,
}));

let mockAgents: Agent[] = [];
let mockAgentsLoading = false;
vi.mock("@/api/agents", () => ({
    useAgents: () => ({ data: mockAgents, isLoading: mockAgentsLoading }),
}));

const agentData = (name: string, overrides: Partial<ComponentCanvasData> = {}): ComponentCanvasData => ({
    name,
    filename: `${name}.yaml`,
    type: "agent",
    yamlText: null,
    isLoading: false,
    isGenerating: false,
    isQueued: false,
    isWaitingForInput: false,
    isBuildingDeps: false,
    isBuilderResponding: false,
    dependencyProgress: [],
    ...overrides,
});

const deployedAgent = (name: string): Agent => ({ id: `id-${name}`, name, description: "" });

const AGENT_VIEW = "agent-view";
const NO_CONFIG_TEXT = "No configuration data available";
const LOADING_TEXT = "Loading agent details…";

describe("ComponentDetailDialog", () => {
    test("renders the real AgentView for an existing agent with no session config", () => {
        mockAgents = [deployedAgent("FriendlyRephraser")];
        mockAgentsLoading = false;

        render(<ComponentDetailDialog data={agentData("FriendlyRephraser", { origin: "platform" })} isOpen onClose={vi.fn()} />);

        expect(screen.getByTestId(AGENT_VIEW)).toHaveTextContent("FriendlyRephraser");
        expect(screen.queryByText(NO_CONFIG_TEXT)).not.toBeInTheDocument();
    });

    test("matches the deployed agent case-insensitively by name", () => {
        mockAgents = [deployedAgent("FriendlyRephraser")];
        mockAgentsLoading = false;

        render(<ComponentDetailDialog data={agentData("friendlyrephraser", { origin: "platform" })} isOpen onClose={vi.fn()} />);

        expect(screen.getByTestId(AGENT_VIEW)).toBeInTheDocument();
    });

    test("shows the loading state while deployed agents are still fetching", () => {
        mockAgents = [];
        mockAgentsLoading = true;

        render(<ComponentDetailDialog data={agentData("Fetching", { origin: "platform" })} isOpen onClose={vi.fn()} />);

        expect(screen.getByText(LOADING_TEXT)).toBeInTheDocument();
        expect(screen.queryByTestId(AGENT_VIEW)).not.toBeInTheDocument();
        expect(screen.queryByText(NO_CONFIG_TEXT)).not.toBeInTheDocument();
    });

    test("shows the empty state for a draft agent with no config and no deployed match", () => {
        mockAgents = [];
        mockAgentsLoading = false;

        render(<ComponentDetailDialog data={agentData("BrandNewDraft")} isOpen onClose={vi.fn()} />);

        expect(screen.queryByTestId(AGENT_VIEW)).not.toBeInTheDocument();
        expect(screen.getByText(NO_CONFIG_TEXT)).toBeInTheDocument();
    });

    test("renders workflow sections from a flat app_config body (builder-authored shape)", () => {
        const flatWorkflowYaml = `apps:
  - app_config:
      description: Flat workflow body description.
      nodes:
        - id: start
          type: agent
          agent_name: Worker
`;
        render(<ComponentDetailDialog data={{ ...agentData("FlatPipeline"), type: "workflow", yamlText: flatWorkflowYaml }} isOpen onClose={vi.fn()} />);

        expect(screen.getByText("Nodes")).toBeInTheDocument();
        expect(screen.getByText("Flat workflow body description.")).toBeInTheDocument();
    });

    test("renders workflow sections from a nested app_config.workflow body (config-apply-authored shape)", () => {
        const nestedWorkflowYaml = `apps:
  - app_config:
      agent_type: workflow
      namespace: acme/prod
      tools:
        - tool_type: builtin
          tool_name: web_request
      workflow:
        description: Nested workflow body description.
        nodes:
          - id: start
            type: agent
            agent_name: Worker
`;
        render(<ComponentDetailDialog data={{ ...agentData("Pipeline"), type: "workflow", yamlText: nestedWorkflowYaml }} isOpen onClose={vi.fn()} />);

        expect(screen.queryByText(NO_CONFIG_TEXT)).not.toBeInTheDocument();
        expect(screen.getByText("Nodes")).toBeInTheDocument();
        expect(screen.getByText("Nested workflow body description.")).toBeInTheDocument();
        expect(screen.getByText("acme/prod")).toBeInTheDocument();
    });

    test("prefers the in-progress YAML over AgentView for a draft that has session config", () => {
        mockAgents = [deployedAgent("Summarizer")];
        mockAgentsLoading = true;

        render(<ComponentDetailDialog data={agentData("Summarizer", { yamlText: "apps:\n  - app_config:\n      instruction: hi\n" })} isOpen onClose={vi.fn()} />);

        expect(screen.queryByTestId(AGENT_VIEW)).not.toBeInTheDocument();
        expect(screen.queryByText(LOADING_TEXT)).not.toBeInTheDocument();
    });

    describe("accessibility", () => {
        let diagnostics: ReturnType<typeof captureRadixDialogDiagnostics>;

        beforeEach(() => {
            diagnostics = captureRadixDialogDiagnostics();
            mockAgents = [];
            mockAgentsLoading = false;
        });

        afterEach(() => diagnostics.restore());

        test("the dialog is named by the humanized component name and described by its type", () => {
            render(<ComponentDetailDialog data={agentData("ticket_classifier")} isOpen onClose={vi.fn()} />);

            expect(screen.getByRole("dialog", { name: "Ticket Classifier", description: "Agent configuration details" })).toBeInTheDocument();
        });

        test("the dialog describes its component type for non-agent components", () => {
            render(<ComponentDetailDialog data={{ ...agentData("Pipeline"), type: "workflow" }} isOpen onClose={vi.fn()} />);

            expect(screen.getByRole("dialog", { name: "Pipeline", description: "Workflow configuration details" })).toBeInTheDocument();
        });

        test("opening the dialog emits no Radix missing-title or missing-description diagnostics", () => {
            render(<ComponentDetailDialog data={agentData("ticket_classifier")} isOpen onClose={vi.fn()} />);

            expect(diagnostics.messages()).toEqual([]);
        });
    });
});

function CardThatOpensDialog() {
    const [open, setOpen] = useState(false);
    return (
        <>
            <div role="group" aria-label="Summarizer card" tabIndex={0} onKeyDown={event => event.key === "Enter" && setOpen(true)} />
            {open && <ComponentDetailDialog data={agentData("Summarizer", { yamlText: "apps:\n  - app_config:\n      instruction: hi\n" })} isOpen onClose={() => setOpen(false)} />}
        </>
    );
}

describe("ComponentDetailDialog focus", () => {
    test("Escape closes the dialog and returns focus to the card that opened it", async () => {
        const user = userEvent.setup();
        render(<CardThatOpensDialog />);
        const card = screen.getByRole("group", { name: "Summarizer card" });
        card.focus();

        fireEvent.keyDown(card, { key: "Enter" });
        expect(await screen.findByRole("dialog")).toBeInTheDocument();
        expect(card).not.toHaveFocus();

        await user.keyboard("{Escape}");

        await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
        await waitFor(() => expect(card).toHaveFocus());
    });
});
