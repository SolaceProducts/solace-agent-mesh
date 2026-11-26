// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { getPaginatedMeta } from "@/testing/helpers";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { AgentMeshPage } from "./AgentMeshPage";

const agentCard = (name: string, displayName: string, isWorkflow = false) => {
    const extensions: { uri: string; params: Record<string, unknown> }[] = [{ uri: "https://solace.com/a2a/extensions/display-name", params: { display_name: displayName } }];
    if (isWorkflow) {
        extensions.push({ uri: "https://solace.com/a2a/extensions/agent-type", params: { type: "workflow" } });
    }
    return {
        name,
        description: `${displayName} description`,
        url: `solace:${name}`,
        version: "1.0.0",
        protocolVersion: "0.3.0",
        capabilities: { streaming: true, pushNotifications: false, stateTransitionHistory: false, extensions },
        defaultInputModes: ["text"],
        defaultOutputModes: ["text"],
        skills: [],
    };
};

const CARDS = [agentCard("hr", "HR Agent"), agentCard("finance", "Finance Agent"), agentCard("wf_order", "Order Flow", true), agentCard("wf_invoice", "Invoice Sync", true)];

const server = setupServer(
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: CARDS })),
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/toolsets*", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) }))
);

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
beforeEach(() => {
    localStorage.clear();
});
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const renderPage = (initialPath = "/agent-mesh") => renderWithProviders(<AgentMeshPage />, { initialPath, userContextValues: { userHasCapability: () => true } });

const tab = (name: "Agents" | "Workflows") => screen.getByRole("tab", { name });

describe("AgentMeshPage tabs", () => {
    test("renders both tabs with Agents active by default", async () => {
        renderPage();

        expect(await screen.findByRole("tab", { name: "Agents" })).toHaveAttribute("aria-selected", "true");
        expect(tab("Workflows")).toHaveAttribute("aria-selected", "false");
    });

    test("the Agents tab lists agents only", async () => {
        renderPage();

        expect(await screen.findByText("HR Agent")).toBeInTheDocument();
        expect(screen.getByText("Finance Agent")).toBeInTheDocument();
        expect(screen.queryByText("Order Flow")).not.toBeInTheDocument();
    });

    test("switching to Workflows swaps the list to workflows only", async () => {
        renderPage();
        await screen.findByText("HR Agent");

        await userEvent.click(tab("Workflows"));

        expect(await screen.findByText("Order Flow")).toBeInTheDocument();
        expect(screen.getByText("Invoice Sync")).toBeInTheDocument();
        expect(screen.queryByText("HR Agent")).not.toBeInTheDocument();
    });

    test("?tab=workflows lands on the Workflows tab without showing Agents first", async () => {
        renderPage("/agent-mesh?tab=workflows");

        expect(await screen.findByText("Order Flow")).toBeInTheDocument();
        expect(screen.queryByText("HR Agent")).not.toBeInTheDocument();
        expect(tab("Workflows")).toHaveAttribute("aria-selected", "true");
    });

    test("?selected= naming a workflow lands on the Workflows tab with its panel open", async () => {
        renderPage("/agent-mesh?selected=Order%20Flow");

        expect(await screen.findByText("Workflow Details", undefined, { timeout: 5000 })).toBeInTheDocument();
        await waitFor(() => expect(tab("Workflows")).toHaveAttribute("aria-selected", "true"));
        expect(screen.getByRole("tab", { name: "Agents" })).toHaveAttribute("aria-selected", "false");
    });

    test("?selected= matching a workflow's name beats an agent whose display name is the same", async () => {
        server.use(http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [agentCard("ops", "wf_order"), ...CARDS] })));
        renderPage("/agent-mesh?selected=wf_order");

        expect(await screen.findByText("Workflow Details", undefined, { timeout: 5000 })).toBeInTheDocument();
        await waitFor(() => expect(tab("Workflows")).toHaveAttribute("aria-selected", "true"));
    });

    test("?selected= naming a workflow wins over an explicit ?tab=agents", async () => {
        renderPage("/agent-mesh?tab=agents&selected=Order%20Flow");

        expect(await screen.findByText("Workflow Details", undefined, { timeout: 5000 })).toBeInTheDocument();
        await waitFor(() => expect(tab("Workflows")).toHaveAttribute("aria-selected", "true"));
        expect(screen.queryByText("HR Agent")).not.toBeInTheDocument();
    });

    test("an empty Workflows tab says Workflows, not Agents", async () => {
        server.use(http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [agentCard("hr", "HR Agent")] })));
        renderPage("/agent-mesh?tab=workflows");

        expect(await screen.findByText("No Workflows Found")).toBeInTheDocument();
    });
});

describe("AgentMeshPage tabs — layout interaction", () => {
    test("Hierarchical is offered on Agents but not on Workflows", async () => {
        renderPage();
        await screen.findByText("HR Agent");
        expect(screen.getByRole("button", { name: /Hierarchical/ })).toBeInTheDocument();

        await userEvent.click(tab("Workflows"));

        await waitFor(() => expect(screen.queryByRole("button", { name: /Hierarchical/ })).not.toBeInTheDocument());
        expect(screen.getByRole("button", { name: /Cards/ })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Table/ })).toBeInTheDocument();
    });

    test("a Hierarchical user sees Cards on Workflows and Hierarchical again on Agents", async () => {
        renderPage("/agent-mesh?layout=hierarchical");
        expect(await screen.findByText(/Only running agents are displayed/)).toBeInTheDocument();

        await userEvent.click(tab("Workflows"));
        await waitFor(() => expect(screen.queryByText(/Only running agents are displayed/)).not.toBeInTheDocument());
        expect(await screen.findByText("Order Flow")).toBeInTheDocument();

        await userEvent.click(tab("Agents"));
        expect(await screen.findByText(/Only running agents are displayed/)).toBeInTheDocument();
    });
});

describe("AgentMeshPage tabs — per-tab state", () => {
    test("a search term does not carry across a tab switch", async () => {
        renderPage();
        const search = await screen.findByTestId("agentSearchInput");

        await userEvent.type(search, "HR");
        await waitFor(() => expect(screen.queryByText("Finance Agent")).not.toBeInTheDocument());

        await userEvent.click(tab("Workflows"));

        expect(await screen.findByText("Order Flow")).toBeInTheDocument();
        expect(screen.getByText("Invoice Sync")).toBeInTheDocument();
        expect(screen.getByTestId("agentSearchInput")).toHaveValue("");
    });

    test("an open side panel closes on a tab switch", async () => {
        renderPage("/agent-mesh?tab=workflows");

        await userEvent.click(await screen.findByText("Order Flow"));
        const panel = await screen.findByText("Workflow Details");
        expect(panel).toBeInTheDocument();

        await userEvent.click(tab("Agents"));

        await waitFor(() => expect(screen.queryByText("Workflow Details")).not.toBeInTheDocument());
    });

    test("Table layout also drops the search term and the side panel on a tab switch", async () => {
        renderPage("/agent-mesh?layout=table");
        const search = await screen.findByTestId("agentSearchInput");

        await userEvent.type(search, "HR");
        await waitFor(() => expect(screen.queryByText("Finance Agent")).not.toBeInTheDocument());
        await userEvent.click(screen.getByText("HR Agent"));
        expect(await screen.findByText("Agent Details")).toBeInTheDocument();

        await userEvent.click(tab("Workflows"));

        expect(await screen.findByText("Order Flow")).toBeInTheDocument();
        expect(screen.getByText("Invoice Sync")).toBeInTheDocument();
        expect(screen.getByTestId("agentSearchInput")).toHaveValue("");
        await waitFor(() => expect(screen.queryByText("Agent Details")).not.toBeInTheDocument());
    });

    test("the table drops the Type and Created By columns on the Workflows tab", async () => {
        renderPage("/agent-mesh?layout=table");
        const table = await screen.findByRole("table");
        expect(within(table).getByText("Type")).toBeInTheDocument();
        expect(within(table).getByText("Created By")).toBeInTheDocument();

        await userEvent.click(tab("Workflows"));

        await waitFor(() => expect(within(screen.getByRole("table")).queryByText("Type")).not.toBeInTheDocument());
        expect(within(screen.getByRole("table")).queryByText("Created By")).not.toBeInTheDocument();
        expect(within(screen.getByRole("table")).getByText("Status")).toBeInTheDocument();
    });

    test("no em-dash placeholder cell survives on the Workflows tab", async () => {
        renderPage("/agent-mesh?layout=table&tab=workflows");

        await screen.findByText("Order Flow");
        expect(within(screen.getByRole("table")).queryByText("—")).not.toBeInTheDocument();
    });
});

describe("AgentMeshPage for an invoke-only user", () => {
    test("the Workflows tab lists the workflows and their panel opens without touching /platform/workflows, with no detail view; the diagram is reached from Builder > Workflows", async () => {
        const platformWorkflowsFetch = vi.fn(() => HttpResponse.json({ message: "forbidden" }, { status: 403 }));
        server.use(
            http.get("*/api/v2/platform/agents", () => HttpResponse.json({ message: "forbidden" }, { status: 403 })),
            http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ message: "forbidden" }, { status: 403 })),
            http.get("*/api/v2/platform/workflows", platformWorkflowsFetch)
        );
        renderWithProviders(<AgentMeshPage />, { initialPath: "/agent-mesh?tab=workflows", userContextValues: { userHasCapability: () => false } });

        await userEvent.click(await screen.findByText("Order Flow"));

        expect(await screen.findByText("Workflow Details")).toBeInTheDocument();
        expect(screen.getByText("Invoice Sync")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Manage Workflow" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Open Workflow" })).not.toBeInTheDocument();
        expect(platformWorkflowsFetch).not.toHaveBeenCalled();
    });
});
