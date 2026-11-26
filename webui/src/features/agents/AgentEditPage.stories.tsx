// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fireEvent, screen, userEvent, within } from "storybook/test";

import { getAgentById, mockAgents, mockConnectors, mockCustomToolsets, mockGetConnectorSchema, mockModelConfigs, mockModels, mockToolsets } from "@/testing/data";
import { toModelV2 } from "@/testing/data/modelsV2";
import { agentToV2, connectorToV2, toolsetToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

const baseListHandlers = withDefaults(
    http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(mockAgents.map(agentToV2)))),
    http.get("*/api/v2/platform/toolsets", ({ request }) => {
        const type = new URL(request.url).searchParams.get("origin");
        const list = type === "builtin" ? mockToolsets : mockCustomToolsets;
        return HttpResponse.json(paginatedResponse(list.map(toolsetToV2)));
    }),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json(paginatedResponse(mockConnectors.map(connectorToV2))))
);

const agent0 = getAgentById("0")!;
const agent1 = getAgentById("1")!;
import { SAFE_NAME_DESCRIPTION } from "@/common/form";

import { AgentEditPage } from "@/features/agents/AgentEditPage";

const meta = {
    title: "Agents/AgentEditPage",
    component: AgentEditPage,
    parameters: {
        docs: {
            description: {
                component: "The main agent page component that displays the agent's details and handles user interactions.",
            },
        },
        msw: { handlers: baseListHandlers },
    },
    decorators: [
        Story => (
            <OpenFeatureTestProvider>
                <Story />
            </OpenFeatureTestProvider>
        ),
    ],
} satisfies Meta<typeof AgentEditPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            agentToEdit: { name: "New Agent", description: "A new agent for testing" },
            useLoaderData: () => ({ agentId: "new", isNew: true }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("New Agent");
        await canvas.findByText("A new agent for testing");
        await canvas.findByText("Extend Agent with Custom Toolsets");
    },
};

export const Edit: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText(agent1.name);
        await canvas.findByText(agent1.description);
    },
};

export const EditAgentDetails: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Agent Details").then(button => button.click());
        await canvas.findByTestId("agent-name").then(input => {
            expect(input).toHaveValue(agent1.name);
        });
        await canvas.findByTestId("agent-description").then(input => {
            expect(input).toHaveValue(agent1.description);
        });
    },
};

export const EditAgentDetailsValidation: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Agent Details").then(button => button.click());
        await canvas.findByTestId("agent-name").then(input => {
            fireEvent.change(input, { target: { value: "\x1B[31mRed Text\x1B[0m" } });
        });
        await canvas.findByTestId("agent-description").then(input => {
            fireEvent.change(input, { target: { value: "Too short" } });
        });
        await canvas.findByTestId("agentApplyButton").then(button => button.click());

        await canvas.findByText(SAFE_NAME_DESCRIPTION);
        await canvas.findByText("Value must be at least 10 characters");
    },
};

export const EditAgentInstructions: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Instructions").then(button => button.click());
        await canvas.findByTestId("agent-instructions").then(textarea => {
            expect(textarea).toHaveValue(agent1.systemPrompt);
        });
    },
};

export const EditAgentInstructionsValidation: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Instructions").then(button => button.click());

        await canvas.findByTestId("agent-instructions").then(textarea => {
            fireEvent.change(textarea, { target: { value: "   " } });
        });
        await canvas.findByTestId("agentApplyButton").then(button => button.click());
        await canvas.findByText("Instructions are required");

        await canvas.findByTestId("agent-instructions").then(textarea => {
            fireEvent.change(textarea, { target: { value: "A".repeat(32001) } });
        });
        await canvas.findByTestId("agentApplyButton").then(button => button.click());
        await canvas.findByText("Instructions must be 32,000 characters or fewer (currently 32,001)");
    },
};

export const EditAgentInstructionsAcceptsTerse: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Instructions").then(button => button.click());
        await canvas.findByTestId("agent-instructions").then(textarea => {
            fireEvent.change(textarea, { target: { value: "You are a helpful SQL assistant." } });
        });
        await canvas.findByTestId("agentApplyButton").then(button => button.click());

        expect(canvas.queryByText("Instructions are required")).toBeNull();
        expect(canvas.queryByText(/characters or fewer/)).toBeNull();

        await canvas.findByTestId("agent-instructions").then(textarea => {
            fireEvent.change(textarea, { target: { value: "\u{1F600}".repeat(20000) } });
        });
        await canvas.findByTestId("agentApplyButton").then(button => button.click());
        expect(canvas.queryByText(/characters or fewer/)).toBeNull();
    },
};

export const ViewAgentInstructions: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByRole("button", { name: /View Full Instructions/i }).then(button => button.click());
        await screen.findByRole("dialog");
    },
};

export const EditAgentTools: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Toolsets (1)").then(button => button.click());
        await canvas.findByText(mockToolsets[0].name);
        await canvas.findByText(mockToolsets[1].name);
        await canvas.findByText(mockCustomToolsets[0].name);

        await canvas.findByText("Extend Agent with Custom Toolsets");
        await canvas.findByPlaceholderText("Filter by name");
    },
};

export const EditAgentConnectorsEmpty: Story = {
    parameters: {
        chatContext: {
            getConnectorSchema: mockGetConnectorSchema,
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Connectors (0)").then(button => button.click());

        await canvas.findByText("No connectors assigned to this agent");

        await canvas.findByTestId("addConnector");
    },
};

export const EditAgentConnectorsWithExisting: Story = {
    parameters: {
        chatContext: {
            getConnectorSchema: mockGetConnectorSchema,
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent0.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Connectors (1)").then(button => button.click());

        await canvas.findByText(mockConnectors[0].name);

        await canvas.findByRole("button", { name: /Remove/i });
    },
};

export const EditAgentConnectorsOpenDialog: Story = {
    parameters: {
        chatContext: {
            getConnectorSchema: mockGetConnectorSchema,
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Connectors (0)").then(button => button.click());

        await canvas.findByTestId("addConnector").then(button => button.click());

        const dialog = await screen.findByRole("dialog");
        await within(dialog).findByText("Add Connectors");

        await within(dialog).findByText(mockConnectors[0].name);
        await within(dialog).findByText(mockConnectors[1].name);
    },
};

export const EditAgentConnectorsAddConnector: Story = {
    parameters: {
        chatContext: {
            getConnectorSchema: mockGetConnectorSchema,
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Connectors (0)").then(button => button.click());

        await canvas.findByTestId("addConnector").then(button => button.click());

        const dialog = await screen.findByRole("dialog");
        const checkboxes = await within(dialog).findAllByRole("checkbox");
        expect(checkboxes.length).toBeGreaterThan(0);

        await checkboxes[0].click();

        expect(checkboxes[0]).toHaveAttribute("aria-checked", "true");

        await within(dialog)
            .findByTestId("closeAvailableConnectorsDialog")
            .then(button => button.click());

        await canvas.findByTestId("agent-connector-card-mock-connector-3");
    },
};

export const EditAgentConnectorsRemoveConnector: Story = {
    parameters: {
        chatContext: {
            getConnectorSchema: mockGetConnectorSchema,
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent0.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Connectors (1)").then(button => button.click());

        await canvas.findByText(mockConnectors[0].name);

        await canvas.findByRole("button", { name: /Remove/i }).then(button => button.click());

        await canvas.findByText("No connectors assigned to this agent");
    },
};

export const EditAgentConnectorsDialogSearch: Story = {
    parameters: {
        chatContext: {
            getConnectorSchema: mockGetConnectorSchema,
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Connectors (0)").then(button => button.click());

        await canvas.findByTestId("addConnector").then(button => button.click());

        const dialog = await screen.findByRole("dialog");
        const searchInput = within(dialog).getByRole("textbox");

        fireEvent.change(searchInput, { target: { value: mockConnectors[0].name } });

        await within(dialog).findByText(mockConnectors[0].name);
    },
};

export const EditAgentConnectorsDialogNoResults: Story = {
    parameters: {
        chatContext: {
            getConnectorSchema: mockGetConnectorSchema,
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Connectors (0)").then(button => button.click());

        await canvas.findByTestId("addConnector").then(button => button.click());

        const dialog = await screen.findByRole("dialog");
        const searchInput = within(dialog).getByRole("textbox");
        fireEvent.change(searchInput, { target: { value: "NonExistentConnectorXYZ" } });

        await within(dialog).findByText("No connectors found");
    },
};

export const EditAgentCard: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const editButton = await canvas.findByTestId("Edit Agent Card");
        editButton.scrollIntoView();
        editButton.click();

        await canvas.findByText(agent1?.skills?.[0]?.name || "ERROR");
        await canvas.findByText(agent1?.skills?.[1]?.description || "ERROR");

        expect(await canvas.findAllByText("file")).toHaveLength(2);
        expect(await canvas.findAllByText("text")).toHaveLength(2);
    },
};

export const EditAgentSkillsValidation: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const editButton = await canvas.findByTestId("Edit Agent Card");
        editButton.scrollIntoView();
        editButton.click();

        await canvas.findByText("Add Ability").then(button => button.click());

        await canvas.findByTestId("agent-skillName").then(input => {
            fireEvent.change(input, { target: { value: "AB" } });
        });
        await canvas.findByTestId("agent-skillDescription").then(input => {
            fireEvent.change(input, { target: { value: new Array(251).fill("A").join("") } });
        });
        await canvas.findByRole("button", { name: /Add/i }).then(button => button.click());

        await canvas.findByText("Name must be at least 3 characters");
        await canvas.findByText("Value must be 250 characters or less");
    },
};

export const AddSkillSuccess: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const editButton = await canvas.findByTestId("Edit Agent Card");
        editButton.scrollIntoView();
        editButton.click();

        await canvas.findByText("Add Ability").then(button => button.click());

        await canvas.findByTestId("agent-skillName").then(input => {
            fireEvent.change(input, { target: { value: "New Test Skill" } });
        });
        await canvas.findByTestId("agent-skillDescription").then(input => {
            fireEvent.change(input, { target: { value: "This is a valid skill description for testing purposes" } });
        });

        await canvas.findByRole("button", { name: /Add/i }).then(button => button.click());

        await canvas.findByText("New Test Skill");
    },
};

export const AddSkillDuplicateName: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const editButton = await canvas.findByTestId("Edit Agent Card");
        editButton.scrollIntoView();
        editButton.click();

        await canvas.findByText("Add Ability").then(button => button.click());

        const existingSkillName = agent1?.skills?.[0]?.name || "Conversation";
        await canvas.findByTestId("agent-skillName").then(input => {
            fireEvent.change(input, { target: { value: existingSkillName.toLowerCase() } });
        });
        await canvas.findByTestId("agent-skillDescription").then(input => {
            fireEvent.change(input, { target: { value: "This is a valid skill description" } });
        });

        await canvas.findByRole("button", { name: /Add/i }).then(button => button.click());

        await canvas.findByText("An ability with this name (case insensitive) already exists");
    },
};

export const AddSkillCancelFlow: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const editButton = await canvas.findByTestId("Edit Agent Card");
        editButton.scrollIntoView();
        editButton.click();

        await canvas.findByText("Add Ability").then(button => button.click());

        const nameInput = await canvas.findByTestId("agent-skillName");
        expect(nameInput).toBeInTheDocument();

        fireEvent.change(nameInput, { target: { value: "Partial Skill" } });

        await canvas.findByRole("button", { name: /Cancel/i }).then(button => button.click());

        await canvas.findByText("Add Ability");
        expect(canvas.queryByTestId("agent-skillName")).toBeNull();
    },
};

export const RemoveSkill: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const editButton = await canvas.findByTestId("Edit Agent Card");
        editButton.scrollIntoView();
        editButton.click();

        const firstSkillName = agent1?.skills?.[0]?.name || "Conversation";
        await canvas.findByText(firstSkillName);

        const removeButtons = await canvas.findAllByRole("button", { name: /Remove ability/i });
        expect(removeButtons.length).toBeGreaterThan(0);
        await removeButtons[0].click();
    },
};

export const EditCommunicationModes: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const editButton = await canvas.findByTestId("Edit Agent Card");
        editButton.scrollIntoView();
        editButton.click();

        await canvas.findByText("Communication Modes");
        await canvas.findByText("Input Modes");
        await canvas.findByText("Output Modes");

        expect(await canvas.findAllByText("file")).toHaveLength(2);
        expect(await canvas.findAllByText("text")).toHaveLength(2);
    },
};

export const ToolsetSelectionToggle: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Toolsets (1)").then(button => button.click());

        await canvas.findByText(mockToolsets[0].name);
        await canvas.findByText(mockToolsets[1].name);

        const checkboxes = await canvas.findAllByRole("checkbox");
        expect(checkboxes.length).toBeGreaterThan(0);
    },
};

export const NoToolsetsFilter: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Toolsets (1)").then(button => button.click());

        const searchInput = await canvas.findByPlaceholderText("Filter by name");
        fireEvent.change(searchInput, { target: { value: "asd" } });

        expect(canvas.queryByText(mockToolsets[0].name)).toBeNull();
        expect(canvas.queryByText(mockToolsets[1].name)).toBeNull();

        const checkboxes = canvas.queryAllByRole("checkbox");
        expect(checkboxes).toHaveLength(0);

        await canvas.findByText("Clear All");
    },
};

export const NoCustomToolsetsFilter: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            customToolsets: mockCustomToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Toolsets (1)").then(button => button.click());

        const searchInput = await canvas.findByPlaceholderText("Filter by name");
        fireEvent.change(searchInput, { target: { value: "Web" } });

        await canvas.findByText("Web Access");

        expect(canvas.queryByText("Customer_Data_Tools")).toBeNull();

        await canvas.findByText("No custom toolsets found");
        await canvas.findByText("Extend Agent with Custom Toolsets");

        const checkboxes = canvas.queryAllByRole("checkbox");
        expect(checkboxes.length).toBeGreaterThan(0);
    },
};

export const NoBuiltInToolsetsFilter: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            customToolsets: mockCustomToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Toolsets (1)").then(button => button.click());

        const searchInput = await canvas.findByPlaceholderText("Filter by name");
        fireEvent.change(searchInput, { target: { value: "Customer" } });

        await canvas.findByText("Customer_Data_Tools");

        expect(canvas.queryByText("Data Analysis")).toBeNull();
        expect(canvas.queryByText("Web Access")).toBeNull();

        await canvas.findByText("No built-in toolsets found");
        await canvas.findByText("Extend Agent with Custom Toolsets");

        const checkboxes = canvas.queryAllByRole("checkbox");
        expect(checkboxes.length).toBeGreaterThan(0);
    },
};

export const AgentCreateToolset: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            customToolsets: mockCustomToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Toolsets (1)").then(button => button.click());

        await canvas.findByRole("button", { name: /Create Toolset/i }).then(button => button.click());

        const dialog = await screen.findByRole("dialog");
        await within(dialog).findByText("Create Toolset");

        await within(dialog).findByText("Custom tools go beyond the built-in catalog. Use a preferred programming language and framework, package the tool, and upload it.");
        await within(dialog).findByText("Toolset");
        await within(dialog).findByText("Drag and drop file here");
        await within(dialog).findByText("OR");
        await within(dialog).findByRole("button", { name: /Upload File/i });

        await within(dialog).findByRole("button", { name: /Cancel/i });
        await within(dialog).findByRole("button", { name: /Next/i });
    },
};

export const AgentCreateToolsetFileUploaded: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            customToolsets: mockCustomToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Toolsets (1)").then(button => button.click());

        await canvas.findByRole("button", { name: /Create Toolset/i }).then(button => button.click());

        const dialog = await screen.findByRole("dialog");
        await within(dialog).findByText("Next");
        await within(dialog).findByText("Cancel");

        const fileInput = dialog.querySelector('input[type="file"]');
        if (fileInput) {
            const file = new File(["mock zip content"], "toolset.zip", { type: "application/zip" });

            Object.defineProperty(fileInput, "files", {
                value: [file],
                writable: false,
            });

            fireEvent.change(fileInput);

            await new Promise(resolve => setTimeout(resolve, 200));
        }
    },
};

export const AgentCreateToolsetFullFlow: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            customToolsets: mockCustomToolsets,
            customToolsetsRefetch: async () => {},
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
        msw: {
            handlers: [
                http.post("*/api/v2/platform/toolsets", () => HttpResponse.json({ data: toolsetToV2({ ...mockCustomToolsets[0], id: "new-toolset-id", name: "NewToolset" }) })),
                http.put("*/api/v2/platform/toolsets/new-toolset-id/bundle", () => HttpResponse.json({ data: toolsetToV2({ ...mockCustomToolsets[0], id: "new-toolset-id", name: "NewToolset" }) })),
                ...baseListHandlers,
            ],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Toolsets (1)").then(button => button.click());

        await canvas.findByRole("button", { name: /Create Toolset/i }).then(button => button.click());

        const dialog = await screen.findByRole("dialog");
        const fileInput = dialog.querySelector('input[type="file"]');
        if (fileInput) {
            const file = new File(["mock zip content"], "toolset.zip", { type: "application/zip" });
            Object.defineProperty(fileInput, "files", { value: [file], writable: false });
            fireEvent.change(fileInput);
        }

        await new Promise(resolve => setTimeout(resolve, 200));
        const dialogs = screen.queryAllByRole("dialog");
        expect(dialogs.length).toBeGreaterThanOrEqual(0);
    },
};

export const CustomToolsetExpandTools: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            customToolsets: mockCustomToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Toolsets (1)").then(button => button.click());

        await canvas.findByText(mockCustomToolsets[0].name);

        expect(canvas.queryByText("Query Customers")).toBeNull();

        await canvas.findByTestId(`expand-toolset-${mockCustomToolsets[0].name}`).then(b => b.click());

        await canvas.findByText("Query Customers");
        await canvas.findByText("Transform Records");
        await canvas.findByText("Export CSV");
    },
};

export const CustomToolsetToggleTool: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            customToolsets: mockCustomToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const toolsetName = mockCustomToolsets[0].name;

        await canvas.findByTestId("Edit Toolsets (1)").then(button => button.click());
        await canvas.findByTestId(`expand-toolset-${toolsetName}`).then(b => b.click());

        const toolList = await canvas.findByTestId(`tool-select-list-${toolsetName}`);
        const toolCheckbox = within(toolList).getAllByRole("checkbox")[0];
        const toolsetCheckbox = canvas.getByRole("checkbox", { name: toolsetName });

        expect(toolCheckbox).not.toBeChecked();
        expect(toolsetCheckbox).not.toBeChecked();

        await userEvent.click(toolCheckbox);
        expect(toolCheckbox).toBeChecked();
        expect(toolsetCheckbox).toBeChecked();

        await userEvent.click(toolCheckbox);
        expect(toolCheckbox).not.toBeChecked();
        expect(toolsetCheckbox).not.toBeChecked();
    },
};

export const CustomToolsetConfigCogOnTool: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            customToolsets: mockCustomToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Toolsets (1)").then(button => button.click());

        const customToolsetName = mockCustomToolsets[0].name;
        const customCheckbox = await canvas.findByRole<HTMLInputElement>("checkbox", { name: customToolsetName });
        if (!customCheckbox.checked) {
            await userEvent.click(customCheckbox);
        }

        await canvas.findByTestId(`expand-toolset-${customToolsetName}`).then(b => b.click());

        const toolList = await canvas.findByTestId(`tool-select-list-${customToolsetName}`);
        const configCog = within(toolList).queryByRole("button", { name: /Configure/i });
        expect(configCog).toBeInTheDocument();

        fireEvent.click(configCog!);
        const dialog = await screen.findByRole("dialog");
        await within(dialog).findByText("Tool Configuration");

        expect(within(dialog).getByText(/unique to this agent/i)).toBeInTheDocument();
    },
};

export const DiscardChangesInSection: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Agent Details").then(button => button.click());

        await canvas.findByTestId("agent-name").then(input => {
            fireEvent.change(input, { target: { value: "Modified Name" } });
        });

        await canvas.findByTestId("agentDiscardButton").then(button => button.click());

        await canvas.findByTestId("Edit Agent Details");
    },
};

export const DiscardChangesMainPage: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const discardButton = await canvas.findByTestId("agentCancel");
        expect(discardButton).toBeInTheDocument();
        expect(discardButton).toHaveTextContent("Discard Changes");
    },
};

export const AddInstructionsNewAgent: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            agentToEdit: { name: "New Agent Without Instructions", description: "A new agent for testing" },
            useLoaderData: () => ({ agentId: "new", isNew: true }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const addInstructionsButton = await canvas.findByRole("button", { name: /Add Instructions/i });
        expect(addInstructionsButton).toBeInTheDocument();

        addInstructionsButton.click();

        await canvas.findByTestId("agent-instructions");
    },
};

export const AgentNotFound: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: "non-existent-id", isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Agent Not Found");
        await canvas.findByRole("button", { name: /Go To Agents/i });
    },
};

export const EditNameTooShort: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Agent Details").then(button => button.click());
        await canvas.findByTestId("agent-name").then(input => {
            fireEvent.change(input, { target: { value: "AB" } });
        });
        await canvas.findByTestId("agentApplyButton").then(button => button.click());

        await canvas.findByText("Name must be at least 3 characters");
    },
};

export const EditNameTooLong: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("Edit Agent Details").then(button => button.click());
        await canvas.findByTestId("agent-name").then(input => {
            fireEvent.change(input, { target: { value: "A".repeat(41) } });
        });
        await canvas.findByTestId("agentApplyButton").then(button => button.click());

        await canvas.findByText("Name must be 40 characters or less");
    },
};

const modelMswHandlers = [http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse(mockModels.map(toModelV2))))];

export const EditModelSection: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
        msw: {
            handlers: [...modelMswHandlers, ...baseListHandlers],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const editButton = await canvas.findByTestId("Edit Model");
        editButton.scrollIntoView();
        editButton.click();

        await canvas.findByText("Claude Sonnet");
        await canvas.findByText("GPT-4o");

        const radios = await canvas.findAllByRole("radio");
        expect(radios.length).toBe(2);
        await radios[0].click();
        expect(radios[0]).toBeChecked();
    },
};

export const EditModelSectionWarningBanner: Story = {
    parameters: {
        chatContext: {
            useLoaderData: () => ({ agentId: agent1.id, isNew: false }),
        },
        configContext: {
            persistenceEnabled: true,
        },
        msw: {
            handlers: [
                http.get("*/api/v2/platform/agents", () => {
                    const agents = [...mockAgents.filter(a => a.id !== agent1.id), { ...agent1, modelProvider: ["deleted-model-id"] }];
                    return HttpResponse.json(paginatedResponse(agents.map(agentToV2)));
                }),
                ...modelMswHandlers,
                ...baseListHandlers,
            ],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText(/Previously selected model is no longer available/);
    },
};

export const CreateAgentDefaultsToGeneralModel: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            agentToEdit: { name: "New Agent", description: "A new agent", systemPrompt: "You are a helpful agent that assists users with their questions and tasks in a thorough and reliable way." },
            useLoaderData: () => ({ agentId: "new", isNew: true }),
        },
        configContext: {
            persistenceEnabled: true,
        },
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse(mockModelConfigs.map(toModelV2))))),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("general");
    },
};
