// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import type { ExperimentToolAuth, ExperimentToolAuthAgent, ExperimentToolAuthItem } from "@/types/eval";

import { StoryProvider } from "@/testing/providers/StoryProvider";

const mockGetExperimentToolAuth = vi.fn();
const mockBeginExperimentToolAuth = vi.fn();
const mockDisconnectExperimentToolAuth = vi.fn();
const mockListToolAuthCandidateAgents = vi.fn<(...args: unknown[]) => Promise<string[]>>(() => Promise.resolve([]));
const mockAddExperimentToolAuthAgent = vi.fn();
const mockRemoveExperimentToolAuthAgent = vi.fn();

vi.mock("@/api/evaluations/versioned", async () => {
    const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
    return {
        ...actual,
        evaluationsApi: {
            ...actual.evaluationsApi,
            getExperimentToolAuth: (...args: unknown[]) => mockGetExperimentToolAuth(...args),
            beginExperimentToolAuth: (...args: unknown[]) => mockBeginExperimentToolAuth(...args),
            disconnectExperimentToolAuth: (...args: unknown[]) => mockDisconnectExperimentToolAuth(...args),
            listToolAuthCandidateAgents: (...args: unknown[]) => mockListToolAuthCandidateAgents(...args),
            addExperimentToolAuthAgent: (...args: unknown[]) => mockAddExperimentToolAuthAgent(...args),
            removeExperimentToolAuthAgent: (...args: unknown[]) => mockRemoveExperimentToolAuthAgent(...args),
        },
    };
});

vi.mock("@/features/entrypoints/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/features/entrypoints/hooks");
    return {
        ...actual,
        useAgentOptions: () => [
            { name: "agent_solver_uuid", displayName: "Solver" },
            { name: "agent_reporter_uuid", displayName: "Reporter" },
            { name: "agent_archivist_uuid", displayName: "Archivist" },
        ],
    };
});

import { ConnectToolsPanel } from "./ConnectToolsPanel";

const connectedItem: ExperimentToolAuthItem = {
    credentialKey: "gmail",
    displayName: "Gmail",
    authType: "oauth2",
    interactive: true,
    status: "connected",
    expiresAt: "2026-09-01T12:00:00.000000+00:00",
    lastAuthorizedBy: "teammate@example.com",
    lastAuthorizedAt: "2026-08-30T09:00:00.000000+00:00",
};

const notConnectedItem: ExperimentToolAuthItem = {
    credentialKey: "sheets",
    displayName: "Sheets",
    interactive: true,
    status: "not_connected",
};

const nonInteractiveItem: ExperimentToolAuthItem = {
    credentialKey: "api-key-tool",
    interactive: false,
    status: "not_connected",
};

function agentGroup(agentName: string, tools: ExperimentToolAuthItem[], overrides?: Partial<ExperimentToolAuthAgent>): ExperimentToolAuthAgent {
    return { agentName, isTarget: false, reachable: true, tools, ...overrides };
}

function checklist(agents: ExperimentToolAuthAgent[], allowListIsWildcard = false, runAs = "system:eval", overrides?: Partial<ExperimentToolAuth>): ExperimentToolAuth {
    return { runAs, agents, allowListIsWildcard, totalAgents: agents.length, totalRunningAgents: agents.length, agentScanLimit: 60, ...overrides };
}

function target(tools: ExperimentToolAuthItem[], overrides?: Partial<ExperimentToolAuthAgent>): ExperimentToolAuthAgent {
    return agentGroup("Solver", tools, { isTarget: true, ...overrides });
}

function targetOnly(tools: ExperimentToolAuthItem[], allowListIsWildcard = false, runAs = "system:eval"): ExperimentToolAuth {
    return checklist([target(tools)], allowListIsWildcard, runAs);
}

async function openPicker(user: ReturnType<typeof userEvent.setup>, { expand = true } = {}) {
    await user.click(await screen.findByRole("button", { name: /Add Agent/ }));
    if (expand) await user.click(await screen.findByRole("combobox"));
}

type UserContextOverrides = Parameters<typeof StoryProvider>[0]["userContextValues"];

function panelTree(runAs?: string, userContextValues?: UserContextOverrides) {
    return (
        <StoryProvider shouldIsolateQueryClient userContextValues={userContextValues}>
            <ConnectToolsPanel experimentId="exp-1" runAs={runAs} />
        </StoryProvider>
    );
}

function renderPanel(runAs?: string, userContextValues?: UserContextOverrides) {
    return render(panelTree(runAs, userContextValues));
}

function errorWithStatus(status: number): Error & { status: number } {
    const err = new Error(`http ${status}`) as Error & { status: number };
    err.status = status;
    return err;
}

describe("ConnectToolsPanel", () => {
    afterEach(() => {
        vi.clearAllMocks();
    });

    test("renders per-credential status, attribution, and the system-user copy", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([connectedItem, notConnectedItem], false, "system:team-bot"));
        renderPanel("system:team-bot");

        expect(await screen.findByText("Gmail")).toBeInTheDocument();
        expect(screen.getByText("Connected")).toBeInTheDocument();
        expect(screen.getByText("Not connected")).toBeInTheDocument();
        expect(screen.getByText(/Last authorized by teammate@example.com/)).toBeInTheDocument();
        expect(screen.getByText("system:team-bot")).toBeInTheDocument();
        expect(screen.getAllByRole("button", { name: "Authorize" })).toHaveLength(1);
        expect(mockGetExperimentToolAuth).toHaveBeenCalledWith("exp-1");
    });

    test("defaults the principal copy to system:eval when runAs is unset", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem]));
        renderPanel();
        expect(await screen.findByTestId("connectToolsPanel")).toBeInTheDocument();
        expect(screen.getByTestId("connectToolsPrincipal")).toHaveTextContent("Runs as: system:eval");
    });

    test("a run_as change re-queries the checklist under the new principal", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([connectedItem], false, "system:team-bot"));
        const { rerender } = renderPanel("system:team-bot");

        expect(await screen.findByTestId("connectToolsPrincipal")).toHaveTextContent("Runs as: system:team-bot");
        expect(mockGetExperimentToolAuth).toHaveBeenCalledTimes(1);

        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem], false, "system:reporting"));
        rerender(panelTree("system:reporting"));

        await waitFor(() => expect(mockGetExperimentToolAuth).toHaveBeenCalledTimes(2));
        expect(await screen.findByText("Sheets")).toBeInTheDocument();
        expect(screen.getByTestId("connectToolsPrincipal")).toHaveTextContent("Runs as: system:reporting");
        expect(screen.queryByText("Gmail")).not.toBeInTheDocument();
    });

    test("renders nothing when no agent in the graph declares an interactive credential", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([]));
        renderPanel();
        await waitFor(() => expect(mockGetExperimentToolAuth).toHaveBeenCalled());
        expect(screen.queryByTestId("connectToolsPanel")).not.toBeInTheDocument();
    });

    test("non-interactive credentials get no Authorize button", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([nonInteractiveItem, notConnectedItem]));
        renderPanel();
        expect(await screen.findByText("Set in configuration")).toBeInTheDocument();
        expect(screen.getAllByRole("button", { name: "Authorize" })).toHaveLength(1);
    });

    test.each([
        ["a platform with no control plane (503)", errorWithStatus(503)],
        ["an experiment or route that is gone (404)", errorWithStatus(404)],
    ])("hides the section on %s", async (_label, err) => {
        mockGetExperimentToolAuth.mockRejectedValue(err);
        renderPanel();
        await waitFor(() => expect(mockGetExperimentToolAuth).toHaveBeenCalled());
        expect(screen.queryByTestId("connectToolsPanel")).not.toBeInTheDocument();
    });

    test("says so when the checklist cannot be read at all", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockRejectedValue(new Error("boom"));
        renderPanel();

        expect(await screen.findByText(/Unable to read which tools need authorization/)).toBeInTheDocument();
        expect(screen.getByText(/boom/)).toBeInTheDocument();

        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem]));
        await user.click(screen.getByRole("button", { name: "Retry" }));
        expect(await screen.findByRole("button", { name: "Authorize" })).toBeInTheDocument();
    });

    test("a failed refresh keeps the panel and flags the statuses as stale", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValueOnce(targetOnly([notConnectedItem])).mockRejectedValue(new Error("dropped poll"));
        mockBeginExperimentToolAuth.mockResolvedValue({ authUri: "https://idp/auth?state=s1", credentialKey: "sheets", expiresAt: "" });
        vi.spyOn(window, "open").mockReturnValue({ opener: window } as unknown as Window);

        renderPanel();
        await user.click(await screen.findByRole("button", { name: "Authorize" }));

        expect(await screen.findByText(/may be out of date/)).toBeInTheDocument();
        expect(screen.getByTestId("connectToolsPanel")).toBeInTheDocument();
        expect(screen.getByText("Sheets")).toBeInTheDocument();
    });

    test("offers the picker when the graph is larger than the read returns", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([connectedItem])], false, "system:eval", { totalAgents: 74 }));
        renderPanel();

        expect(await screen.findByTestId("connectToolsAddAgent")).toBeInTheDocument();
        expect(screen.getByText(/74/)).toBeInTheDocument();
    });

    test("an empty picker says the scan was capped rather than implying nothing is left", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([notConnectedItem])], true, "system:eval", { totalRunningAgents: 74, agentScanLimit: 60 }));
        mockListToolAuthCandidateAgents.mockResolvedValue([]);
        renderPanel();

        await openPicker(user);
        expect(await screen.findByText(/Showing the first 60 of 74 running agents/)).toBeInTheDocument();
        expect(screen.queryByText("No agent with an authorizable tool was found")).not.toBeInTheDocument();
    });

    test("an empty picker on a small mesh still reports nothing to authorize", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem], true));
        mockListToolAuthCandidateAgents.mockResolvedValue([]);
        renderPanel();

        await openPicker(user);
        expect(await screen.findByText("No agent with an authorizable tool was found")).toBeInTheDocument();
    });

    test("Authorize begins the flow and opens the auth URI", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem]));
        mockBeginExperimentToolAuth.mockResolvedValue({ authUri: "https://idp/auth?state=s1", credentialKey: "sheets", expiresAt: "" });
        const authWindow = { opener: window } as unknown as Window;
        const openSpy = vi.spyOn(window, "open").mockReturnValue(authWindow);

        renderPanel();
        await user.click(await screen.findByRole("button", { name: "Authorize" }));

        await waitFor(() => expect(mockBeginExperimentToolAuth).toHaveBeenCalledWith("exp-1", "Solver", "sheets"));
        expect(openSpy).toHaveBeenCalledWith("https://idp/auth?state=s1", "_blank");
        expect(authWindow.opener).toBeNull();
        expect(screen.queryByText(/blocked the authorization window/)).not.toBeInTheDocument();
        openSpy.mockRestore();
    });

    test("a blocked popup falls back to a plain authorization link", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem]));
        mockBeginExperimentToolAuth.mockResolvedValue({ authUri: "https://idp/auth?state=s2", credentialKey: "sheets", expiresAt: "" });
        const openSpy = vi.spyOn(window, "open").mockReturnValue(null);

        renderPanel();
        await user.click(await screen.findByRole("button", { name: "Authorize" }));

        const link = await screen.findByRole("link", { name: /Open the authorization page/ });
        expect(link).toHaveAttribute("href", "https://idp/auth?state=s2");
        openSpy.mockRestore();
    });

    test("a failed begin surfaces a dismissible error banner", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem]));
        mockBeginExperimentToolAuth.mockRejectedValue(new Error("credential not found: sheets"));

        renderPanel();
        await user.click(await screen.findByRole("button", { name: "Authorize" }));

        expect(await screen.findByText("credential not found: sheets")).toBeInTheDocument();
        await user.click(screen.getByRole("button", { name: "Dismiss" }));
        expect(screen.queryByText("credential not found: sheets")).not.toBeInTheDocument();
    });

    test("Disconnect asks for confirmation before clearing the credential", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([connectedItem]));
        mockDisconnectExperimentToolAuth.mockResolvedValue(undefined);

        renderPanel();
        await user.click(await screen.findByRole("button", { name: "Disconnect" }));
        expect(await screen.findByText("Disconnect Gmail")).toBeInTheDocument();
        expect(mockDisconnectExperimentToolAuth).not.toHaveBeenCalled();

        await user.click(screen.getByTestId("dialogConfirmButton"));
        await waitFor(() => expect(mockDisconnectExperimentToolAuth).toHaveBeenCalledWith("exp-1", "Solver", "gmail"));
        await waitFor(() => expect(screen.queryByText("Disconnect Gmail")).not.toBeInTheDocument());
    });

    test("cancelling the disconnect dialog leaves the credential alone", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([connectedItem]));

        renderPanel();
        await user.click(await screen.findByRole("button", { name: "Disconnect" }));
        await user.click(await screen.findByRole("button", { name: "Cancel" }));

        await waitFor(() => expect(screen.queryByText("Disconnect Gmail")).not.toBeInTheDocument());
        expect(mockDisconnectExperimentToolAuth).not.toHaveBeenCalled();
    });

    test("a failed disconnect surfaces an error banner", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([connectedItem]));
        mockDisconnectExperimentToolAuth.mockRejectedValue(new Error("agent is not running"));

        renderPanel();
        await user.click(await screen.findByRole("button", { name: "Disconnect" }));
        await user.click(screen.getByTestId("dialogConfirmButton"));

        expect(await screen.findByText("agent is not running")).toBeInTheDocument();
    });
});

describe("ConnectToolsPanel delegation graph", () => {
    afterEach(() => {
        vi.clearAllMocks();
        mockListToolAuthCandidateAgents.mockResolvedValue([]);
    });

    test("renders one section per agent, target first", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([connectedItem]), agentGroup("agent_reporter_uuid", [notConnectedItem])]));
        renderPanel();

        const sections = await screen.findAllByTestId("connectToolsAgentSection");
        expect(sections).toHaveLength(2);
        expect(sections[0]).toHaveAttribute("data-agent-name", "Solver");
        expect(sections[0]).toHaveTextContent("Solver");
        expect(sections[0]).not.toHaveTextContent("Delegates to");
        expect(sections[1]).toHaveTextContent("Delegates to: Reporter");
        expect(sections[1]).toHaveTextContent("Sheets");
        expect(screen.getByText("(1 of 2 connected)")).toBeInTheDocument();
    });

    test("authorizing a peer credential names that peer", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([connectedItem]), agentGroup("Reporter", [notConnectedItem])]));
        mockBeginExperimentToolAuth.mockResolvedValue({ authUri: "https://idp/auth?state=peer", credentialKey: "sheets", expiresAt: "" });
        vi.spyOn(window, "open").mockReturnValue({} as Window);

        renderPanel();
        await user.click(await screen.findByRole("button", { name: "Authorize" }));

        await waitFor(() => expect(mockBeginExperimentToolAuth).toHaveBeenCalledWith("exp-1", "Reporter", "sheets"));
    });

    test("disconnecting a shared credential key names the right agent", async () => {
        const user = userEvent.setup();
        const shared: ExperimentToolAuthItem = { credentialKey: "mcp_oauth2", displayName: "Atlassian", interactive: true, status: "connected" };
        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([shared]), agentGroup("Reporter", [shared])]));
        mockDisconnectExperimentToolAuth.mockResolvedValue(undefined);

        renderPanel();
        const buttons = await screen.findAllByRole("button", { name: "Disconnect" });
        await user.click(buttons[1]);
        await user.click(await screen.findByTestId("dialogConfirmButton"));

        await waitFor(() => expect(mockDisconnectExperimentToolAuth).toHaveBeenCalledWith("exp-1", "Reporter", "mcp_oauth2"));
    });

    test("an unreachable peer is shown with a hint instead of its tools", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([notConnectedItem]), agentGroup("Ghost", [], { reachable: false })]));
        renderPanel();

        expect(await screen.findByText("Delegates to: Ghost")).toBeInTheDocument();
        expect(screen.getByText(/may be stopped, undeployed/)).toBeInTheDocument();
    });

    test("a wildcard graph with no credentials of its own still shows the picker", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([], true));
        renderPanel();

        expect(await screen.findByTestId("connectToolsPanel")).toBeInTheDocument();
        expect(screen.getByTestId("connectToolsAddAgent")).toBeInTheDocument();
        expect(screen.queryByTestId("connectToolsAgentSection")).not.toBeInTheDocument();
        expect(screen.queryByText(/connected\)/)).not.toBeInTheDocument();
    });

    test("a reachable agent with nothing authorizable is dropped from the list", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([notConnectedItem]), agentGroup("Reporter", [nonInteractiveItem])]));
        renderPanel();

        const sections = await screen.findAllByTestId("connectToolsAgentSection");
        expect(sections).toHaveLength(1);
        expect(sections[0]).toHaveAttribute("data-agent-name", "Solver");
        expect(screen.getByText("(0 of 1 connected)")).toBeInTheDocument();
    });

    test("the connected count ignores credentials nobody can authorize", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([connectedItem, nonInteractiveItem]));
        renderPanel();

        expect(await screen.findByText("(1 of 1 connected)")).toBeInTheDocument();
        expect(screen.getByText("Set in configuration")).toBeInTheDocument();
    });

    test("stays mounted when every agent is unreachable", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([], { reachable: false }), agentGroup("Reporter", [], { reachable: false })]));
        renderPanel();

        expect(await screen.findByTestId("connectToolsPanel")).toBeInTheDocument();
        expect(screen.getAllByText(/The agent may be stopped, undeployed/)).toHaveLength(2);
    });

    test("an unreachable agent is kept even though it lists nothing", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([notConnectedItem]), agentGroup("Ghost", [], { reachable: false })]));
        renderPanel();
        expect(await screen.findByText("Delegates to: Ghost")).toBeInTheDocument();
    });

    test("still hides when the graph is enumerated and gates nothing behind OAuth", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([]));
        renderPanel();
        await waitFor(() => expect(mockGetExperimentToolAuth).toHaveBeenCalled());
        expect(screen.queryByTestId("connectToolsPanel")).not.toBeInTheDocument();
    });

    test("no Add Agent picker when the graph is fully enumerated", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem]));
        renderPanel();
        await screen.findByTestId("connectToolsPanel");
        expect(screen.queryByTestId("connectToolsAddAgent")).not.toBeInTheDocument();
    });

    test("a wildcard allow list offers an Add Agent picker that pins the agent to the experiment", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem], true));
        mockListToolAuthCandidateAgents.mockResolvedValue(["Archivist"]);
        mockAddExperimentToolAuthAgent.mockResolvedValue(undefined);
        renderPanel();

        await openPicker(user);
        await user.click(await screen.findByText("Archivist"));

        await waitFor(() => expect(mockAddExperimentToolAuthAgent).toHaveBeenCalledWith("exp-1", "agent_archivist_uuid"));
        await waitFor(() => expect(mockGetExperimentToolAuth).toHaveBeenCalledTimes(2));
        expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    });

    test("a failed pin is reported", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem], true));
        mockListToolAuthCandidateAgents.mockResolvedValue(["Archivist"]);
        mockAddExperimentToolAuthAgent.mockRejectedValue(new Error("agentName must name a running agent"));
        renderPanel();

        await openPicker(user);
        await user.click(await screen.findByText("Archivist"));

        expect(await screen.findByText("agentName must name a running agent")).toBeInTheDocument();
    });

    test("the picker offers only agents that hold an authorizable credential", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem], true));
        mockListToolAuthCandidateAgents.mockResolvedValue(["Reporter"]);
        renderPanel();

        await openPicker(user);

        expect(await screen.findByText("Reporter")).toBeInTheDocument();
        expect(screen.queryByText("Archivist")).not.toBeInTheDocument();
    });

    test("the picker says so when no agent needs authorizing", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem], true));
        mockListToolAuthCandidateAgents.mockResolvedValue([]);
        renderPanel();

        await openPicker(user, { expand: false });
        expect(await screen.findByText(/No agent with an authorizable tool was found/)).toBeInTheDocument();
    });

    test("only a pinned agent can be removed, and removing it unpins it from the experiment", async () => {
        const user = userEvent.setup();
        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([notConnectedItem]), agentGroup("Reporter", [connectedItem]), agentGroup("Archivist", [notConnectedItem], { addedBy: "ops@test" })], true));
        mockRemoveExperimentToolAuthAgent.mockResolvedValue(undefined);
        renderPanel();

        expect(await screen.findByText("Delegates to: Archivist")).toBeInTheDocument();
        const removeButtons = screen.getAllByRole("button", { name: /^Remove / });
        expect(removeButtons).toHaveLength(1);
        expect(removeButtons[0]).toHaveAccessibleName("Remove Archivist");

        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([notConnectedItem]), agentGroup("Reporter", [connectedItem])], true));
        await user.click(removeButtons[0]);

        await waitFor(() => expect(mockRemoveExperimentToolAuthAgent).toHaveBeenCalledWith("exp-1", "Archivist"));
        await waitFor(() => expect(screen.queryByText("Delegates to: Archivist")).not.toBeInTheDocument());
        expect(mockDisconnectExperimentToolAuth).not.toHaveBeenCalled();
    });
});

describe("ConnectToolsPanel reduced access", () => {
    const only = (...granted: Capability[]) => ({ userHasCapability: (capability: Capability) => granted.includes(capability) });

    afterEach(() => {
        vi.clearAllMocks();
    });

    test("a read-only viewer sees statuses but none of the write controls", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([connectedItem, notConnectedItem]), agentGroup("Archivist", [notConnectedItem], { addedBy: "ops@test" })], true));
        renderPanel(undefined, only(CAPABILITIES.readEvaluations));

        expect(await screen.findByText("Connected")).toBeInTheDocument();
        expect(screen.getAllByText("Not connected")).toHaveLength(2);
        expect(screen.getByText("Delegates to: Archivist")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Authorize" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Disconnect" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Add Agent/ })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /^Remove / })).not.toBeInTheDocument();
    });

    test("a wildcard graph with nothing of its own hides from a viewer who cannot pin", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([], true));
        renderPanel(undefined, only(CAPABILITIES.readEvaluations, CAPABILITIES.invokeEvaluations));

        await waitFor(() => expect(mockGetExperimentToolAuth).toHaveBeenCalled());
        expect(screen.queryByTestId("connectToolsPanel")).not.toBeInTheDocument();
    });

    test("each write control follows its own permission", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(checklist([target([notConnectedItem]), agentGroup("Archivist", [notConnectedItem], { addedBy: "ops@test" })], true));
        renderPanel(undefined, only(CAPABILITIES.readEvaluations, CAPABILITIES.deleteEvaluations));

        expect(await screen.findByRole("button", { name: "Remove Archivist" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Authorize" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Add Agent/ })).not.toBeInTheDocument();
    });

    test("the principal copy comes from the response, not the prop", async () => {
        mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem], false, "system:actual-bot"));
        renderPanel("system:stale-prop");

        expect(await screen.findByTestId("connectToolsPrincipal")).toHaveTextContent("Runs as: system:actual-bot");
        expect(screen.queryByText("system:stale-prop")).not.toBeInTheDocument();
    });

    test("a pending flow polls until its credential reports connected", async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
            mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem]));
            mockBeginExperimentToolAuth.mockResolvedValue({ authUri: "https://idp.example.com/authorize", credentialKey: "sheets" });
            vi.spyOn(window, "open").mockReturnValue({ opener: null } as unknown as Window);
            renderPanel();

            await user.click(await screen.findByRole("button", { name: "Authorize" }));
            await waitFor(() => expect(mockBeginExperimentToolAuth).toHaveBeenCalledTimes(1));
            const callsAfterBegin = mockGetExperimentToolAuth.mock.calls.length;

            mockGetExperimentToolAuth.mockResolvedValue(targetOnly([{ ...notConnectedItem, status: "connected" }]));
            await vi.advanceTimersByTimeAsync(3_000);

            await waitFor(() => expect(screen.getByText("Connected")).toBeInTheDocument());
            expect(mockGetExperimentToolAuth.mock.calls.length).toBeGreaterThan(callsAfterBegin);

            const callsAfterResolve = mockGetExperimentToolAuth.mock.calls.length;
            await vi.advanceTimersByTimeAsync(9_000);
            expect(mockGetExperimentToolAuth.mock.calls.length).toBe(callsAfterResolve);
        } finally {
            vi.useRealTimers();
        }
    });

    test("the pending poll stops at the end of its window", async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
            mockGetExperimentToolAuth.mockResolvedValue(targetOnly([notConnectedItem]));
            mockBeginExperimentToolAuth.mockResolvedValue({ authUri: "https://idp.example.com/authorize", credentialKey: "sheets" });
            vi.spyOn(window, "open").mockReturnValue({ opener: null } as unknown as Window);
            renderPanel();

            await user.click(await screen.findByRole("button", { name: "Authorize" }));
            await waitFor(() => expect(mockBeginExperimentToolAuth).toHaveBeenCalledTimes(1));

            await vi.advanceTimersByTimeAsync(2 * 60 * 1000 + 3_000);
            const callsAfterWindow = mockGetExperimentToolAuth.mock.calls.length;
            await vi.advanceTimersByTimeAsync(30_000);
            expect(mockGetExperimentToolAuth.mock.calls.length).toBe(callsAfterWindow);
        } finally {
            vi.useRealTimers();
        }
    });
});
