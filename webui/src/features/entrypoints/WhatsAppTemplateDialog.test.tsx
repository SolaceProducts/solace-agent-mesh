// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { gatewaysApi } from "@/api/gateways";

import type { WhatsAppMessageTemplate } from "@/types";

import { WhatsAppTemplateDialog } from "./WhatsAppTemplateDialog";

const realLookup = gatewaysApi.getWhatsAppMessageTemplates;
const mockLookup = vi.spyOn(gatewaysApi, "getWhatsAppMessageTemplates");

const usable: WhatsAppMessageTemplate = {
    name: "share_contact_request",
    language: "en_US",
    status: "APPROVED",
    body: "Tap the button below to share your number.",
    usable: true,
    blockers: [],
};

const withVariable: WhatsAppMessageTemplate = {
    name: "request_contact_info_3",
    language: "en_US",
    status: "APPROVED",
    body: "Hi {{1}}, having your phone number helps us serve you faster.",
    usable: false,
    blockers: ["has_variables"],
};

const noButton: WhatsAppMessageTemplate = {
    name: "greetings",
    language: "en",
    status: "APPROVED",
    body: "Hello, it is really nice to meet you.",
    usable: false,
    blockers: ["no_contact_button"],
};

function renderDialog(onSelect = vi.fn(), gatewayId?: string) {
    const onOpenChange = vi.fn();
    render(<WhatsAppTemplateDialog open onOpenChange={onOpenChange} values={{ phone_number_id: "555111", access_token: "tok" }} gatewayId={gatewayId} onSelect={onSelect} />);
    return { onSelect, onOpenChange };
}

async function lookUp(id = "123456789") {
    await userEvent.type(screen.getByTestId("whatsappBusinessAccountId"), id);
    await userEvent.click(screen.getByTestId("lookUpWhatsappTemplates"));
}

describe("WhatsAppTemplateDialog", () => {
    beforeEach(() => vi.clearAllMocks());

    test("lists usable and unusable templates together", async () => {
        mockLookup.mockResolvedValue({ data: [usable, withVariable, noButton] });
        renderDialog();
        await lookUp();

        expect(await screen.findByTestId("whatsappTemplateRow-share_contact_request")).toBeInTheDocument();
        expect(screen.getByTestId("whatsappTemplateRow-request_contact_info_3")).toBeInTheDocument();
        expect(screen.getByTestId("whatsappTemplateRow-greetings")).toBeInTheDocument();

        expect(screen.getByTestId("whatsappTemplateRow-share_contact_request")).toHaveAttribute("role", "button");
        expect(screen.getByTestId("whatsappTemplateRow-request_contact_info_3")).toHaveAttribute("aria-disabled", "true");
        expect(screen.getByTestId("whatsappTemplateRow-greetings")).not.toHaveAttribute("role", "button");
    });

    test("clicking a row reports its name and language, then closes", async () => {
        mockLookup.mockResolvedValue({ data: [usable] });
        const { onSelect, onOpenChange } = renderDialog();
        await lookUp();

        await userEvent.click(await screen.findByTestId("whatsappTemplateRow-share_contact_request"));

        expect(onSelect).toHaveBeenCalledWith("share_contact_request", "en_US");
        expect(onOpenChange).toHaveBeenCalledWith(false);
    });

    test("a usable row is selectable with the keyboard", async () => {
        mockLookup.mockResolvedValue({ data: [usable] });
        const { onSelect } = renderDialog();
        await lookUp();

        const row = await screen.findByTestId("whatsappTemplateRow-share_contact_request");
        row.focus();
        await userEvent.keyboard("{Enter}");
        expect(onSelect).toHaveBeenCalledWith("share_contact_request", "en_US");
    });

    test("clicking an unusable row selects nothing", async () => {
        mockLookup.mockResolvedValue({ data: [withVariable] });
        const { onSelect, onOpenChange } = renderDialog();
        await lookUp();

        await userEvent.click(await screen.findByTestId("whatsappTemplateRow-request_contact_info_3"));
        expect(onSelect).not.toHaveBeenCalled();
        expect(onOpenChange).not.toHaveBeenCalled();
    });

    test("an unusable template explains itself on hover", async () => {
        mockLookup.mockResolvedValue({ data: [withVariable] });
        renderDialog();
        await lookUp();

        await userEvent.hover(await screen.findByTestId("whatsappTemplateBlocked-request_contact_info_3"));
        const tip = await screen.findByRole("tooltip");
        expect(within(tip).getByText(/Expects a parameter/i)).toBeInTheDocument();
    });

    test("the content cell truncates and shows the whole body on hover", async () => {
        const multiline: WhatsAppMessageTemplate = {
            ...usable,
            body: "I can't tell who you are yet.\n\nTap the button below to share your number.\n\nThen send your request again.",
        };
        mockLookup.mockResolvedValue({ data: [multiline] });
        renderDialog();
        await lookUp();

        const cell = await screen.findByTestId("whatsappTemplateBody-share_contact_request");
        expect(cell.textContent ?? "").toHaveLength(76);
        expect(cell).not.toHaveTextContent(/Then send your request again/);
        expect(cell).not.toHaveTextContent(/\n/);

        await userEvent.hover(cell);
        const tip = await screen.findByRole("tooltip");
        expect(tip).toHaveTextContent(/Then send your request again/);
    });

    test("a body under the cap is shown whole, with no ellipsis", async () => {
        mockLookup.mockResolvedValue({ data: [usable] });
        renderDialog();
        await lookUp();

        const cell = await screen.findByTestId("whatsappTemplateBody-share_contact_request");
        expect(cell).toHaveTextContent(usable.body);
        expect(cell.textContent ?? "").not.toContain("…");
    });

    test("the account id reaches the platform, never the access token", async () => {
        mockLookup.mockResolvedValue({ data: [usable] });
        renderDialog(vi.fn(), "gw-1");
        await lookUp("999888777");

        expect(mockLookup).toHaveBeenCalledWith({
            businessAccountId: "999888777",
            values: { phone_number_id: "555111", access_token: "tok" },
            gatewayId: "gw-1",
        });
    });

    test("a thrown error is shown rather than swallowed", async () => {
        mockLookup.mockRejectedValue(new Error("platform unreachable"));
        renderDialog();
        await lookUp();

        expect(await screen.findByTestId("whatsappTemplateLookupMessage")).toHaveTextContent("platform unreachable");
    });

    test("an account with no templates says so instead of looking broken", async () => {
        mockLookup.mockResolvedValue({ data: [] });
        renderDialog();
        await lookUp();

        expect(await screen.findByText(/no message templates yet/i)).toBeInTheDocument();
    });

    test("says when the list is cut short", async () => {
        mockLookup.mockResolvedValue({ data: [usable], hasMore: true });
        renderDialog();
        await lookUp();

        expect(await screen.findByText(/more templates than are shown/i)).toBeInTheDocument();
    });

    test("does not call the platform without an account id", async () => {
        renderDialog();
        await userEvent.click(screen.getByTestId("lookUpWhatsappTemplates"));
        expect(mockLookup).not.toHaveBeenCalled();
    });
});

describe("WhatsAppTemplateDialog on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const respond = (status: number, body: unknown) => fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(body), { status }));

    beforeEach(() => {
        mockLookup.mockImplementation(realLookup);
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("looks up through the v2 action and lists what comes back", async () => {
        respond(200, { data: { templates: [{ ...usable, status: "approved" }], hasMore: true } });
        renderDialog(vi.fn(), "gw-1");
        await lookUp("999888777");

        expect(await screen.findByTestId("whatsappTemplateRow-share_contact_request")).toBeInTheDocument();
        expect(screen.getByText(/more templates than are shown/i)).toBeInTheDocument();
        expect(fetchMock.mock.calls[0][0]).toBe("/api/v2/platform/entrypoints/whatsapp/messageTemplates");
        expect(fetchMock.mock.calls[0][1]?.method).toBe("PUT");
        expect(JSON.parse(fetchMock.mock.calls[0][1]?.body as string)).toEqual({ businessAccountId: "999888777", values: { phone_number_id: "555111", access_token: "tok" }, entrypointId: "gw-1" });
    });

    test("shows the reason from a failed lookup", async () => {
        respond(422, { message: "Meta rejected the access token.", errorId: "e1" });
        renderDialog();
        await lookUp();

        expect(await screen.findByTestId("whatsappTemplateLookupMessage")).toHaveTextContent("Meta rejected the access token.");
        expect(screen.queryByRole("table")).not.toBeInTheDocument();
    });
});
