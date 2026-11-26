// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { useLoaderData } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import { ApiV2Error } from "@/api/common/v2";
import { gatewaysApi } from "@/api/gateways";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Gateway, GatewaySchema } from "@/types";
import { DOC_LINKS } from "@/utils";

import { GatewayEditPage } from "./GatewayEditPage";

expect.extend(matchers);

vi.mock("@/api/capabilities", async importActual => ({
    ...(await importActual<typeof import("@/api/capabilities")>()),
    useCapabilities: () => ({ data: [] }),
    findGatewayBadge: () => null,
}));

vi.mock("@/common/dialogs", async importOriginal => ({
    ...(await importOriginal<typeof import("@/common/dialogs")>()),
    useNavigationBlocker: () => ({ NavigationBlocker: () => null, allowNavigation: (fn: () => void) => fn(), setBlockingEnabled: () => {} }),
}));

vi.mock("./hooks", async importActual => ({
    ...(await importActual<typeof import("./hooks")>()),
    useAgentOptions: () => [{ name: "orchestrator-broker", displayName: "Orchestrator", deploymentStatus: "deployed" }],
}));

const schemas: GatewaySchema[] = [{ type: "google_chat", name: "Google Chat", description: "", schema: {}, yamlTemplatePath: "", requiredEnvVars: [] }];

const stored: Gateway = {
    id: "gw-1",
    slug: "hr-assistant",
    name: "HR Assistant",
    description: "Answers HR questions in Google Chat.",
    type: "google_chat",
    values: {
        delivery: "https",
        endpoint_url: "",
        addon_service_account_email: "service-123456789@gcp-sa-gsuiteaddons.iam.gserviceaccount.com",
        service_account_key: "<REDACTED>",
        default_agent_name: "orchestrator-broker",
        thread_history_limit: 0,
    },
};

const renderPage = (isNew: boolean, canDeploy = false, stubGet = true, endpointBase = "https://sam.example.com") => {
    const held = new Set<string>([CAPABILITIES.updateGateways, CAPABILITIES.createGateways, ...(canDeploy ? [CAPABILITIES.deployGateways] : [])]);
    if (stubGet) vi.spyOn(gatewaysApi, "get").mockResolvedValue(stored);
    const path = isNew ? "/entrypoints/new?type=google_chat" : "/entrypoints/gw-1/edit";
    window.location.hash = `#${path}`;
    renderWithProviders(<GatewayEditPage />, {
        initialPath: path,
        chatContextValues: {
            gatewaySchemas: schemas,
            useLoaderData: (() => ({ gatewayId: isNew ? undefined : "gw-1", isNew })) as unknown as typeof useLoaderData,
        },
        configContextValues: { googleChatEndpointBase: endpointBase },
        userContextValues: { userHasCapability: (capability: Capability) => held.has(capability) },
    });
};

const renderEditPage = () => renderPage(false);

describe("GatewayEditPage for Google Chat", () => {
    afterEach(() => vi.restoreAllMocks());

    test("a rejected save lands on the form field, not in an error dialog", async () => {
        const save = vi.spyOn(gatewaysApi, "save").mockRejectedValue(new ApiV2Error(422, "Invalid entrypoint", { validationDetails: { "values.thread_history_limit": ["is too large"] } }));
        renderEditPage();

        const field = await screen.findByTestId("field-values.thread_history_limit");
        fireEvent.change(within(field).getByRole("spinbutton"), { target: { value: "5" } });
        await userEvent.click(screen.getByTestId("saveGatewayButton"));

        await expect(within(field).findByText("is too large")).resolves.toBeInTheDocument();
        expect(save).toHaveBeenCalledWith("gw-1", expect.objectContaining({ values: { thread_history_limit: 5 } }));
        expect(screen.queryByText("Entrypoint Save Failed")).not.toBeInTheDocument();
    });

    test("a save rejected after confirming Save and Deploy lands on the form field, not in an error dialog", async () => {
        const save = vi.spyOn(gatewaysApi, "save").mockRejectedValue(new ApiV2Error(422, "Invalid entrypoint", { validationDetails: { "values.thread_history_limit": ["is too large"] } }));
        renderPage(false, true);

        const field = await screen.findByTestId("field-values.thread_history_limit");
        fireEvent.change(within(field).getByRole("spinbutton"), { target: { value: "5" } });
        await userEvent.click(screen.getByTestId("saveAndDeployGatewayButton"));
        await userEvent.click(await screen.findByTestId("dialogConfirmButton"));

        await expect(within(field).findByText("is too large")).resolves.toBeInTheDocument();
        expect(save).toHaveBeenCalledWith("gw-1", expect.objectContaining({ deploy: true, values: { thread_history_limit: 5 } }));
        expect(screen.queryByText("Entrypoint Save Failed")).not.toBeInTheDocument();
    });

    test("Save and Deploy on an entrypoint with a stored endpoint URL shows that URL in the deploy dialog", async () => {
        vi.spyOn(gatewaysApi, "get").mockResolvedValue({ ...stored, deploymentStatus: "not_deployed", values: { ...stored.values, endpoint_url: "https://tunnel.example.com/gw/hr-assistant/events" } });
        renderPage(false, true, false);

        const field = await screen.findByTestId("field-values.thread_history_limit");
        fireEvent.change(within(field).getByRole("spinbutton"), { target: { value: "5" } });
        await userEvent.click(screen.getByTestId("saveAndDeployGatewayButton"));

        expect(await screen.findByText("https://tunnel.example.com/gw/hr-assistant/events")).toBeInTheDocument();
    });

    test("closing the deploy dialog saves nothing, and a plain Save afterwards still reaches the field", async () => {
        const save = vi.spyOn(gatewaysApi, "save").mockRejectedValue(new ApiV2Error(422, "Invalid entrypoint", { validationDetails: { "values.thread_history_limit": ["is too large"] } }));
        renderPage(false, true);

        const field = await screen.findByTestId("field-values.thread_history_limit");
        fireEvent.change(within(field).getByRole("spinbutton"), { target: { value: "5" } });
        await userEvent.click(screen.getByTestId("saveAndDeployGatewayButton"));
        await userEvent.click(await screen.findByRole("button", { name: "Close" }));
        expect(save).not.toHaveBeenCalled();

        await userEvent.click(screen.getByTestId("saveGatewayButton"));
        await expect(within(field).findByText("is too large")).resolves.toBeInTheDocument();
        expect(save).toHaveBeenCalledWith("gw-1", expect.objectContaining({ deploy: false }));
    });

    test.each([
        { base: "http://localhost:8000", disabled: true },
        { base: "https://sam.example.com", disabled: false },
    ])("Create is disabled while the external address $base is not https: $disabled", async ({ base, disabled }) => {
        renderPage(true, true, true, base);
        await screen.findByLabelText(/Name/);
        for (const id of ["saveGatewayButton", "createAndDeployGatewayButton"]) {
            if (disabled) expect(screen.getByTestId(id)).toBeDisabled();
            else expect(screen.getByTestId(id)).toBeEnabled();
        }
    });

    test.each([
        { mode: "create", isNew: true, shown: true },
        { mode: "edit", isNew: false, shown: false },
    ])("the setup banner shows on $mode: $shown", async ({ isNew, shown }) => {
        renderPage(isNew);
        await screen.findByLabelText(/Name/);
        const line = screen.queryByText("A webhook URL will be generated after creation.");
        if (!shown) {
            expect(line).not.toBeInTheDocument();
            return;
        }
        expect(line).toBeInTheDocument();
        expect(screen.getByRole("link", { name: /Learn more/ })).toHaveAttribute("href", DOC_LINKS.buildingEntrypointsGoogleChat);
    });
});
