// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { mockConnectorSchemas } from "@/testing/data";
import { getPaginatedMeta } from "@/testing/helpers";

import exaLogo from "./assets/exa.svg";
import tavilyLogo from "./assets/tavily.svg";
import meta from "./ConnectorsAvailableDialog.stories";

const webSearchCapability = { key: "web_search_connector", name: "Web Search Connector", description: "", status: "early_access", platform: "linux", hasEnvOverride: false };

const server = setupServer(
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/config/capabilities", () => HttpResponse.json({ data: [webSearchCapability], meta: getPaginatedMeta(1) })),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({})),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] }))
);

const handleCreateConnector = vi.fn();

const Picker = composeStory(
    {
        parameters: {
            chatContext: {
                sessionId: "mock-session-id",
                connectorSchemas: mockConnectorSchemas,
                handleCreateConnector,
            },
            configContext: { persistenceEnabled: true },
        },
    },
    meta
);

describe("ConnectorsAvailableDialog web search cards", () => {
    beforeAll(() => server.listen());
    afterEach(() => server.resetHandlers());
    afterAll(() => server.close());

    test.each([
        { subtype: "tavily", label: "Tavily", logo: tavilyLogo },
        { subtype: "exa", label: "Exa", logo: exaLogo },
    ])("lists $label as a Web Search app connector with its logo and the Early Access badge", async ({ subtype, label, logo }) => {
        const user = userEvent.setup();
        render(<Picker />);
        await user.click(await screen.findByTestId("createConnector"));

        const card = await within(await screen.findByRole("dialog")).findByTestId(`connector-type-web_search-${subtype}`);

        expect(within(card).getByText(label)).toBeInTheDocument();
        expect(within(card).getByText("Web Search")).toBeInTheDocument();
        expect(within(card).getByRole("img", { name: label })).toHaveAttribute("src", logo);
        expect(await within(card).findByText("EARLY ACCESS")).toBeInTheDocument();

        await user.click(card);
        expect(handleCreateConnector).toHaveBeenCalledWith(expect.objectContaining({ type: "web_search", subtype }));
    });
});
