// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fireEvent, screen, userEvent, waitFor, within } from "storybook/test";

import { GatewayEditPage } from "@/features/entrypoints/GatewayEditPage";

import { mockGateways, mockGatewaySchemas } from "@/testing/data";
import { savedEntrypointV2, succeededDeploymentV2, toEntrypointV2 } from "@/testing/data/entrypointsV2";
import { withDefaults } from "@/testing/helpers";

const whatsappGateway = {
    ...mockGateways[0],
    id: "gateway-wa-1",
    name: "WhatsApp Entrypoint",
    type: "whatsapp",
    values: {
        phone_number_id: "1234567890",
        access_token: "<REDACTED>",
        app_secret: "<REDACTED>",
        verify_token: "<REDACTED>",
        default_agent_name: "Manager",
    },
};

const handlersNew = withDefaults(http.post("*/api/v2/platform/entrypoints", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-wa-new", await request.json()) })));

const handlersEdit = withDefaults(http.get("*/api/v2/platform/entrypoints/gateway-wa-1", () => HttpResponse.json({ data: toEntrypointV2(whatsappGateway) })));

const newGatewayChatContext = {
    useLoaderData: () => {
        const hash = window.location.hash;
        if (!hash.includes("?type=whatsapp")) {
            window.location.hash = hash.includes("?") ? hash + "&type=whatsapp" : hash + "?type=whatsapp";
        }
        return { isNew: true };
    },
    gatewaySchemas: mockGatewaySchemas,
    navigate: (path: string) => {
        console.log(`Navigate to: ${path}`);
    },
    addNotification: (message: string) => {
        console.log(`Notification: ${message}`);
    },
};

const meta = {
    title: "Entrypoints/GatewayEditPage/WhatsApp",
    component: GatewayEditPage,
    parameters: {
        docs: {
            description: {
                component: "The gateway edit page for creating and editing WhatsApp entrypoint configurations.",
            },
        },
    },
} satisfies Meta<typeof GatewayEditPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const CreateNewWhatsAppGateway: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newGatewayChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("WhatsApp");

        expect(canvas.getAllByText("Create Entrypoint").length).toBeGreaterThan(0);

        expect(canvas.getByText("Phone Number ID")).toBeInTheDocument();
        expect(canvas.getByText("Access Token")).toBeInTheDocument();
        expect(canvas.getByText("App Secret")).toBeInTheDocument();
        expect(canvas.getByText("Webhook Verify Token")).toBeInTheDocument();
        expect(canvas.getByText("Assigned Agent")).toBeInTheDocument();
        expect(canvas.queryByText("Max Media Size (MB)")).not.toBeInTheDocument();

        expect(canvas.getByText(/The ID Meta assigns to a WhatsApp number/i)).toBeInTheDocument();
        expect(canvas.getByText(/Generate a permanent token in Meta Business Settings/i)).toBeInTheDocument();
        expect(canvas.queryByText(/Leave placeholder to keep the existing value/i)).not.toBeInTheDocument();

        await waitFor(() => {
            expect(canvas.queryByTestId("whatsapp-gateway-setup-banner")).toBeInTheDocument();
        });
        expect(canvas.getByText(/A webhook URL will be generated after creation/i)).toBeInTheDocument();
        const setupDocsLink = within(canvas.getByTestId("whatsapp-gateway-setup-banner")).getByRole("link", { name: /Learn more/i });
        expect(setupDocsLink.getAttribute("href")).toMatch(/\/building\/entrypoints\/whatsapp\/index\.htm$/);

        expect(canvas.getByRole("button", { name: "Create" })).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: "Create and Deploy" })).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: "Discard Changes" })).toBeInTheDocument();
    },
};

export const PrerequisitesWarningIsShownOnCreate: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newGatewayChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("WhatsApp");

        const prerequisites = await canvas.findByTestId("entrypointPrerequisitesWarning");
        expect(prerequisites).toHaveTextContent(/Requires a WhatsApp Business Account/i);
        expect(prerequisites).toHaveTextContent(/Cloud API phone number/i);
        const docsLink = within(prerequisites).getByRole("link", { name: /Learn more about WhatsApp prerequisites/i });
        expect(docsLink).toHaveAttribute("target", "_blank");
        expect(docsLink.getAttribute("href")).toMatch(/\/building\/entrypoints\/whatsapp\/index\.htm#prerequisites$/);

        expect(prerequisites).not.toHaveAttribute("role", "note");
        const configHeading = canvas.getByText("Entrypoint Configuration");
        expect(configHeading.compareDocumentPosition(prerequisites) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

        const phoneLabel = canvas.getByText("Phone Number ID");
        expect(prerequisites.compareDocumentPosition(phoneLabel) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    },
};

export const IdentityFieldsHaveTheirOwnSection: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newGatewayChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const identityHeading = await canvas.findByText("Identity Configuration");

        const declineFiles = canvas.getByText("Decline Inbound Files");
        expect(declineFiles.compareDocumentPosition(identityHeading) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        for (const label of ["Sender Identity", "Identify Senders By", "Identity Provider Claim", "Contact Request Template", "Contact Request Template Language"]) {
            expect(identityHeading.compareDocumentPosition(canvas.getByText(label)) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        }

        const findTemplate = canvas.getByTestId("openWhatsappTemplateLookup");
        const templateInput = findTemplate.parentElement?.querySelector("input");
        expect(templateInput).toBeTruthy();
        const buttonBox = findTemplate.getBoundingClientRect();
        const inputBox = templateInput!.getBoundingClientRect();
        expect(buttonBox.left).toBeGreaterThanOrEqual(inputBox.right);
        expect(buttonBox.top).toBeLessThan(inputBox.bottom);
        expect(buttonBox.bottom).toBeGreaterThan(inputBox.top);

        await userEvent.click(findTemplate);
        expect(await screen.findByText("Find a Contact Request Template")).toBeInTheDocument();
    },
};

const textBox = (node: Node): DOMRect => {
    const range = document.createRange();
    range.selectNodeContents(node);
    return range.getBoundingClientRect();
};

const PIXEL_TOLERANCE = 1.5;

export const SectionTitlesAreEvenlySpaced: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newGatewayChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const identityTitle = await canvas.findByText("Identity Configuration");
        const entrypointTitle = canvas.getByText("Entrypoint Configuration");

        for (const [title, firstLine] of [
            [entrypointTitle, canvas.getByTestId("entrypointPrerequisitesWarning").firstChild!],
            [identityTitle, canvas.getByText("Sender Identity")],
        ]) {
            expect(Math.abs(textBox(firstLine).top - textBox(title).bottom - 24)).toBeLessThanOrEqual(PIXEL_TOLERANCE);
        }

        const descriptionBox = canvasElement.querySelector('textarea[name="description"]') as HTMLElement;
        const declineFilesHint = canvas.getByText(/inbound images, documents, videos, and voice messages are declined/i);
        const gapBelowDescription = textBox(entrypointTitle).top - descriptionBox.getBoundingClientRect().bottom;
        const gapBelowConnectionFields = textBox(identityTitle).top - textBox(declineFilesHint).bottom;
        expect(Math.abs(gapBelowConnectionFields - gapBelowDescription)).toBeLessThanOrEqual(PIXEL_TOLERANCE);
    },
};

export const CheckboxHintAlignsWithCheckbox: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newGatewayChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const hint = await canvas.findByText(/inbound images, documents, videos, and voice messages are declined/i);
        const checkbox = canvas.getByText("Decline Inbound Files").parentElement?.querySelector('input[type="checkbox"]');
        expect(checkbox).toBeTruthy();
        expect(textBox(hint).left).toBe(checkbox!.getBoundingClientRect().left);
    },
};

export const PrerequisitesWarningIsShownOnEdit: Story = {
    parameters: {
        msw: { handlers: handlersEdit },
        chatContext: {
            useLoaderData: () => {
                return { gatewayId: "gateway-wa-1", isNew: false };
            },
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                console.log(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                console.log(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByDisplayValue("WhatsApp Entrypoint");

        const prerequisites = await canvas.findByTestId("entrypointPrerequisitesWarning");
        expect(prerequisites).toHaveTextContent(/Requires a WhatsApp Business Account/i);
    },
};

export const RequiredErrorsIncludeGuidance: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newGatewayChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("WhatsApp");

        await userEvent.click(canvas.getByRole("button", { name: "Create" }));

        expect(await canvas.findByText("Phone Number ID is required. Find it in WhatsApp Manager after verifying the number.")).toBeInTheDocument();
        expect(canvas.getByText("Access Token is required. Generate a permanent token in Meta Business Settings under System Users.")).toBeInTheDocument();
        expect(canvas.getByText("App Secret is required. Find it in the Meta app under Settings > Basic > App Secret.")).toBeInTheDocument();
        expect(canvas.getByText("Webhook Verify Token is required. Create and enter it in both this form and Meta's webhook settings to confirm the connection.")).toBeInTheDocument();

        expect(canvas.queryByText("The ID Meta assigns to a WhatsApp number. Find it in WhatsApp Manager after verifying the number.")).not.toBeInTheDocument();
        expect(canvas.queryByText("Authenticates outbound messages sent on the business's behalf. Generate a permanent token in Meta Business Settings under System Users.")).not.toBeInTheDocument();
    },
};

const fillCreateForm = (canvasElement: HTMLElement) => {
    const fill = (selector: string, value: string) => {
        const input = canvasElement.querySelector(selector) as HTMLElement | null;
        expect(input, selector).not.toBeNull();
        fireEvent.change(input!, { target: { value } });
    };
    fill('input[name="name"]', "My WhatsApp Entrypoint");
    fill('textarea[name="description"], input[name="description"]', "WhatsApp entrypoint for customer conversations");
    fill('input[name="values.phone_number_id"]', "1234567890");
    fill('input[name="values.access_token"]', "EAAtoken");
    fill('input[name="values.app_secret"]', "appsecret");
    fill('input[name="values.verify_token"]', "verifytoken");
};

let lastCreateBody: Record<string, unknown> | null = null;
let lastPatchBody: Record<string, unknown> | null = null;
let lastDeployBody: Record<string, unknown> | null = null;

const deployHandler = http.post("*/api/v2/platform/entrypointDeployments", async ({ request }) => {
    lastDeployBody = (await request.json()) as Record<string, unknown>;
    return HttpResponse.json({ data: succeededDeploymentV2(lastDeployBody) });
});

export const CreateAndDeployShowsDialog: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.post("*/api/v2/platform/entrypoints", async ({ request }) => {
                    lastCreateBody = (await request.json()) as Record<string, unknown>;
                    return HttpResponse.json({ data: savedEntrypointV2("gateway-wa-new", lastCreateBody) });
                }),
                deployHandler
            ),
        },
        chatContext: newGatewayChatContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        lastDeployBody = null;
        const canvas = within(canvasElement);
        await canvas.findByText("WhatsApp");
        fillCreateForm(canvasElement);

        await userEvent.click(canvas.getByRole("button", { name: "Create and Deploy" }));

        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText("Deploy My WhatsApp Entrypoint")).toBeInTheDocument();
        expect(within(dialog).getByText(/the webhook URL must be pointed to Meta/i)).toBeInTheDocument();
        expect(lastCreateBody).toBeNull();

        await userEvent.click(within(dialog).getByTestId("dialogConfirmButton"));
        await waitFor(() => {
            expect(lastDeployBody).toEqual({ entrypointId: "gateway-wa-new", action: "deploy" });
        });
        expect(lastCreateBody).not.toBeNull();
        await waitFor(() => {
            expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
        });
    },
};

export const CreateAndDeployCancelKeepsForm: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.post("*/api/v2/platform/entrypoints", async ({ request }) => {
                    lastCreateBody = (await request.json()) as Record<string, unknown>;
                    return HttpResponse.json({ data: savedEntrypointV2("gateway-wa-new", lastCreateBody) });
                })
            ),
        },
        chatContext: newGatewayChatContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);
        await canvas.findByText("WhatsApp");
        fillCreateForm(canvasElement);

        await userEvent.click(canvas.getByRole("button", { name: "Create and Deploy" }));
        const dialog = await screen.findByRole("dialog");

        await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
        await waitFor(() => {
            expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
        });

        expect(lastCreateBody).toBeNull();
        expect(canvas.getByDisplayValue("My WhatsApp Entrypoint")).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: "Create and Deploy" })).toBeInTheDocument();
    },
};

export const SaveAndDeployShowsUpdateDialog: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/entrypoints/gateway-wa-1", () => HttpResponse.json({ data: toEntrypointV2({ ...whatsappGateway, deploymentStatus: "deployed" }) })),
                http.patch("*/api/v2/platform/entrypoints/gateway-wa-1", async ({ request }) => {
                    lastPatchBody = (await request.json()) as Record<string, unknown>;
                    return HttpResponse.json({ data: savedEntrypointV2("gateway-wa-1", lastPatchBody, { ...whatsappGateway, deploymentStatus: "deployed" }) });
                }),
                deployHandler
            ),
        },
        chatContext: {
            useLoaderData: () => {
                return { gatewayId: "gateway-wa-1", isNew: false };
            },
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                console.log(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                console.log(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        lastPatchBody = null;
        lastDeployBody = null;
        const canvas = within(canvasElement);
        await canvas.findByDisplayValue("WhatsApp Entrypoint");

        await userEvent.click(canvas.getByRole("button", { name: "Save and Deploy" }));

        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText("Deploy Updates for WhatsApp Entrypoint")).toBeInTheDocument();

        await userEvent.click(within(dialog).getByTestId("dialogConfirmButton"));
        await waitFor(() => {
            expect(lastDeployBody).toEqual({ entrypointId: "gateway-wa-1", action: "update" });
        });
        expect(lastPatchBody).not.toBeNull();
    },
};

export const EditExistingWhatsAppGateway: Story = {
    parameters: {
        msw: { handlers: handlersEdit },
        chatContext: {
            useLoaderData: () => {
                return { gatewayId: "gateway-wa-1", isNew: false };
            },
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                console.log(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                console.log(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByDisplayValue("WhatsApp Entrypoint");

        await waitFor(() => {
            expect(canvas.getAllByText(/Leave placeholder to keep the existing value/i)).toHaveLength(3);
        });

        expect(canvas.queryByTestId("whatsapp-gateway-setup-banner")).not.toBeInTheDocument();

        expect(canvas.getByRole("button", { name: "Save" })).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: "Save and Deploy" })).toBeInTheDocument();
    },
};
