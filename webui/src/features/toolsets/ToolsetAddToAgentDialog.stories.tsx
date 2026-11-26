// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fn, screen, userEvent, waitFor } from "storybook/test";

import { mockStandardAgents } from "@/testing/data";
import { agentToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";
import type { Agent, Toolset, ToolsetFile } from "@/types";

import { ToolsetAddToAgentDialog } from "./ToolsetAddToAgentDialog";

const agentsListHandler = (agents: Agent[]) => http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(agents.map(agentToV2))));

const mockFiles: ToolsetFile[] = [
    {
        id: "file-1",
        filename: "lookup_customer.py",
        toolName: "lookup_customer",
        toolFunction: "lookup_customer",
        toolDescription: "Look up a customer record by ID.",
        timeoutSeconds: 30,
        sandboxProfile: "default",
        modulePath: null,
        fileSize: 1024,
        configSchema: null,
        createdAt: "2026-01-01T00:00:00Z",
    },
    {
        id: "file-2",
        filename: "update_customer.py",
        toolName: "update_customer",
        toolFunction: "update_customer",
        toolDescription: "Update a customer record with new data.",
        timeoutSeconds: 30,
        sandboxProfile: "default",
        modulePath: null,
        fileSize: 2048,
        configSchema: null,
        createdAt: "2026-01-01T00:00:00Z",
    },
    {
        id: "file-3",
        filename: "delete_customer.py",
        toolName: "delete_customer",
        toolFunction: "delete_customer",
        toolDescription: "Permanently remove a customer record.",
        timeoutSeconds: 30,
        sandboxProfile: "default",
        modulePath: null,
        fileSize: 512,
        configSchema: null,
        createdAt: "2026-01-01T00:00:00Z",
    },
];

const mockToolset: Toolset = {
    id: "custom-toolset-1",
    name: "Customer Data Tools",
    description: "Tools for querying and transforming customer records.",
    type: "custom",
    discoveryStatus: "ready",
    discoveryErrors: null,
    zipFilename: "customer-data-tools.zip",
    zipFileSize: 48320,
    files: mockFiles,
    agents: [],
    createdBy: "alice@example.com",
    updatedBy: "alice@example.com",
    createdAt: "2026-03-01T10:00:00Z",
    updatedAt: "2026-03-15T12:00:00Z",
};

const standardAgents = mockStandardAgents;

const agentWithoutSystemPrompt = mockStandardAgents[0];
const agentWithSystemPrompt = mockStandardAgents[1];

const agentResponse = (id: unknown) => {
    const agent = mockStandardAgents.find(a => a.id === id);
    if (agent) return HttpResponse.json({ data: agentToV2(agent) });
    return HttpResponse.json({ message: "Not found", errorId: "test-not-found" }, { status: 404 });
};

const agentHandler = http.get("*/api/v2/platform/agents/:id", ({ params }) => agentResponse(params.id));

const agentPutHandler = http.put("*/api/v2/platform/agents/:id", ({ params }) => agentResponse(params.id));

const agentPutErrorHandler = http.put("*/api/v2/platform/agents/:id", () => HttpResponse.json({ message: "Internal server error", errorId: "stub" }, { status: 500 }));

const meta = {
    title: "Toolsets/ToolsetAddToAgentDialog",
    component: ToolsetAddToAgentDialog,
    parameters: {
        docs: {
            description: {
                component: "Dialog for adding a custom toolset to an agent, with optional tool customization.",
            },
        },
        msw: {
            handlers: withDefaults(agentsListHandler(standardAgents), agentHandler, agentPutHandler),
        },
        chatContext: {
            addNotification: fn(),
        },
    },
} satisfies Meta<typeof ToolsetAddToAgentDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Open: Story = {
    args: {
        isOpen: true,
        toolset: mockToolset,
        onClose: fn(),
    },
    play: async () => {
        expect(await screen.findByText("Add Toolset to Agent")).toBeInTheDocument();
        expect(screen.getByTestId("agentSelectTrigger")).toBeInTheDocument();
        expect(screen.getByTestId("agentInstructionsTextarea")).toBeInTheDocument();
        expect(screen.getByTestId("cancelButton")).toBeInTheDocument();
        expect(screen.getByTestId("addButton")).toBeInTheDocument();
    },
};

export const AgentSelection: Story = {
    args: {
        isOpen: true,
        toolset: mockToolset,
        onClose: fn(),
    },
    parameters: {
        msw: {
            handlers: withDefaults(agentsListHandler([agentWithSystemPrompt]), agentHandler, agentPutHandler),
        },
        chatContext: {
            addNotification: fn(),
        },
    },
    play: async () => {
        await userEvent.click(await screen.findByTestId("agentSelectTrigger"));
        await userEvent.type(await screen.findByPlaceholderText("Search agents..."), "Sales");
        await userEvent.click(await screen.findByRole("button", { name: agentWithSystemPrompt.name }));

        const textarea = screen.getByTestId("agentInstructionsTextarea");
        await waitFor(() => {
            expect((textarea as HTMLTextAreaElement).value).toBe(agentWithSystemPrompt.systemPrompt);
        });
    },
};

export const ValidationErrors: Story = {
    args: {
        isOpen: true,
        toolset: mockToolset,
        onClose: fn(),
    },
    play: async () => {
        await screen.findByText("Add Toolset to Agent");

        await userEvent.click(screen.getByTestId("addButton"));

        expect(await screen.findByText("Please select an agent")).toBeInTheDocument();
        expect(await screen.findByText("Agent instructions are required")).toBeInTheDocument();
    },
};

export const CancelDismisses: Story = {
    args: {
        isOpen: true,
        toolset: mockToolset,
        onClose: fn(),
    },
    play: async ({ args }) => {
        await screen.findByText("Add Toolset to Agent");

        await userEvent.click(screen.getByTestId("cancelButton"));

        expect(args.onClose).toHaveBeenCalledOnce();
    },
};

export const SubmitSuccess: Story = {
    args: {
        isOpen: true,
        toolset: mockToolset,
        onClose: fn(),
    },
    parameters: {
        msw: {
            handlers: withDefaults(agentsListHandler([agentWithoutSystemPrompt]), agentHandler, agentPutHandler),
        },
        chatContext: {
            addNotification: fn(),
        },
    },
    play: async ({ args }) => {
        await screen.findByText("Add Toolset to Agent");

        await userEvent.click(screen.getByTestId("agentSelectTrigger"));
        await userEvent.type(await screen.findByPlaceholderText("Search agents..."), "Support");
        await userEvent.click(await screen.findByRole("button", { name: agentWithoutSystemPrompt.name }));

        const textarea = screen.getByTestId("agentInstructionsTextarea");
        await userEvent.clear(textarea);
        await userEvent.type(textarea, "Use these tools to handle customer queries.");

        await userEvent.click(screen.getByTestId("addButton"));

        await waitFor(() => {
            expect(args.onClose).toHaveBeenCalledOnce();
        });
    },
};

export const SubmitError: Story = {
    args: {
        isOpen: true,
        toolset: mockToolset,
        onClose: fn(),
    },
    parameters: {
        msw: {
            handlers: withDefaults(agentsListHandler([agentWithoutSystemPrompt]), agentHandler, agentPutErrorHandler),
        },
        chatContext: {
            addNotification: fn(),
        },
    },
    play: async () => {
        await screen.findByText("Add Toolset to Agent");

        await userEvent.click(screen.getByTestId("agentSelectTrigger"));
        await userEvent.type(await screen.findByPlaceholderText("Search agents..."), "Support");
        await userEvent.click(await screen.findByRole("button", { name: agentWithoutSystemPrompt.name }));

        const textarea = screen.getByTestId("agentInstructionsTextarea");
        await userEvent.clear(textarea);
        await userEvent.type(textarea, "Handle customer requests.");

        await userEvent.click(screen.getByTestId("addButton"));

        await waitFor(() => {
            expect(screen.getByText(/internal server error|failed to add toolset to agent/i)).toBeInTheDocument();
        });
    },
};

export const SubmitKeysByToolsetName: Story = {
    args: {
        isOpen: true,
        toolset: mockToolset,
        onClose: fn(),
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                agentsListHandler([agentWithoutSystemPrompt]),
                agentHandler,
                http.put("*/api/v2/platform/agents/:id", async ({ request, params }) => {
                    const body = (await request.json()) as Record<string, unknown>;
                    const toolsets = body.toolsets as string[];
                    if (toolsets?.includes(mockToolset.id)) {
                        return HttpResponse.json({ message: "toolsets contains ID instead of name" }, { status: 400 });
                    }
                    if (!toolsets?.includes(mockToolset.name)) {
                        return HttpResponse.json({ message: "toolsets must contain toolset name" }, { status: 400 });
                    }
                    return agentResponse(params.id);
                })
            ),
        },
        chatContext: {
            addNotification: fn(),
        },
    },
    play: async ({ args }) => {
        await screen.findByText("Add Toolset to Agent");

        await userEvent.click(screen.getByTestId("agentSelectTrigger"));
        await userEvent.type(await screen.findByPlaceholderText("Search agents..."), "Support");
        await userEvent.click(await screen.findByRole("button", { name: agentWithoutSystemPrompt.name }));

        const textarea = screen.getByTestId("agentInstructionsTextarea");
        await userEvent.clear(textarea);
        await userEvent.type(textarea, "Handle customer requests with these tools.");

        await userEvent.click(screen.getByTestId("addButton"));

        await waitFor(() => {
            expect(args.onClose).toHaveBeenCalledOnce();
        });
    },
};
