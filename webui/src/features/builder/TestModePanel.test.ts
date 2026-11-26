// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type FunctionComponent } from "react";
import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { isChatTestable, isEffectiveTargetDeployed, isTestAgentHealthy, pickDefaultTestTarget, TestModePanel } from "./TestModePanel";

const panelDeps = vi.hoisted(() => ({
    testAgent: undefined as Record<string, unknown> | undefined,
    createMutate: vi.fn(),
    buildResponding: false,
}));

vi.mock("@/providers/contexts", async importOriginal => ({
    ...(await importOriginal<typeof import("@/providers/contexts")>()),
    useChatContext: () => ({ sessionId: "build-session", agents: [], isResponding: panelDeps.buildResponding }),
}));
vi.mock("@/api/agent-cards", () => ({ useAgentCards: () => ({ data: [] }) }));
vi.mock("@/api/builder", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/builder")>()),
    useTestAgentQuery: () => ({ isLoading: false, data: panelDeps.testAgent }),
    useCreateTestAgentMutation: () => ({ isPending: false, isError: false, data: undefined, error: undefined, mutate: panelDeps.createMutate, reset: vi.fn() }),
}));
vi.mock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => false }));
vi.mock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: () => new Map() }));
vi.mock("./hooks/useBuilderManifest", () => ({
    useBuilderManifest: () => ({ manifest: { components: [{ name: "Mule Detection", type: "workflow", filename: "wf.yaml", status: "deployed", origin: "session" }] } }),
}));
vi.mock("./hooks/useTestSessionLifecycle", () => ({ useTestSessionLifecycle: () => ({ testSessionId: null, sessionError: null, retry: vi.fn() }) }));

describe("isChatTestable", () => {
    it.each(["workflow", "agent", "skill", "a2a_agent"])("returns true for %s (the chat-testable set)", kind => {
        expect(isChatTestable(kind)).toBe(true);
    });

    it.each(["connector", "gateway", "tool", "event_mesh", "", "Agent", "workflow ", "skill_with_extra"])("returns false for %s (not chat-testable)", kind => {
        expect(isChatTestable(kind)).toBe(false);
    });
});

describe("pickDefaultTestTarget", () => {
    it("returns null for an undefined / empty manifest", () => {
        expect(pickDefaultTestTarget(undefined)).toBeNull();
        expect(pickDefaultTestTarget([])).toBeNull();
    });

    it("returns null when only platform-origin components exist", () => {
        expect(pickDefaultTestTarget([{ name: "PreExisting", type: "agent", origin: "platform" }])).toBeNull();
    });

    it("returns null when no session component is a chat-testable kind", () => {
        expect(pickDefaultTestTarget([{ name: "Conn", type: "connector", origin: "session" }])).toBeNull();
    });

    it("returns the session workflow / agent / skill, preferring workflow > agent > skill", () => {
        expect(pickDefaultTestTarget([{ name: "Sk", type: "skill", origin: "session" }])).toEqual({ type: "skill", name: "Sk" });
        expect(pickDefaultTestTarget([{ name: "Ag", type: "agent", origin: "session" }])).toEqual({ type: "agent", name: "Ag" });
        expect(
            pickDefaultTestTarget([
                { name: "Sk", type: "skill", origin: "session" },
                { name: "Ag", type: "agent", origin: "session" },
                { name: "Wf", type: "workflow", origin: "session" },
            ])
        ).toEqual({ type: "workflow", name: "Wf" });
    });

    it("selects an external A2A agent (preferred below agent, above skill)", () => {
        expect(pickDefaultTestTarget([{ name: "MultiplicationAgent", type: "a2a_agent", origin: "session" }])).toEqual({ type: "a2a_agent", name: "MultiplicationAgent" });
        expect(
            pickDefaultTestTarget([
                { name: "Sk", type: "skill", origin: "session" },
                { name: "A2A", type: "a2a_agent", origin: "session" },
            ])
        ).toEqual({ type: "a2a_agent", name: "A2A" });
    });

    it("enables on a created-but-not-yet-deployed component (deployment is not required)", () => {
        expect(pickDefaultTestTarget([{ name: "Ag", type: "agent", origin: "session", status: "validated" }])).toEqual({ type: "agent", name: "Ag" });
    });

    it("treats a missing origin as session (only origin === 'platform' is excluded)", () => {
        expect(pickDefaultTestTarget([{ name: "Ag", type: "agent" }])).toEqual({ type: "agent", name: "Ag" });
    });
});

const OWN_ID = "01a160d0-2474-7426-830a-ae0397fcdc9a";
const OWN_CARD = "agent_01a160d0_2474_7426_830a_ae0397fcdc9a";
const OWN_WORKFLOW_CARD = "workflow_01a160d0_2474_7426_830a_ae0397fcdc9a";
const OTHER_CARD = "agent_01a160ce_7f96_7e82_80dd_7e47b3032891";

describe("isEffectiveTargetDeployed", () => {
    const agent = { type: "agent", name: "DocSummarizer" } as const;
    const noEdits = { versions: undefined, serverApplyEnabled: false };

    it("returns false with no target", () => {
        expect(isEffectiveTargetDeployed(null, [{ name: "DocSummarizer", status: "deployed" }], [], noEdits)).toBe(false);
    });

    it("passes when the manifest marks the component deployed", () => {
        expect(isEffectiveTargetDeployed(agent, [{ name: "DocSummarizer", status: "deployed", origin: "session" }], [], noEdits)).toBe(true);
    });

    it("passes for a pre-existing platform-origin component regardless of status", () => {
        expect(isEffectiveTargetDeployed(agent, [{ name: "DocSummarizer", status: "validated", origin: "platform" }], [], noEdits)).toBe(true);
    });

    it("passes when the manifest lags (validated) but the agent's own recorded record is live on the mesh", () => {
        expect(isEffectiveTargetDeployed(agent, [{ id: OWN_ID, name: "DocSummarizer", type: "agent", status: "validated", origin: "session" }], [{ name: OWN_CARD }], noEdits)).toBe(true);
    });

    it("blocks a never-deployed session whose same-named component is live from another session — by name or displayName", () => {
        const comps = [{ name: "DocSummarizer", type: "agent", status: "draft", origin: "session" }];
        expect(isEffectiveTargetDeployed(agent, comps, [{ name: "DocSummarizer" }], noEdits)).toBe(false);
        expect(isEffectiveTargetDeployed(agent, comps, [{ name: OTHER_CARD, displayName: "DocSummarizer" }], noEdits)).toBe(false);
    });

    it("blocks a validated component with no recorded id even when a same-named card is live", () => {
        expect(isEffectiveTargetDeployed(agent, [{ name: "DocSummarizer", type: "agent", status: "validated", origin: "session" }], [{ name: OTHER_CARD, displayName: "DocSummarizer" }], noEdits)).toBe(false);
    });

    it("blocks a target the manifest does not list, even when a card carries its name", () => {
        expect(isEffectiveTargetDeployed(agent, [], [{ name: "DocSummarizer", displayName: "DocSummarizer" }], noEdits)).toBe(false);
        expect(isEffectiveTargetDeployed(agent, [{ id: OWN_ID, name: "Other", type: "agent", status: "deployed", origin: "session" }], [{ name: OWN_CARD }], noEdits)).toBe(false);
    });

    it("blocks a recorded id whose record was deleted or replaced by another session's same-named one", () => {
        expect(isEffectiveTargetDeployed(agent, [{ id: OWN_ID, name: "DocSummarizer", type: "agent", status: "validated", origin: "session" }], [{ name: OTHER_CARD, displayName: "DocSummarizer" }], noEdits)).toBe(false);
    });

    it("a legacy component with no id keeps passing on its deployed status", () => {
        expect(isEffectiveTargetDeployed(agent, [{ name: "DocSummarizer", type: "agent", status: "deployed", origin: "session" }], [], noEdits)).toBe(true);
    });

    it("blocks when the manifest is not deployed and nothing is live on the mesh", () => {
        expect(isEffectiveTargetDeployed(agent, [{ name: "DocSummarizer", status: "validated", origin: "session" }], [], noEdits)).toBe(false);
    });

    it("blocks a skill that is neither manifest-deployed nor on the A2A list (no regression)", () => {
        const skill = { type: "skill", name: "MySkill" } as const;
        expect(isEffectiveTargetDeployed(skill, [{ name: "MySkill", status: "validated", origin: "session" }], [{ name: "DocSummarizer" }], noEdits)).toBe(false);
        expect(isEffectiveTargetDeployed(skill, [{ name: "MySkill", status: "deployed", origin: "session" }], [], noEdits)).toBe(true);
    });

    it("does not let a non-agent/workflow kind match a live card, even one carrying its id", () => {
        const skill = { type: "skill", name: "WebSearch" } as const;
        expect(isEffectiveTargetDeployed(skill, [{ id: OWN_ID, name: "WebSearch", type: "skill", status: "validated", origin: "session" }], [{ name: OWN_CARD }], noEdits)).toBe(false);
        const wf = { type: "workflow", name: "WebSearch" } as const;
        expect(isEffectiveTargetDeployed(wf, [{ id: OWN_ID, name: "WebSearch", type: "workflow", filename: "w.yaml", status: "validated", origin: "session" }], [{ name: OWN_WORKFLOW_CARD }], noEdits)).toBe(true);
    });

    it("un-gates an external A2A agent whose own proxy record is live on the mesh (it publishes an A2A card)", () => {
        const a2a = { type: "a2a_agent", name: "MultiplicationAgent" } as const;
        expect(isEffectiveTargetDeployed(a2a, [{ id: OWN_ID, name: "MultiplicationAgent", type: "a2a_agent", status: "validated", origin: "session" }], [{ name: OWN_CARD }], noEdits)).toBe(true);
        expect(isEffectiveTargetDeployed(a2a, [{ name: "MultiplicationAgent", type: "a2a_agent", status: "validated", origin: "session" }], [], noEdits)).toBe(false);
    });
});

describe("isEffectiveTargetDeployed — undeployed edits", () => {
    const wf = { type: "workflow", name: "Detector" } as const;
    const DETECTOR_CARD = "workflow_01a160d0_2474_7426_830a_ae0397fcdc9a";
    const dirtyWorkflow = { id: "01a160d0-2474-7426-830a-ae0397fcdc9a", name: "Detector", type: "workflow", filename: "wf.yaml", status: "validated", origin: "session", deployed_version: 0 };
    const versions = new Map([["wf.yaml", 2]]);

    it("refuses a workflow that is live on the mesh but running a superseded config", () => {
        expect(isEffectiveTargetDeployed(wf, [dirtyWorkflow], [{ name: DETECTOR_CARD }], { versions, serverApplyEnabled: false })).toBe(false);
    });

    it("refuses a workflow the manifest still marks deployed while its config has moved on", () => {
        const stale = { ...dirtyWorkflow, status: "deployed" };
        expect(isEffectiveTargetDeployed(wf, [stale], [], { versions, serverApplyEnabled: false })).toBe(false);
    });

    it("still passes a workflow whose deployed_version matches the current artifact", () => {
        const clean = { ...dirtyWorkflow, deployed_version: 2 };
        expect(isEffectiveTargetDeployed(wf, [clean], [{ name: DETECTOR_CARD }], { versions, serverApplyEnabled: false })).toBe(true);
    });

    it("does NOT refuse a dirty agent on the per-kind path — nothing there can redeploy it", () => {
        const agent = { type: "agent", name: "Chat" } as const;
        const dirtyAgent = { name: "Chat", type: "agent", filename: "a.yaml", status: "deployed", origin: "session", deployed_version: 0 };
        expect(isEffectiveTargetDeployed(agent, [dirtyAgent], [], { versions: new Map([["a.yaml", 2]]), serverApplyEnabled: false })).toBe(true);
    });

    it("DOES refuse that same agent under server-side apply, where Build & Activate is offered", () => {
        const agent = { type: "agent", name: "Chat" } as const;
        const dirtyAgent = { name: "Chat", type: "agent", filename: "a.yaml", status: "deployed", origin: "session", deployed_version: 0 };
        expect(isEffectiveTargetDeployed(agent, [dirtyAgent], [], { versions: new Map([["a.yaml", 2]]), serverApplyEnabled: true })).toBe(false);
    });

    it("does not refuse a deployed workflow whose manifest carries no deployed_version", () => {
        const noStamp = { name: "Detector", type: "workflow", filename: "wf.yaml", status: "deployed", origin: "session" };
        expect(isEffectiveTargetDeployed(wf, [noStamp], [], { versions, serverApplyEnabled: false })).toBe(true);
    });

    it("does not refuse while the versions map has not resolved the component yet", () => {
        expect(isEffectiveTargetDeployed(wf, [{ ...dirtyWorkflow, status: "deployed" }], [], { versions: new Map(), serverApplyEnabled: false })).toBe(true);
    });

    it("leaves a platform-origin component alone — it has no session artifact to be stale against", () => {
        const platform = { type: "workflow", name: "Existing" } as const;
        expect(isEffectiveTargetDeployed(platform, [{ name: "Existing", type: "workflow", filename: "", status: "validated", origin: "platform" }], [], { versions, serverApplyEnabled: true })).toBe(true);
    });
});

describe("isTestAgentHealthy", () => {
    it("is healthy when deployed and running", () => {
        expect(isTestAgentHealthy("deployed", "running")).toBe(true);
    });

    it("is healthy when deployed and degraded — still up, just reporting a problem", () => {
        expect(isTestAgentHealthy("deployed", "degraded")).toBe(true);
    });

    it("is healthy when deployed and starting — just created, no card yet", () => {
        expect(isTestAgentHealthy("deployed", "starting")).toBe(true);
    });

    it("is not healthy when deployed but disconnected", () => {
        expect(isTestAgentHealthy("deployed", "disconnected")).toBe(false);
    });

    it("is not healthy when deployed but stopped", () => {
        expect(isTestAgentHealthy("deployed", "stopped")).toBe(false);
    });

    it("is not healthy when not deployed, regardless of runtimeStatus", () => {
        expect(isTestAgentHealthy("not_deployed", "running")).toBe(false);
        expect(isTestAgentHealthy(undefined, "degraded")).toBe(false);
    });

    it("is not healthy when deployed but runtimeStatus is absent", () => {
        expect(isTestAgentHealthy("deployed", undefined)).toBe(false);
    });
});

describe("TestModePanel test-agent create", () => {
    const runningTestAgent = (sourceComponentName: string) => ({
        name: "test-agent",
        sourceComponentName,
        sourceComponentKind: "workflow",
        deploymentStatus: "deployed",
        runtimeStatus: { status: "running" },
    });
    const Panel = TestModePanel as FunctionComponent<NonNullable<Parameters<typeof TestModePanel>[0]>>;
    const panelAddressedAs = (name: string) => createElement(Panel, { resolveAddressableName: () => name });

    beforeEach(() => {
        panelDeps.testAgent = undefined;
        panelDeps.buildResponding = false;
        panelDeps.createMutate.mockReset();
    });

    it("re-creates the test agent when the addressable name changes under the same manifest target", () => {
        const { rerender } = render(panelAddressedAs("old_name"));
        expect(panelDeps.createMutate).toHaveBeenCalledTimes(1);
        expect(panelDeps.createMutate).toHaveBeenLastCalledWith({ sessionId: "build-session", componentName: "old_name", componentKind: "workflow" });

        panelDeps.testAgent = runningTestAgent("old_name");
        rerender(panelAddressedAs("old_name"));
        expect(panelDeps.createMutate).toHaveBeenCalledTimes(1);

        rerender(panelAddressedAs("NewName"));
        expect(panelDeps.createMutate).toHaveBeenCalledTimes(2);
        expect(panelDeps.createMutate).toHaveBeenLastCalledWith({ sessionId: "build-session", componentName: "NewName", componentKind: "workflow" });
    });

    describe("while the build chat is responding", () => {
        const streamPartialName = (rerender: (ui: ReturnType<typeof panelAddressedAs>) => void) => {
            panelDeps.buildResponding = true;
            rerender(panelAddressedAs("X_par"));
            rerender(panelAddressedAs("X_partial_na"));
            panelDeps.buildResponding = false;
        };

        beforeEach(() => {
            panelDeps.testAgent = runningTestAgent("X");
        });

        it("creates nothing for partial names and nothing once the name settles back", () => {
            const { rerender } = render(panelAddressedAs("X"));
            streamPartialName(rerender);
            expect(panelDeps.createMutate).not.toHaveBeenCalled();

            rerender(panelAddressedAs("X"));
            expect(panelDeps.createMutate).not.toHaveBeenCalled();
        });

        it("creates exactly once for the settled name when the stream renamed the target", () => {
            const { rerender } = render(panelAddressedAs("X"));
            streamPartialName(rerender);
            expect(panelDeps.createMutate).not.toHaveBeenCalled();

            rerender(panelAddressedAs("Y"));
            rerender(panelAddressedAs("Y"));
            expect(panelDeps.createMutate).toHaveBeenCalledTimes(1);
            expect(panelDeps.createMutate).toHaveBeenLastCalledWith({ sessionId: "build-session", componentName: "Y", componentKind: "workflow" });
        });
    });
});
