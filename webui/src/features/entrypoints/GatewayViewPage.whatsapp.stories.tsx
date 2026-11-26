// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, userEvent, within } from "storybook/test";

import { GatewayViewPage } from "@/features/entrypoints/GatewayViewPage";

import { createAgent, mockGateways, mockGatewaySchemas } from "@/testing/data";
import { toEntrypointV2, toNetworkingDetailsV2 } from "@/testing/data/entrypointsV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

const orchestratorAgent = createAgent("0324234-23cd322", { name: "Orchestrator Agent", deploymentStatus: "deployed" });

const orchestratorAgentV2 = {
    id: orchestratorAgent.id,
    agentType: "standard",
    name: orchestratorAgent.name,
    instanceName: orchestratorAgent.instanceName,
    description: orchestratorAgent.description,
    systemPrompt: "",
    toolsets: [],
    toolsetExcludedTools: null,
    toolsetConfigs: [],
    skills: [],
    skillRefs: [],
    skillIds: [],
    skillConfigs: [],
    inputModes: [],
    outputModes: [],
    connectorIds: [],
    modelProvider: [],
    additionalConfigurations: null,
    deploymentStatus: "deployed",
    syncStatus: null,
    deployed: null,
    runtimeStatus: { status: "stopped", problems: [] },
    toolRegistrations: [],
    isEphemeral: false,
    sourceSessionId: null,
    sourceComponentName: null,
    sourceComponentKind: null,
    expiresTime: null,
    createdBy: "Test User",
    updatedBy: "Test User",
    createdTime: null,
    updatedTime: null,
};

const whatsappGateway = {
    ...mockGateways[0],
    id: "gateway-wa-1",
    name: "WhatsApp Entrypoint",
    description: "WhatsApp entrypoint for customer conversations",
    type: "whatsapp",
    values: {
        phone_number_id: "000000000000000",
        access_token: "<REDACTED>",
        app_secret: "<REDACTED>",
        verify_token: "<REDACTED>",
        default_agent_name: orchestratorAgent.instanceName,
    },
};

const handlers = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-wa-1", () => HttpResponse.json({ data: toEntrypointV2(whatsappGateway) })),
    http.get("*/api/v2/platform/entrypoints/gateway-wa-1/networkingDetails", () =>
        HttpResponse.json({
            data: toNetworkingDetailsV2({
                dnsName: "",
                port: 0,
                url: "https://wa.example.com/gw/gateway-wa-1",
                webhookUrl: "https://wa.example.com/gw/gateway-wa-1/webhook",
                apiPaths: [{ path: "/webhook", description: "WhatsApp Business Cloud API webhook endpoint (GET verify + POST messages)" }],
            }),
        })
    ),
    http.get("*/api/v2/platform/entrypointDeployments", () => HttpResponse.json(paginatedResponse([]))),
    http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse([orchestratorAgentV2])))
);

const meta = {
    title: "Entrypoints/GatewayViewPage/WhatsApp",
    component: GatewayViewPage,
    parameters: {
        docs: {
            description: {
                component: "The gateway view page for a WhatsApp entrypoint: configuration details with resolved agent display name, and the Network tab.",
            },
        },
        msw: { handlers },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-wa-1" }),
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                console.log(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                console.log(`Notification: ${message}`);
            },
            gatewayDeployError: null,
            setGatewayDeployError: () => {},
            deployGateway: async () => {},
        },
    },
} satisfies Meta<typeof GatewayViewPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const ConfigurationAndNetwork: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const nameElements = await canvas.findAllByText("WhatsApp Entrypoint");
        expect(nameElements[0]).toBeInTheDocument();

        expect(await canvas.findByText("Orchestrator Agent")).toBeInTheDocument();
        expect(canvas.queryByText(orchestratorAgent.instanceName!)).not.toBeInTheDocument();

        await userEvent.click(canvas.getByRole("tab", { name: "Network" }));
        expect(await canvas.findByText("Entrypoint Endpoint")).toBeInTheDocument();
        expect(canvas.getAllByText("https://wa.example.com/gw/gateway-wa-1/webhook").length).toBeGreaterThan(0);
        expect(canvas.getByRole("link", { name: /Learn more about WhatsApp configuration/i })).toBeInTheDocument();
        expect(canvas.getByText("API Paths")).toBeInTheDocument();
    },
};
