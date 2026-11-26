// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, userEvent, waitFor, within } from "storybook/test";

import { GatewayEditPage } from "@/features/entrypoints/GatewayEditPage";

import { generatedServiceAccountKey } from "@/forms/testing/generatedKey";
import { mockGatewaySchemas } from "@/testing/data";
import { savedEntrypointV2 } from "@/testing/data/entrypointsV2";
import { withDefaults } from "@/testing/helpers";

let lastCreateBody: Record<string, unknown> | null = null;

const handlersNew = withDefaults(
    http.post("*/api/v2/platform/entrypoints", async ({ request }) => {
        const body = await request.json();
        lastCreateBody = typeof body === "object" && body !== null ? (body as Record<string, unknown>) : null;
        return HttpResponse.json({ data: savedEntrypointV2("gateway-new", lastCreateBody) });
    })
);

const newGoogleChatContext = {
    useLoaderData: () => {
        const hash = window.location.hash;
        if (!hash.includes("?type=google_chat")) {
            window.location.hash = hash.includes("?") ? hash + "&type=google_chat" : hash + "?type=google_chat";
        }
        return { isNew: true };
    },
    gatewaySchemas: mockGatewaySchemas,
    navigate: () => {},
    addNotification: () => {},
};

const meta = {
    title: "Entrypoints/GatewayEditPage/GoogleChat",
    component: GatewayEditPage,
} satisfies Meta<typeof GatewayEditPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Create: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newGoogleChatContext,
        configContext: { googleChatEndpointBase: "https://sam.example.com" },
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);
        const input = async (testId: string) => within(await canvas.findByTestId(testId)).getByRole("textbox");

        await userEvent.type(await input("field-name"), "HR Assistant");
        await userEvent.type(await input("field-description"), "Answers HR questions in Google Chat.");
        await userEvent.clear(await input("field-slug"));
        await userEvent.type(await input("field-slug"), "hr-assistant");
        await userEvent.type(await input("field-values.addon_service_account_email"), "service-123456789@gcp-sa-gsuiteaddons.iam.gserviceaccount.com");
        const fileInput = canvasElement.querySelector<HTMLInputElement>('[data-testid="credential-drop-zone"] input[type="file"]');
        await userEvent.upload(fileInput!, new File([await generatedServiceAccountKey()], "key.json", { type: "application/json" }));
        await canvas.findByText("key.json");

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        await waitFor(() => expect(lastCreateBody).not.toBeNull());
        expect(lastCreateBody).toMatchObject({ entrypointType: "google_chat", slug: "hr-assistant", values: { delivery: "https", endpoint_url: "", service_account_key: expect.stringContaining("service_account") } });
    },
};

export const CreateWithoutHttpsAddress: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newGoogleChatContext,
        configContext: { googleChatEndpointBase: "" },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(await canvas.findByText(/WEBUI_FRONTEND_SERVER_URL/)).toBeInTheDocument();
        await expect(canvas.getByTestId("saveGatewayButton")).toBeDisabled();
    },
};
