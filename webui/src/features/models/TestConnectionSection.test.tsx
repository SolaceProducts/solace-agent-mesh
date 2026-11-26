// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import type { ModelFormData } from "./modelProviderUtils";
import { TestConnectionSection } from "./TestConnectionSection";

const FORM: ModelFormData = { alias: "a", description: "d", provider: "openai", modelName: "gpt-4o", authType: "apikey", apiKey: "sk-test" };

describe("TestConnectionSection", () => {
    afterEach(() => {
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
    });

    const stubProbe = (result: { success: boolean; message: string }) => {
        const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ data: { ...result, errorClass: null } }), { headers: { "Content-Type": "application/json" } }));
        vi.stubGlobal("fetch", fetchMock);
        return fetchMock;
    };

    async function runProbe() {
        renderWithProviders(<TestConnectionSection getFormData={() => FORM} isNew />);
        await userEvent.click(screen.getByTestId("test-connection-button"));
        return screen.findByTestId("messageBanner");
    }

    test("a passed probe with an empty message still reads as a success, through v2", async () => {
        const fetchMock = stubProbe({ success: true, message: "" });

        expect(await runProbe()).toHaveTextContent("Connection test successful");
        expect(fetchMock.mock.calls.every(call => String(call[0]).includes("/api/v2/platform"))).toBe(true);
    });

    test("a failed probe shows the server's sentence, not the success copy", async () => {
        stubProbe({ success: false, message: "The provider rejected the credentials." });

        const banner = await runProbe();
        expect(banner).toHaveTextContent("The provider rejected the credentials.");
        expect(banner).not.toHaveTextContent("Connection test successful");
    });

    test("a failed probe with no message does not render a blank banner", async () => {
        stubProbe({ success: false, message: "" });

        expect((await runProbe()).textContent?.trim()).not.toBe("");
    });

    test("a non-Error rejection falls back to the generic sentence", async () => {
        vi.stubGlobal("fetch", vi.fn().mockRejectedValue("offline"));

        expect(await runProbe()).toHaveTextContent("An unknown error occurred while testing the connection.");
    });
});
