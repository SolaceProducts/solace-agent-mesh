// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { agentsApi } from "@/api/agents";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Agent, Toolset } from "@/types";

import { ToolsetAddToAgentDialog } from "./ToolsetAddToAgentDialog";

const WEB = "builtin_web_request_tools";
const AGENT_ID = "agent-1";

const storedAgent = (over: Partial<Agent> = {}): Agent =>
    ({
        id: AGENT_ID,
        name: "DocFetcher",
        description: "Fetches docs",
        systemPrompt: "You fetch documentation.",
        type: "standard",
        toolsets: ["builtin_artifact_tools"],
        additionalConfigurations: { volumes: [{ name: "workspace", type: "ephemeral" }] },
        ...over,
    }) as Agent;

let agentFromApi: Agent = storedAgent();
const putCalls: Array<{ id: string; body: Record<string, unknown> }> = [];

const invalidateAgentRelations = vi.fn();
vi.mock("@/api/agents", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/agents")>()),
    useAgents: () => ({ data: [agentFromApi], isLoading: false }),
    invalidateAgentRelations: (...args: unknown[]) => invalidateAgentRelations(...args),
}));

vi.mock("@/api/toolsets", () => ({
    useCustomToolsets: () => ({ refetch: vi.fn() }),
}));

const webToolset: Toolset = {
    id: WEB,
    name: "Web Request Tools",
    description: "Make HTTP requests to external services",
    type: "builtin",
    agents: [],
} as unknown as Toolset;

function openDialog() {
    return renderWithProviders(<ToolsetAddToAgentDialog isOpen toolset={webToolset} onClose={vi.fn()} />, { initialPath: "/toolsets" });
}

async function submitFor(view: ReturnType<typeof openDialog>) {
    const { getByTestId, getByRole } = view;
    fireEvent.click(getByTestId("agentSelectTrigger"));
    fireEvent.click(getByRole("button", { name: "DocFetcher" }));
    await waitFor(() => expect((getByTestId("agentInstructionsTextarea") as HTMLTextAreaElement).value).not.toBe(""));
    fireEvent.click(getByTestId("addButton"));
    await waitFor(() => expect(putCalls).toHaveLength(1));
    return putCalls[0].body;
}

afterEach(() => {
    putCalls.length = 0;
    invalidateAgentRelations.mockClear();
    agentFromApi = storedAgent();
    vi.restoreAllMocks();
});

describe("ToolsetAddToAgentDialog — the read-modify-write PUT", () => {
    beforeEach(() => {
        vi.spyOn(agentsApi, "get").mockImplementation(() => Promise.resolve(agentFromApi));
        vi.spyOn(agentsApi, "update").mockImplementation((id, body) => {
            putCalls.push({ id, body });
            return Promise.resolve({ id: AGENT_ID });
        });
    });

    test("grants the toolset under the key it is bound by", async () => {
        const body = await submitFor(openDialog());

        expect(body.toolsets).toEqual(["builtin_artifact_tools", WEB]);
    });

    test("clears narrowing using the bound key, not the display label", async () => {
        agentFromApi = storedAgent({ toolsetExcludedTools: { [WEB]: ["web_search_google"] } });

        const body = await submitFor(openDialog());

        expect(body.toolsetExcludedTools).toEqual({});
    });

    test("echoes back the agent's authored extras", async () => {
        const body = await submitFor(openDialog());

        expect(body.additionalConfigurations).toEqual({ volumes: [{ name: "workspace", type: "ephemeral" }] });
    });

    test("invalidates the agent cache the editor seeds itself from", async () => {
        await submitFor(openDialog());

        expect(invalidateAgentRelations).toHaveBeenCalledTimes(1);
    });
});

describe("ToolsetAddToAgentDialog — the /api/v2 request", () => {
    afterEach(() => vi.unstubAllGlobals());

    async function submitOnV2(over: Record<string, unknown> = {}) {
        const wire = {
            ...storedAgent(),
            type: "agent",
            agentType: "standard",
            connectorIds: ["c1"],
            skills: [],
            skillIds: [],
            toolsetConfigs: [],
            skillConfigs: [],
            toolRegistrations: [],
            runtimeStatus: { status: "unknown", problems: [] },
            deployed: null,
            syncStatus: null,
            expiresTime: null,
            createdTime: null,
            updatedTime: null,
            ...over,
        };
        const fetchMock = vi.fn<typeof fetch>(async () => new Response(JSON.stringify({ data: wire })));
        vi.stubGlobal("fetch", fetchMock);
        const { getByTestId, getByRole } = renderWithProviders(<ToolsetAddToAgentDialog isOpen toolset={webToolset} onClose={vi.fn()} />, { initialPath: "/toolsets" });

        fireEvent.click(getByTestId("agentSelectTrigger"));
        fireEvent.click(getByRole("button", { name: "DocFetcher" }));
        await waitFor(() => expect((getByTestId("agentInstructionsTextarea") as HTMLTextAreaElement).value).not.toBe(""));
        fireEvent.click(getByTestId("addButton"));
        await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(true));

        const [url, init] = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT")!;
        return { fetchMock, url: String(url), body: JSON.parse(init!.body as string) };
    }

    test("reads and replaces the agent through /api/v2", async () => {
        const { fetchMock, url, body } = await submitOnV2();

        const agentUrl = new RegExp(`/api/v2/platform/agents/${AGENT_ID}$`);
        expect(String(fetchMock.mock.calls[0][0])).toMatch(agentUrl);
        expect(url).toMatch(agentUrl);
        expect(body).toMatchObject({ toolsets: ["builtin_artifact_tools", WEB], connectorIds: ["c1"] });
        expect(body).not.toHaveProperty("deploy");
        expect(body).not.toHaveProperty("connectors");
    });

    test("keeps the agent's skills, since a v2 PUT clears what it leaves out", async () => {
        const skillConfigs = [{ skillId: "s1", skillName: "render", configValues: { render: { api_key: "<REDACTED>", model_id: "m1" } } }];

        const { body } = await submitOnV2({ skillIds: ["s1"], skillConfigs });

        expect(body).toMatchObject({ skillIds: ["s1"], skillConfigs });
    });
});
