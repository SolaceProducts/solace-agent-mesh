// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { GatewayEditPage } from "@/features/entrypoints/GatewayEditPage";

import { makeAgentCard, mockGatewaySchemas } from "@/testing/data";
import { savedEntrypointV2, toEntrypointV2 } from "@/testing/data/entrypointsV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

let lastCreateBody: Record<string, unknown> | null = null;

const asBody = (body: unknown) => (typeof body === "object" && body !== null ? (body as Record<string, unknown>) : null);

const agentCardsHandler = http.get("*/api/v1/agentCards", () => HttpResponse.json(paginatedResponse([makeAgentCard({ name: "Orchestrator" }), makeAgentCard({ name: "OrderIntake", isWorkflow: true })])));

const handlersNew = withDefaults(
    agentCardsHandler,
    http.post("*/api/v2/platform/entrypoints", async ({ request }) => {
        lastCreateBody = asBody(await request.json());
        return HttpResponse.json({ data: savedEntrypointV2("gateway-new", lastCreateBody) });
    })
);

const UNDEPLOYED_WORKFLOW = "RetiredWorkflow";

const storedWebhookGateway = (targetWorkflowName: string) => ({
    id: "gateway-webhook",
    name: "Order webhook",
    description: "Accepts inbound order events.",
    type: "webhook",
    values: {
        routePath: "/orders",
        method: "POST",
        authMode: "token",
        authToken: "dev-webhook-token",
        targetType: "workflow",
        targetWorkflowName,
        inputExpression: "input.payload:",
    },
});

const storedGatewayHandler = (targetWorkflowName: string) => http.get("*/api/v2/platform/entrypoints/gateway-webhook", () => HttpResponse.json({ data: toEntrypointV2(storedWebhookGateway(targetWorkflowName)) }));

const legacyStoredGatewayHandler = http.get("*/api/v2/platform/entrypoints/gateway-webhook", () => {
    const gateway = storedWebhookGateway("OrderIntake");
    const { targetType: _absent, ...valuesWithoutDiscriminator } = gateway.values;
    return HttpResponse.json({ data: toEntrypointV2({ ...gateway, values: valuesWithoutDiscriminator }) });
});

const patchGatewayHandler = http.patch("*/api/v2/platform/entrypoints/gateway-webhook", async ({ request }) => {
    lastCreateBody = asBody(await request.json());
    return HttpResponse.json({ data: savedEntrypointV2("gateway-webhook", lastCreateBody, storedWebhookGateway("OrderIntake")) });
});

const handlersEditUndeployedWorkflow = withDefaults(agentCardsHandler, storedGatewayHandler(UNDEPLOYED_WORKFLOW), patchGatewayHandler);

const handlersEditLegacyBag = withDefaults(agentCardsHandler, legacyStoredGatewayHandler, patchGatewayHandler);

const handlersEditWorkflowsUnavailable = withDefaults(
    http.get("*/api/v1/agentCards", () => new HttpResponse(null, { status: 500 })),
    storedGatewayHandler("OrderIntake")
);

const editWebhookContext = {
    useLoaderData: () => ({ gatewayId: "gateway-webhook", isNew: false }),
    gatewaySchemas: mockGatewaySchemas,
    navigate: () => {},
    addNotification: () => {},
};

const newWebhookContext = {
    useLoaderData: () => {
        const hash = window.location.hash;
        if (!hash.includes("?type=webhook")) {
            window.location.hash = hash.includes("?") ? hash + "&type=webhook" : hash + "?type=webhook";
        }
        return { isNew: true };
    },
    gatewaySchemas: mockGatewaySchemas,
    navigate: () => {},
    addNotification: () => {},
};

const meta = {
    title: "Entrypoints/GatewayEditPage/Webhook",
    component: GatewayEditPage,
} satisfies Meta<typeof GatewayEditPage>;

export default meta;
type Story = StoryObj<typeof meta>;

const agentHint = "Agent that handles inbound requests.";
const workflowHint = "Workflow that handles inbound requests.";

const fillTextFields = async (canvasElement: HTMLElement) => {
    const field = (name: string) => canvasElement.querySelector(`[name="${name}"]`) as HTMLElement;

    await userEvent.type(field("name"), "Order webhook");
    await userEvent.type(field("description"), "Accepts inbound order events.");
    await userEvent.type(field("values.routePath"), "/orders");
    await userEvent.type(field("values.authToken"), "dev-webhook-token");
    await userEvent.type(field("values.inputExpression"), "input.payload:");
};

const TARGET_TYPE_SELECT = 0;
const TARGET_SELECT = 1;

const targetRow = (canvas: ReturnType<typeof within>) => within(canvas.getByTestId("fieldRow-target"));

const chooseOption = async (canvas: ReturnType<typeof within>, selectIndex: number, option: RegExp | string) => {
    await userEvent.click(targetRow(canvas).getAllByRole("combobox")[selectIndex]);
    await userEvent.click(await screen.findByRole("option", { name: option }));
};

export const ShowsOnlyTheSelectedTarget: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newWebhookContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Target");
        expect(targetRow(canvas).getByText(agentHint)).toBeInTheDocument();
        expect(canvas.queryByText(workflowHint)).not.toBeInTheDocument();

        await chooseOption(canvas, TARGET_TYPE_SELECT, "Workflow");

        expect(await targetRow(canvas).findByText(workflowHint)).toBeInTheDocument();
        expect(canvas.queryByText(agentHint)).not.toBeInTheDocument();
    },
};

export const SwitchingTargetClearsTheOtherBranch: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newWebhookContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);

        await canvas.findByText("Target");
        await fillTextFields(canvasElement);

        await chooseOption(canvas, TARGET_SELECT, "Orchestrator");
        await chooseOption(canvas, TARGET_TYPE_SELECT, "Workflow");
        await chooseOption(canvas, TARGET_SELECT, "OrderIntake");

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        await waitFor(() => expect(lastCreateBody).not.toBeNull());
        expect(lastCreateBody).toMatchObject({
            values: { targetType: "workflow", targetWorkflowName: "OrderIntake", targetAgentName: "" },
        });
    },
};

export const RequiresTheSelectedTarget: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newWebhookContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);

        await canvas.findByText("Target");
        await fillTextFields(canvasElement);

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        expect(await canvas.findByText("Agent is required")).toBeInTheDocument();
        expect(lastCreateBody).toBeNull();
    },
};

export const KeepsAnUndeployedWorkflowTarget: Story = {
    parameters: {
        msw: { handlers: handlersEditUndeployedWorkflow },
        chatContext: editWebhookContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);

        await canvas.findByText("Target");
        expect(await targetRow(canvas).findByText(workflowHint)).toBeInTheDocument();
        expect(targetRow(canvas).getAllByRole("combobox")[TARGET_SELECT]).toHaveTextContent(UNDEPLOYED_WORKFLOW);

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        await waitFor(() => expect(lastCreateBody).not.toBeNull());
        expect(lastCreateBody).toMatchObject({
            values: { targetType: "workflow", targetWorkflowName: UNDEPLOYED_WORKFLOW },
        });
    },
};

export const NeedsTheServerToDeriveTheDiscriminator: Story = {
    parameters: {
        msw: { handlers: handlersEditLegacyBag },
        chatContext: editWebhookContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);

        await canvas.findByText("Target");
        expect(await targetRow(canvas).findByText(agentHint)).toBeInTheDocument();
        expect(canvas.queryByText(workflowHint)).not.toBeInTheDocument();

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        expect(await canvas.findByText("Agent is required")).toBeInTheDocument();
        expect(lastCreateBody).toBeNull();
    },
};

export const ExplainsAWorkflowListThatFailedToLoad: Story = {
    parameters: {
        msw: { handlers: handlersEditWorkflowsUnavailable },
        chatContext: editWebhookContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Target");
        expect(await canvas.findByText(/couldn't load workflows/i)).toBeInTheDocument();
        expect(targetRow(canvas).getAllByRole("combobox")[TARGET_SELECT]).toHaveTextContent("OrderIntake");
    },
};
