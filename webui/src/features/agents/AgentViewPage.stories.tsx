// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, within } from "storybook/test";

import { transformAgent } from "@/api/agents";

import { AgentViewPage } from "@/features/agents/AgentViewPage";

import { createDiscoveredAgentCard, getAgentCardById, mockAgents, mockAgentsWithDuplicates, mockBuiltinSkills, mockConnectors, mockCustomSkills, mockGetConnectorSchema, mockModels, mockToolsets } from "@/testing/data";
import { toModelV2 } from "@/testing/data/modelsV2";
import { agentToV2, connectorToV2, skillToV2, toolsetToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";
import type { Agent, AgentCardInfo } from "@/types";

const makeDiscoveredCard = (id: string): AgentCardInfo => {
    const card = createDiscoveredAgentCard(id);
    return {
        ...card,
        capabilities: {
            ...card.capabilities,
            extensions: [...(card.capabilities?.extensions ?? []), { uri: "https://solace.com/a2a/extensions/display-name", params: { display_name: card.displayName ?? card.name } }],
        },
    };
};

const makeListHandlers = (agents: Agent[] = mockAgents, yamlAgentCards: AgentCardInfo[] = []) =>
    withDefaults(
        http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(agents.map(agentToV2)))),
        http.get("*/api/v2/platform/connectors", () => HttpResponse.json(paginatedResponse(mockConnectors.map(connectorToV2)))),
        http.get("*/api/v2/platform/toolsets", ({ request }) => {
            const type = new URL(request.url).searchParams.get("origin");
            const list = type === "builtin" ? mockToolsets : [];
            return HttpResponse.json(paginatedResponse(list.map(toolsetToV2)));
        }),
        http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: yamlAgentCards }))
    );

const connectorsListHandlers = makeListHandlers();

const meta = {
    title: "Agents/AgentViewPage",
    component: AgentViewPage,
    parameters: {
        docs: {
            description: {
                component: "The main agent page component that displays the agent's details and handles user interactions.",
            },
        },
        msw: { handlers: connectorsListHandlers },
    },
    decorators: [
        Story => (
            <OpenFeatureTestProvider>
                <Story />
            </OpenFeatureTestProvider>
        ),
    ],
} satisfies Meta<typeof AgentViewPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: "10" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("deployAgentButton");
        await canvas.findByTestId("editAgentButton");
        const notDeployedElements = await canvas.findAllByText(/Not Deployed/);
        expect(notDeployedElements.length).toBeGreaterThan(0);
        expect(canvas.queryByTestId("deploymentStatusInfo")).toBeNull();
    },
};

export const DuplicateNames: Story = {
    parameters: {
        msw: { handlers: makeListHandlers(mockAgentsWithDuplicates) },
        chatContext: {
            getConnectorSchema: mockGetConnectorSchema,
            yamlAgentCards: mockAgentsWithDuplicates.filter(agent => agent.deploymentStatus === "deployed").map(agent => transformAgent(agent, mockToolsets, mockConnectors)),
            useLoaderData: () => ({ agentId: "18" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("deployAgentButton");
        await canvas.findByTestId("editAgentButton");
        expect(canvas.queryByTestId("deploymentStatusInfo")).toBeNull();
        await canvas.findByTestId("duplicate-name-warning-banner");
    },
};

export const ViewDeployedActive: Story = {
    parameters: {
        msw: { handlers: makeListHandlers(mockAgents, [makeDiscoveredCard("0")]) },
        chatContext: {
            getConnectorSchema: mockGetConnectorSchema,
            useLoaderData: () => ({ agentId: "0" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    render: () => <AgentViewPage />,

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("editAgentButton");
        const runningElements = await canvas.findAllByText(/Running/);
        expect(runningElements.length).toBeGreaterThan(0);
        expect(canvas.queryByText("Disconnected")).toBeNull();
    },
};

export const ViewDeploy: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: "10" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    render: () => <AgentViewPage />,

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        (await canvas.findByTestId("deployAgentButton")).click();
        const dialog = await screen.findByRole("dialog");
        expect(await within(dialog).findByRole("heading", { name: /Deploy Agent/i })).toBeInTheDocument();
        expect(within(dialog).queryByText(/Deploy Updates/)).not.toBeInTheDocument();
    },
};

export const ViewOutOfSync: Story = {
    parameters: {
        msw: { handlers: makeListHandlers(mockAgents, [makeDiscoveredCard("3")]) },
        chatContext: {
            useLoaderData: () => ({ agentId: "3" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    render: () => <AgentViewPage />,

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("deployAgentButton");
        await canvas.findByTestId("editAgentButton");
        await canvas.findByTestId("viewFullInstructionsButton");
        const runningElements = await canvas.findAllByText(/Running/);
        expect(runningElements.length).toBeGreaterThan(0);
        const infoElements = await canvas.findAllByTestId("deploymentStatusInfo");
        expect(infoElements.length).toBeGreaterThan(0);
    },
};

export const ViewOutOfSyncDeploy: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            userAgentCards: [getAgentCardById("3")!],
            yamlAgentCards: [createDiscoveredAgentCard("3")],
            remoteAgentCards: [],
            useLoaderData: () => ({ agentId: "3" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    render: () => <AgentViewPage />,

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        (await canvas.findByTestId("deployAgentButton")).click();
        const dialog = await screen.findByRole("dialog");
        expect(await within(dialog).findByRole("heading", { name: /Deploy Updates/i })).toBeInTheDocument();
    },
};

export const ViewDeployFailed: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: "6" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    render: () => <AgentViewPage />,

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployAgentButton");
        expect(deployButton).toHaveTextContent("Redeploy");
        await canvas.findByTestId("editAgentButton");
        const failedElements = await canvas.findAllByText(/Deployment Failed/);
        expect(failedElements.length).toBeGreaterThan(0);
        expect(canvas.queryByTestId("deploymentStatusInfo")).toBeNull();
    },
};

export const ViewDeployFailedOutOfSync: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: "7" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    render: () => <AgentViewPage />,

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployAgentButton");
        expect(deployButton).toHaveTextContent("Redeploy");
        await canvas.findByTestId("editAgentButton");
        const failedElements = await canvas.findAllByText(/Deployment Failed/);
        expect(failedElements.length).toBeGreaterThan(0);
        const infoElements = await canvas.findAllByTestId("deploymentStatusInfo");
        expect(infoElements.length).toBeGreaterThan(0);
    },
};

export const ViewDisconnectedDeploy: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: "1" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    render: () => <AgentViewPage />,

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployAgentButton");
        expect(deployButton).toHaveTextContent("Deploy");
        deployButton.click();
        const dialog = await screen.findByRole("dialog");
        expect(await within(dialog).findByRole("heading", { name: /Deploy Agent/i })).toBeInTheDocument();
    },
};

export const ViewDisconnectedOutOfSync: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: "4" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    render: () => <AgentViewPage />,

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployAgentButton");
        expect(deployButton).toHaveTextContent("Deploy Updates");
        const notRunningElements = await canvas.findAllByText(/Not Running/);
        expect(notRunningElements.length).toBeGreaterThan(0);
    },
};

export const ViewUndeployed: Story = {
    parameters: {
        chatContext: {
            toolsets: mockToolsets,
            userAgents: mockAgents,
            useLoaderData: () => ({ agentId: "11" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    render: () => <AgentViewPage />,

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("deployAgentButton");
        await canvas.findByTestId("editAgentButton");
        const notDeployedElements = await canvas.findAllByText(/Not Deployed/);
        expect(notDeployedElements.length).toBeGreaterThan(0);
        expect(canvas.queryByTestId("deploymentStatusInfo")).toBeNull();
    },
};

const modelMswHandlers = [http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse(mockModels.map(toModelV2))))];

const agentWithModel = {
    ...mockAgents.find(a => a.id === "10")!,
    modelProvider: ["model-1"],
};

const agentWithDeletedModel = {
    ...mockAgents.find(a => a.id === "10")!,
    modelProvider: ["deleted-model-id"],
};

export const ViewWithModel: Story = {
    parameters: {
        chatContext: {
            useLoaderData: () => ({ agentId: "10" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
        msw: {
            handlers: [...modelMswHandlers, ...makeListHandlers([...mockAgents.filter(a => a.id !== "10"), agentWithModel])],
        },
    },
    render: () => <AgentViewPage />,

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Claude Sonnet");
        await canvas.findByText("claude-3-5-sonnet");
    },
};

export const ViewModelUnavailable: Story = {
    parameters: {
        chatContext: {
            useLoaderData: () => ({ agentId: "10" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
        msw: {
            handlers: [...modelMswHandlers, ...makeListHandlers([...mockAgents.filter(a => a.id !== "10"), agentWithDeletedModel])],
        },
    },
    render: () => <AgentViewPage />,

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText(/Previously selected model is no longer available/);
    },
};

const customSkill = mockCustomSkills[0];
const builtinSkill = mockBuiltinSkills[0];
const abilities = [
    { name: "Multi-Agent Orchestration", description: "Coordinates work across peer agents." },
    { name: "Document Generation", description: "Creates Word, PowerPoint, and PDF documents." },
];
const agentWithSkills: Agent = {
    ...mockAgents.find(a => a.id === "10")!,
    skillIds: [customSkill.id],
    skillRefs: [builtinSkill.name],
    skills: abilities,
};

const skillsHandlers = [
    http.get("*/api/v2/platform/skills", ({ request }) => {
        const type = new URL(request.url).searchParams.get("origin");
        const list = type === "builtin" ? mockBuiltinSkills : mockCustomSkills;
        return HttpResponse.json(paginatedResponse(list.map(skillToV2)));
    }),
];

export const ViewWithSkills: Story = {
    parameters: {
        chatContext: {
            useLoaderData: () => ({ agentId: "10" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
        msw: {
            handlers: [...skillsHandlers, ...makeListHandlers([...mockAgents.filter(a => a.id !== "10"), agentWithSkills])],
        },
    },
    render: () => <AgentViewPage />,

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText(/Skills \(2\)/);
        await canvas.findByText(customSkill.name);
        await canvas.findByText(customSkill.description);
        await canvas.findByText(builtinSkill.label!);
        await canvas.findByText(builtinSkill.description);

        await canvas.findByText("Abilities");
        for (const ability of abilities) {
            await canvas.findByText(ability.name);
            await canvas.findByText(ability.description);
        }
    },
};
