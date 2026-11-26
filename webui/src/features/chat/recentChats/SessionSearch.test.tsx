// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { BUILDER_AGENT_NAME } from "@/providers/contexts/BuilderContext";

const apiGet = vi.fn();
let builderAccess = true;

type SearchSession = {
    id: string;
    name: string;
    updatedAt: string | null;
    agentId?: string;
    isEphemeralTest?: boolean;
    source?: string;
};

async function loadSearch() {
    vi.resetModules();

    vi.doMock("@/api/client", async () => {
        const actual = await vi.importActual<typeof import("@/api/client")>("@/api/client");
        return { ...actual, api: { webui: { get: apiGet } } };
    });

    vi.doMock("@/hooks", async () => {
        const actual = await vi.importActual<typeof import("@/hooks")>("@/hooks");
        return { ...actual, useBuilderEnabled: () => builderAccess };
    });

    return (await import("./SessionSearch")).SessionSearch;
}

function respondWith(sessions: SearchSession[]) {
    apiGet.mockResolvedValue({ data: sessions, meta: { pagination: { pageNumber: 1, pageSize: 20, totalPages: 1, count: sessions.length, nextPage: null } } });
}

const plainChat: SearchSession = { id: "s-plain", name: "Weather Report", updatedAt: new Date().toISOString() };

beforeEach(() => {
    apiGet.mockReset();
    builderAccess = true;
});

async function typeQuery(query = "weather") {
    const input = screen.getByPlaceholderText("Search chats by title");
    await userEvent.type(input, query);
    await waitFor(() => expect(apiGet).toHaveBeenCalled(), { timeout: 3000 });
}

describe("SessionSearch", () => {
    test("forwards the query and the project scope to the search endpoint", async () => {
        respondWith([plainChat]);
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} projectId="11111111-1111-1111-1111-111111111111" activeTab="chat" />);

        await typeQuery();

        const url = apiGet.mock.calls[apiGet.mock.calls.length - 1][0] as string;
        expect(url).toContain("query=weather");
        expect(url).toContain("projectId=11111111-1111-1111-1111-111111111111");
    });

    test("hands the whole session to onSessionSelect so the caller can route it", async () => {
        const builderChat: SearchSession = { id: "s-builder", name: "Weather Builder Chat", updatedAt: new Date().toISOString(), agentId: BUILDER_AGENT_NAME, isEphemeralTest: false };
        respondWith([builderChat]);
        const onSessionSelect = vi.fn();
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={onSessionSelect} activeTab="chat" />);

        await typeQuery();
        await userEvent.click(await screen.findByText("Weather Builder Chat"));

        expect(onSessionSelect).toHaveBeenCalledWith(expect.objectContaining({ id: "s-builder", agentId: BUILDER_AGENT_NAME, isEphemeralTest: false }));
    });

    test("excludes Builder server-side so gated chats cannot consume the page", async () => {
        builderAccess = false;
        respondWith([plainChat]);
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} activeTab="chat" />);

        await typeQuery();

        const url = apiGet.mock.calls[apiGet.mock.calls.length - 1][0] as string;
        expect(url).toContain(`excludeAgentIds=${BUILDER_AGENT_NAME}`);
    });

    test("does not exclude Builder server-side when the user has access", async () => {
        builderAccess = true;
        respondWith([plainChat]);
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} activeTab="chat" />);

        await typeQuery();

        const url = apiGet.mock.calls[apiGet.mock.calls.length - 1][0] as string;
        expect(url).not.toContain("excludeAgentIds");
    });

    test("hides Builder sessions when the user lacks Builder access", async () => {
        builderAccess = false;
        respondWith([plainChat, { id: "s-builder", name: "Weather Builder Chat", updatedAt: new Date().toISOString(), agentId: BUILDER_AGENT_NAME }]);
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} activeTab="chat" />);

        await typeQuery();

        expect(await screen.findByText("Weather Report")).toBeInTheDocument();
        expect(screen.queryByText("Weather Builder Chat")).not.toBeInTheDocument();
    });

    test("shows Builder sessions when the user has Builder access", async () => {
        builderAccess = true;
        respondWith([{ id: "s-builder", name: "Weather Builder Chat", updatedAt: new Date().toISOString(), agentId: BUILDER_AGENT_NAME }]);
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} activeTab="chat" />);

        await typeQuery();

        expect(await screen.findByText("Weather Builder Chat")).toBeInTheDocument();
    });

    test("hides ephemeral builder Test-mode sessions", async () => {
        respondWith([plainChat, { id: "s-test", name: "Weather Test Session", updatedAt: new Date().toISOString(), isEphemeralTest: true }]);
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} activeTab="chat" />);

        await typeQuery();

        expect(await screen.findByText("Weather Report")).toBeInTheDocument();
        expect(screen.queryByText("Weather Test Session")).not.toBeInTheDocument();
    });

    test("renders N/A rather than the epoch when updatedAt is null", async () => {
        respondWith([{ id: "s-null", name: "Weather Report", updatedAt: null }]);
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} activeTab="chat" />);

        await typeQuery();

        expect(await screen.findByText("Last message N/A")).toBeInTheDocument();
        expect(screen.queryByText(/1970/)).not.toBeInTheDocument();
    });

    test("keyboard selection indexes the filtered list, not the raw results", async () => {
        builderAccess = false;
        respondWith([
            { id: "s-builder", name: "Weather Builder Chat", updatedAt: new Date().toISOString(), agentId: BUILDER_AGENT_NAME },
            { id: "s-plain", name: "Weather Report", updatedAt: new Date().toISOString() },
        ]);
        const onSessionSelect = vi.fn();
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={onSessionSelect} activeTab="chat" />);

        await typeQuery();
        await screen.findByText("Weather Report");
        await userEvent.keyboard("{Enter}");

        expect(onSessionSelect).toHaveBeenCalledWith(expect.objectContaining({ id: "s-plain" }));
    });

    test("arrow keys move the selection within the filtered list", async () => {
        respondWith([
            { id: "s-1", name: "Weather One", updatedAt: new Date().toISOString() },
            { id: "s-2", name: "Weather Two", updatedAt: new Date().toISOString() },
        ]);
        const onSessionSelect = vi.fn();
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={onSessionSelect} activeTab="chat" />);

        await typeQuery();
        await screen.findByText("Weather Two");
        await userEvent.keyboard("{ArrowDown}{Enter}");

        expect(onSessionSelect).toHaveBeenCalledWith(expect.objectContaining({ id: "s-2" }));
    });

    test("Tab does not select a result", async () => {
        respondWith([plainChat]);
        const onSessionSelect = vi.fn();
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={onSessionSelect} activeTab="chat" />);

        await typeQuery();
        await screen.findByText("Weather Report");
        await userEvent.keyboard("{Tab}");

        expect(onSessionSelect).not.toHaveBeenCalled();
    });

    test("scopes results to the active tab", async () => {
        respondWith([
            { id: "s-chat", name: "Weather Chat", updatedAt: new Date().toISOString(), source: "chat" },
            { id: "s-sched", name: "Weather Scheduled", updatedAt: new Date().toISOString(), source: "scheduler" },
        ]);
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} activeTab="scheduler" />);

        await typeQuery();

        expect(await screen.findByText("Weather Scheduled")).toBeInTheDocument();
        expect(screen.queryByText("Weather Chat")).not.toBeInTheDocument();
    });

    test("forwards the agent scope so agent-mode search stays scoped", async () => {
        respondWith([plainChat]);
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} agentId="MyAgent" activeTab="chat" />);

        await typeQuery();

        const url = apiGet.mock.calls[apiGet.mock.calls.length - 1][0] as string;
        expect(url).toContain("includeAgentIds=MyAgent");
    });

    test("over-fetches so client-side filtering cannot empty the page", async () => {
        respondWith([plainChat]);
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} activeTab="chat" />);

        await typeQuery();

        const url = apiGet.mock.calls[apiGet.mock.calls.length - 1][0] as string;
        expect(url).toContain("pageSize=50");
    });

    test("distinguishes an empty view from a genuinely empty result set", async () => {
        builderAccess = false;
        respondWith([{ id: "s-builder", name: "Weather Builder Chat", updatedAt: new Date().toISOString(), agentId: BUILDER_AGENT_NAME }]);
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} activeTab="chat" />);

        await typeQuery();

        expect(await screen.findByText("No results in this view")).toBeInTheDocument();
        expect(screen.queryByText("No results found")).not.toBeInTheDocument();
    });

    test("says how many matches were withheld when results are truncated", async () => {
        apiGet.mockResolvedValue({
            data: [plainChat],
            meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 7, nextPage: null } },
        });
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} activeTab="chat" />);

        await typeQuery();

        expect(await screen.findByText(/Showing 1 of 7/)).toBeInTheDocument();
    });

    test("surfaces a failed search distinctly from an empty result set", async () => {
        apiGet.mockRejectedValue(new Error("boom"));
        const SessionSearch = await loadSearch();
        render(<SessionSearch onSessionSelect={vi.fn()} activeTab="chat" />);

        await typeQuery();

        expect(await screen.findByText("Search failed. Please try again.")).toBeInTheDocument();
        expect(screen.queryByText("No results found")).not.toBeInTheDocument();
    });
});
