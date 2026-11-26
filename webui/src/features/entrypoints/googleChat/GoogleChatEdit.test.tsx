// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";

import { gatewaysApi } from "@/api/gateways";

import { NO_BROWSER_FILL } from "@/forms";
import { generatedServiceAccountKey } from "@/forms/testing/generatedKey";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Gateway, GatewaySchema } from "@/types";
import { DOC_LINKS } from "@/utils";

import { GoogleChatEdit } from "./GoogleChatEdit";

expect.extend(matchers);

vi.mock("../hooks", async importActual => ({
    ...(await importActual<typeof import("../hooks")>()),
    useAgentOptions: () => [{ name: "orchestrator-broker", displayName: "Orchestrator", deploymentStatus: "deployed" }],
}));

const schemaInfo: GatewaySchema = { type: "google_chat", name: "Google Chat", description: "", schema: {}, yamlTemplatePath: "", requiredEnvVars: [], warning: "Requires a Google Workspace Chat app and a service account for it." };

const storedGateway: Gateway = {
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
        allowed_email_domains: ["example.com"],
        use_user_identity_in_channels: false,
        default_agent_name: "orchestrator-broker",
        thread_history_limit: 0,
        artifact_drive_folder_id: "",
        feedback_enabled: true,
        run_as: "svc",
    },
};

const renderEdit = (initial: Gateway = storedGateway) => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    renderWithProviders(<GoogleChatEdit schemaInfo={schemaInfo} initialGatewayToEdit={initial} isNew={false} onSubmit={onSubmit} footer={<button type="submit">Save</button>} />, {
        configContextValues: { googleChatEndpointBase: "https://sam.example.com" },
    });
    return onSubmit;
};

const renderCreate = (endpointBase = "https://sam.example.com") => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    renderWithProviders(<GoogleChatEdit schemaInfo={schemaInfo} isNew onSubmit={onSubmit} footer={<button type="submit">Save</button>} />, {
        configContextValues: { googleChatEndpointBase: endpointBase },
    });
    return onSubmit;
};

const box = (testId: string) => within(screen.getByTestId(testId)).getByRole("textbox");

async function fillCreate(slug: string) {
    await userEvent.type(within(await screen.findByTestId("field-name")).getByRole("textbox"), "HR Bot");
    await userEvent.type(box("field-description"), "Answers HR questions.");
    await userEvent.clear(box("field-slug"));
    await userEvent.type(box("field-slug"), slug);
    await userEvent.type(box("field-values.addon_service_account_email"), "service-123456789@gcp-sa-gsuiteaddons.iam.gserviceaccount.com");
    const key = new File([await generatedServiceAccountKey()], "key.json", { type: "application/json" });
    fireEvent.drop(screen.getByTestId("credential-drop-zone"), { dataTransfer: { files: [key] } });
    await screen.findByRole("button", { name: /Replace Service account key|Remove Service account key/ });
}

describe("GoogleChatEdit", () => {
    afterEach(() => vi.restoreAllMocks());

    test("edit save keeps hidden fields by sending only changed values", async () => {
        const onSubmit = renderEdit();

        const threadHistory = within(await screen.findByTestId("field-values.thread_history_limit")).getByRole("spinbutton");
        fireEvent.change(threadHistory, { target: { value: "5" } });
        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
        expect(onSubmit.mock.calls[0][0].values).toStrictEqual({ thread_history_limit: 5 });
    });

    test("an edit shows the saved key as a card that can be replaced, and keeping it sends no key", async () => {
        const onSubmit = renderEdit();
        const keyField = await screen.findByTestId("field-values.service_account_key");
        expect(within(keyField).getByText("A key is saved")).toBeInTheDocument();
        expect(within(keyField).queryByTestId("credential-drop-zone")).not.toBeInTheDocument();

        await userEvent.click(within(keyField).getByRole("button", { name: "Replace Service account key" }));
        expect(within(keyField).getByTestId("credential-drop-zone")).toBeInTheDocument();
        await userEvent.click(within(keyField).getByRole("button", { name: "Keep the saved key" }));
        expect(within(keyField).getByText("A key is saved")).toBeInTheDocument();

        const threadHistory = within(screen.getByTestId("field-values.thread_history_limit")).getByRole("spinbutton");
        fireEvent.change(threadHistory, { target: { value: "5" } });
        await userEvent.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
        expect(onSubmit.mock.calls[0][0].values).not.toHaveProperty("service_account_key");
    });

    test("keeping the saved key after dropping a file that is not a key sends no key", async () => {
        const onSubmit = renderEdit();
        const keyField = await screen.findByTestId("field-values.service_account_key");
        await userEvent.click(within(keyField).getByRole("button", { name: "Replace Service account key" }));
        const notAKey = new File(["not a key"], "notes.txt", { type: "text/plain" });
        fireEvent.drop(within(keyField).getByTestId("credential-drop-zone"), { dataTransfer: { files: [notAKey] } });
        await userEvent.click(await within(keyField).findByRole("button", { name: "Keep the saved key" }));
        expect(within(keyField).getByText("A key is saved")).toBeInTheDocument();

        const threadHistory = within(screen.getByTestId("field-values.thread_history_limit")).getByRole("spinbutton");
        fireEvent.change(threadHistory, { target: { value: "5" } });
        await userEvent.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
        expect(onSubmit.mock.calls[0][0].values).toStrictEqual({ thread_history_limit: 5 });
    });

    test("an edit reports unsaved changes once a field changes", async () => {
        const onDirtyStateChange = vi.fn();
        renderWithProviders(<GoogleChatEdit schemaInfo={schemaInfo} initialGatewayToEdit={storedGateway} isNew={false} onSubmit={vi.fn()} onDirtyStateChange={onDirtyStateChange} footer={null} />, {
            configContextValues: { googleChatEndpointBase: "https://sam.example.com" },
        });
        const threadHistory = within(await screen.findByTestId("field-values.thread_history_limit")).getByRole("spinbutton");
        expect(onDirtyStateChange).not.toHaveBeenCalledWith(true);

        fireEvent.change(threadHistory, { target: { value: "5" } });

        await waitFor(() => expect(onDirtyStateChange).toHaveBeenLastCalledWith(true));
    });

    test("an edit save leaves the endpoint URL unsent", async () => {
        const onSubmit = renderEdit({ ...storedGateway, values: { ...storedGateway.values, endpoint_url: "https://tunnel.example.com/gw/hr-assistant/events" } });
        const threadHistory = within(await screen.findByTestId("field-values.thread_history_limit")).getByRole("spinbutton");
        fireEvent.change(threadHistory, { target: { value: "5" } });
        await userEvent.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
        expect(onSubmit.mock.calls[0][0].values).not.toHaveProperty("endpoint_url");
    });

    test("an edit shows the entrypoint's address read-only", async () => {
        renderEdit();
        expect(await screen.findByText("https://sam.example.com/gw/hr-assistant")).toBeInTheDocument();
        expect(screen.queryByTestId("field-values.endpoint_url")).not.toBeInTheDocument();
    });

    test("on create the URL endpoint starts as google-chat after the entrypoint address", async () => {
        renderCreate();
        expect(await screen.findByText("https://sam.example.com/gw/")).toBeInTheDocument();
        expect(box("field-slug")).toHaveValue("google-chat");
    });

    test("on create the name leaves the URL endpoint alone", async () => {
        renderCreate();
        await userEvent.type(within(await screen.findByTestId("field-name")).getByRole("textbox"), "HR Bot");
        expect(box("field-slug")).toHaveValue("google-chat");
    });

    test("create refuses a URL endpoint another entrypoint already uses, on the field and without a request", async () => {
        const getAll = vi.spyOn(gatewaysApi, "getAll").mockResolvedValue([{ ...storedGateway, id: "gw-2", slug: "google-chat", name: "Existing" }]);
        const onSubmit = renderCreate();
        await waitFor(() => expect(getAll).toHaveBeenCalled());
        await fillCreate("google-chat");
        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        expect(await within(screen.getByTestId("field-slug")).findByText("Another entrypoint already uses this URL endpoint. Choose a different one.")).toBeInTheDocument();
        expect(onSubmit).not.toHaveBeenCalled();
    });

    test("an edit may keep its own name", async () => {
        vi.spyOn(gatewaysApi, "getAll").mockResolvedValue([storedGateway]);
        const onSubmit = renderEdit();
        const threadHistory = within(await screen.findByTestId("field-values.thread_history_limit")).getByRole("spinbutton");
        fireEvent.change(threadHistory, { target: { value: "5" } });
        await userEvent.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    });

    test.each(["", "http://localhost:8000"])("create with external address %j says why it can't be created", async base => {
        renderCreate(base);
        expect(await screen.findByText(/WEBUI_FRONTEND_SERVER_URL/)).toBeInTheDocument();
    });

    test("create with an https external address shows no such notice", async () => {
        renderCreate("https://sam.example.com");
        await screen.findByTestId("field-name");
        expect(screen.queryByText(/WEBUI_FRONTEND_SERVER_URL/)).not.toBeInTheDocument();
    });

    test("create leaves endpoint_url empty so the server derives it from the current external address", async () => {
        const onSubmit = renderCreate("https://sam.example.com");
        await fillCreate("hr-bot");
        await userEvent.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
        expect(onSubmit.mock.calls[0][0].values.endpoint_url).toBe("");
    });

    test("the add-on service account and allowed domains are not offered the browser's saved values", async () => {
        renderEdit();
        expect(await screen.findByRole("textbox", { name: /Add-on service account/ })).toHaveAttribute("autocomplete", NO_BROWSER_FILL);
        expect(screen.getByRole("textbox", { name: /Allowed email domains/ })).toHaveAttribute("autocomplete", NO_BROWSER_FILL);
    });

    test("shows the entrypoint type", async () => {
        renderEdit();
        expect(await screen.findByTestId("entrypointType")).toHaveTextContent("Google Chat");
    });

    test("links to the Google Chat prerequisites", async () => {
        renderEdit();
        const link = await screen.findByRole("link", { name: /Learn about Google Chat prerequisites/ });
        expect(link).toHaveAttribute("href", DOC_LINKS.buildingEntrypointsGoogleChatPrerequisites);
    });

    test("an emptied thread history shows its error instead of saving 0", async () => {
        const onSubmit = renderEdit();

        const field = await screen.findByTestId("field-values.thread_history_limit");
        fireEvent.change(within(field).getByRole("spinbutton"), { target: { value: "" } });
        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        await expect(within(field).findByText("Enter a whole number from 0 to 100")).resolves.toBeInTheDocument();
        expect(onSubmit).not.toHaveBeenCalled();
    });
});
