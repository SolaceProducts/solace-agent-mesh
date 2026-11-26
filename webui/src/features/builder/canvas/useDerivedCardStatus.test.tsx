// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, test } from "vitest";

import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuilderActiveTab } from "../useBuilderPageState";

import { CanvasViewProvider } from "./canvasViewContext";
import { useDerivedCardStatus } from "./useDerivedCardStatus";

function dataFor(overrides: Partial<ComponentCanvasData> = {}): ComponentCanvasData {
    return {
        name: "A",
        filename: "a.yaml",
        type: "agent",
        yamlText: null,
        isLoading: false,
        isGenerating: false,
        isQueued: false,
        isWaitingForInput: false,
        isBuildingDeps: false,
        isBuilderResponding: false,
        dependencyProgress: [],
        ...overrides,
    };
}

const OWN_ID = "01a160d0-2474-7426-830a-ae0397fcdc9a";
const OWN_CARD = { name: "agent_01a160d0_2474_7426_830a_ae0397fcdc9a", displayName: "DocSummarizer" };
const OTHER_SESSION_CARD = { name: "agent_01a160ce_7f96_7e82_80dd_7e47b3032891", displayName: "DocSummarizer" };

interface WrapperOpts {
    isApplyingChanges?: boolean;
    builderActiveTab?: BuilderActiveTab;
    liveAgentCards?: ReadonlyArray<{ name?: string; displayName?: string }>;
    liveConnectors?: ReadonlyArray<{ name?: string }>;
}

function wrap({ isApplyingChanges = false, builderActiveTab = "build", liveAgentCards = undefined, liveConnectors = undefined }: WrapperOpts = {}) {
    return ({ children }: { children: ReactNode }) => (
        <CanvasViewProvider builderActiveTab={builderActiveTab} testTargetName={null} onSelectTestTarget={() => {}} isApplyingChanges={isApplyingChanges} liveAgentCards={liveAgentCards} liveConnectors={liveConnectors}>
            {children}
        </CanvasViewProvider>
    );
}

describe("useDerivedCardStatus.isEditing", () => {
    test("regular update: isGenerating + status=deployed → Editing", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ isGenerating: true }), { status: "deployed" }), { wrapper: wrap() });
        expect(result.current.isEditing).toBe(true);
    });

    test("apply pass: isGenerating + status=validating + isApplyingChanges → Editing (regression)", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ isGenerating: true }), { status: "validating" }), { wrapper: wrap({ isApplyingChanges: true }) });
        expect(result.current.isEditing).toBe(true);
    });

    test("apply pass: isGenerating + status=draft + isApplyingChanges → Editing (fresh draft component)", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ isGenerating: true }), { status: "draft" }), { wrapper: wrap({ isApplyingChanges: true }) });
        expect(result.current.isEditing).toBe(true);
    });

    test("not streaming: isGenerating=false never produces Editing, even mid-apply", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ isGenerating: false }), { status: "deployed" }), { wrapper: wrap({ isApplyingChanges: true }) });
        expect(result.current.isEditing).toBe(false);
    });

    test("fresh build: isGenerating + status=draft + no apply → Creating, not Editing", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ isGenerating: true }), { status: "draft" }), { wrapper: wrap({ isApplyingChanges: false }) });
        expect(result.current.isEditing).toBe(false);
    });

    test("platform-origin component: status is forced to deployed", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ isGenerating: true }), { status: "draft", origin: "platform" }), { wrapper: wrap() });
        expect(result.current.effectiveStatus).toBe("deployed");
        expect(result.current.isEditing).toBe(true);
    });

    test("live on the mesh: a validated component is treated as deployed → Active", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ id: OWN_ID, name: "DocSummarizer" }), { id: OWN_ID, name: "DocSummarizer", type: "agent", status: "validated" }), {
            wrapper: wrap({ liveAgentCards: [OWN_CARD] }),
        });
        expect(result.current.effectiveStatus).toBe("deployed");
        expect(result.current.isActive).toBe(true);
        expect(result.current.isReady).toBe(false);
    });

    test("validated but still finalizing: reads in-progress, NOT Ready — the fix", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ status: "validated", origin: "session", isBuildFinalizing: true })), { wrapper: wrap() });
        expect(result.current.isReady).toBe(false);
        expect(result.current.isActive).toBe(false);
        expect(result.current.isGenerating).toBe(true);
        expect(result.current.isIdle).toBe(false);
    });

    test("live on the mesh wins over finalizing: stays Active, not degraded to in-progress", () => {
        const { result } = renderHook(
            () => useDerivedCardStatus(dataFor({ id: OWN_ID, name: "DocSummarizer", status: "validated", origin: "session", isBuildFinalizing: true }), { id: OWN_ID, name: "DocSummarizer", type: "agent", status: "validated" }),
            {
                wrapper: wrap({ liveAgentCards: [OWN_CARD] }),
            }
        );
        expect(result.current.effectiveStatus).toBe("deployed");
        expect(result.current.isActive).toBe(true);
        expect(result.current.isGenerating).toBe(false);
    });

    test("draft but finalizing: reads in-progress, NOT Draft — the backward-flip fix", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ status: "draft", origin: "session", isBuildFinalizing: true })), { wrapper: wrap() });
        expect(result.current.isDraft).toBe(false);
        expect(result.current.isGenerating).toBe(true);
        expect(result.current.isIdle).toBe(false);
    });

    test("validated and settled (not finalizing): stays Ready", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ status: "validated", origin: "session", isBuildFinalizing: false })), { wrapper: wrap() });
        expect(result.current.isReady).toBe(true);
        expect(result.current.isGenerating).toBe(false);
    });

    test("not live on the mesh: a validated component stays Ready", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ name: "DocSummarizer" }), { name: "DocSummarizer", status: "validated" }), {
            wrapper: wrap({ liveAgentCards: [{ name: "SomethingElse" }] }),
        });
        expect(result.current.effectiveStatus).toBe("validated");
        expect(result.current.isReady).toBe(true);
        expect(result.current.isActive).toBe(false);
    });

    test("a same-named component another session deployed is not this session's: stays Draft (never deployed here)", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ name: "DocSummarizer", status: "draft", origin: "session" }), { name: "DocSummarizer", type: "agent", status: "draft", origin: "session" }), {
            wrapper: wrap({ liveAgentCards: [OTHER_SESSION_CARD] }),
        });
        expect(result.current.effectiveStatus).toBe("draft");
        expect(result.current.isActive).toBe(false);
        expect(result.current.isDraft).toBe(true);
    });

    test("a same-named live card does not make a validated component without a recorded id Active", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ name: "DocSummarizer" }), { name: "DocSummarizer", type: "agent", status: "validated" }), {
            wrapper: wrap({ liveAgentCards: [OTHER_SESSION_CARD] }),
        });
        expect(result.current.effectiveStatus).toBe("validated");
        expect(result.current.isActive).toBe(false);
        expect(result.current.isReady).toBe(true);
    });

    test("a recorded id whose record is gone or replaced by another session's same-named one is not live", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ id: OWN_ID, name: "DocSummarizer" }), { id: OWN_ID, name: "DocSummarizer", type: "agent", status: "validated" }), {
            wrapper: wrap({ liveAgentCards: [OTHER_SESSION_CARD] }),
        });
        expect(result.current.isActive).toBe(false);
        expect(result.current.isReady).toBe(true);
    });

    test("the id comes from the canvas data when the card passes no component (standalone agent card)", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ id: OWN_ID, name: "DocSummarizer", status: "validated" })), { wrapper: wrap({ liveAgentCards: [OWN_CARD] }) });
        expect(result.current.isActive).toBe(true);
    });

    test("an external A2A agent whose own proxy record is live flips to Active (it publishes an A2A card)", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ id: OWN_ID, name: "MultiplicationAgent", type: "a2a_agent" }), { id: OWN_ID, name: "MultiplicationAgent", type: "a2a_agent", status: "validated" }), {
            wrapper: wrap({ liveAgentCards: [OWN_CARD] }),
        });
        expect(result.current.effectiveStatus).toBe("deployed");
        expect(result.current.isActive).toBe(true);
    });

    test("a workflow is live on its own workflow_ instance, not on an agent card carrying the same id", () => {
        const workflowCard = { name: "workflow_01a160d0_2474_7426_830a_ae0397fcdc9a" };
        const live = renderHook(() => useDerivedCardStatus(dataFor({ id: OWN_ID, name: "Flow", type: "workflow" }), { id: OWN_ID, name: "Flow", type: "workflow", status: "validated" }), { wrapper: wrap({ liveAgentCards: [workflowCard] }) });
        expect(live.result.current.isActive).toBe(true);
        const agentCardOnly = renderHook(() => useDerivedCardStatus(dataFor({ id: OWN_ID, name: "Flow", type: "workflow" }), { id: OWN_ID, name: "Flow", type: "workflow", status: "validated" }), { wrapper: wrap({ liveAgentCards: [OWN_CARD] }) });
        expect(agentCardOnly.result.current.isActive).toBe(false);
    });

    test("a legacy component with no id and status deployed keeps reading Active from its own status", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ name: "DocSummarizer", status: "deployed", origin: "session" }), { name: "DocSummarizer", type: "agent", status: "deployed", origin: "session" }), {
            wrapper: wrap({ liveAgentCards: [] }),
        });
        expect(result.current.isActive).toBe(true);
    });

    test("does not flip a non-agent/workflow card to Active on a name collision", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ name: "WebSearch", type: "skill" }), { name: "WebSearch", type: "skill", status: "validated" }), {
            wrapper: wrap({ liveAgentCards: [{ name: "WebSearch" }] }),
        });
        expect(result.current.effectiveStatus).toBe("validated");
        expect(result.current.isReady).toBe(true);
        expect(result.current.isActive).toBe(false);
    });

    test("draft: session component with status='draft' is Draft", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ status: "draft", origin: "session" })), { wrapper: wrap() });
        expect(result.current.isDraft).toBe(true);
        expect(result.current.isReady).toBe(false);
        expect(result.current.isActive).toBe(false);
    });

    test("draft: a MISSING status still reads as Draft (regression)", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ status: undefined, origin: "session" })), { wrapper: wrap() });
        expect(result.current.isDraft).toBe(true);
    });

    test("draft: validated/deployed/error/generating are NOT Draft", () => {
        const validated = renderHook(() => useDerivedCardStatus(dataFor({ status: "validated", origin: "session" })), { wrapper: wrap() });
        expect(validated.result.current.isDraft).toBe(false);
        const deployed = renderHook(() => useDerivedCardStatus(dataFor({ status: "deployed", origin: "session" })), { wrapper: wrap() });
        expect(deployed.result.current.isDraft).toBe(false);
        const errored = renderHook(() => useDerivedCardStatus(dataFor({ status: "error", origin: "session" })), { wrapper: wrap() });
        expect(errored.result.current.isDraft).toBe(false);
        const generating = renderHook(() => useDerivedCardStatus(dataFor({ status: "draft", origin: "session", isGenerating: true })), { wrapper: wrap() });
        expect(generating.result.current.isDraft).toBe(false);
    });

    test("draft: a platform-origin component is never Draft", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ status: undefined, origin: "platform" }), { origin: "platform", status: undefined }), { wrapper: wrap() });
        expect(result.current.isDraft).toBe(false);
        expect(result.current.isActive).toBe(true);
    });

    test("draft: platform origin via canvasData alone (no component arg) is never Draft", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ status: undefined, origin: "platform" })), { wrapper: wrap() });
        expect(result.current.isDraft).toBe(false);
    });

    test("Test mode collapses all status to idle, even when streaming", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ isGenerating: true }), { status: "deployed" }), { wrapper: wrap({ builderActiveTab: "test", isApplyingChanges: true }) });
        expect(result.current.isEditing).toBe(false);
        expect(result.current.isGenerating).toBe(false);
        expect(result.current.isIdle).toBe(true);
        expect(result.current.effectiveStatus).toBeUndefined();
    });
});

describe("useDerivedCardStatus — the interrupted-build reading", () => {
    interface Row {
        name: string;
        data: Partial<ComponentCanvasData>;
        component?: { name?: string; type?: string; origin?: string; status?: string };
        interrupted: boolean;
        tab?: BuilderActiveTab;
        liveConnectors?: ReadonlyArray<{ name?: string }>;
        showsQueued: boolean;
        showsDraft: boolean;
        statusText: string | undefined;
    }

    const ROWS: Row[] = [
        { name: "an unfinished draft in an interrupted build", data: { status: "draft" }, interrupted: true, showsQueued: false, showsDraft: false, statusText: "Paused" },
        { name: "a component with no status in an interrupted build", data: { status: undefined }, interrupted: true, showsQueued: false, showsDraft: false, statusText: "Paused" },
        { name: "a queued component in an interrupted build", data: { status: "draft", isQueued: true }, interrupted: true, showsQueued: false, showsDraft: false, statusText: "Paused" },
        { name: "a queued component in a live turn still reads queued", data: { status: "draft", isQueued: true }, interrupted: false, showsQueued: true, showsDraft: false, statusText: undefined },
        { name: "the same draft when the build is not interrupted", data: { status: "draft" }, interrupted: false, showsQueued: false, showsDraft: true, statusText: undefined },
        { name: "a validated component in an interrupted build", data: { status: "validated" }, interrupted: true, showsQueued: false, showsDraft: false, statusText: undefined },
        { name: "a deployed component in an interrupted build", data: { status: "deployed" }, interrupted: true, showsQueued: false, showsDraft: false, statusText: undefined },
        { name: "a failed component in an interrupted build", data: { status: "error" }, interrupted: true, showsQueued: false, showsDraft: false, statusText: undefined },
        { name: "a generating component", data: { status: "draft", isGenerating: true }, interrupted: true, showsQueued: false, showsDraft: false, statusText: undefined },
        { name: "a waiting component beside an interrupted build reads the wait, not Paused", data: { status: "draft", isWaitingForInput: true }, interrupted: true, showsQueued: false, showsDraft: false, statusText: "Waiting for Input..." },
        { name: "a waiting component in a build that is not interrupted", data: { status: "validated", isWaitingForInput: true }, interrupted: false, showsQueued: false, showsDraft: false, statusText: "Waiting for Input..." },
        {
            name: "a platform-origin draft in an interrupted build is live, not unfinished",
            data: { status: "draft", origin: "platform" },
            component: { origin: "platform", status: "draft" },
            interrupted: true,
            showsQueued: false,
            showsDraft: false,
            statusText: undefined,
        },
        {
            name: "a validated connector whose save never landed in an interrupted build",
            data: { name: "c", type: "connector", status: "validated" },
            component: { name: "c", type: "connector", status: "validated", origin: "session" },
            liveConnectors: [],
            interrupted: true,
            showsQueued: false,
            showsDraft: false,
            statusText: "Paused",
        },
        { name: "Test mode shows no status chrome in an interrupted build", data: { status: "draft" }, interrupted: true, tab: "test", showsQueued: false, showsDraft: false, statusText: undefined },
    ];

    test.each(ROWS)("$name", row => {
        const wrapper = ({ children }: { children: ReactNode }) => (
            <CanvasViewProvider builderActiveTab={row.tab ?? "build"} testTargetName={null} onSelectTestTarget={() => {}} isApplyingChanges={false} buildInterrupted={row.interrupted} liveConnectors={row.liveConnectors}>
                {children}
            </CanvasViewProvider>
        );
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ origin: "session", ...row.data }), row.component), { wrapper });

        expect({ showsDraft: result.current.showsDraft, showsQueued: result.current.showsQueued, statusText: result.current.statusText }).toEqual({ showsDraft: row.showsDraft, showsQueued: row.showsQueued, statusText: row.statusText });
    });

    test("the raw isDraft the workflow pill reads is untouched by the verdict", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ status: "draft", origin: "session" })), {
            wrapper: ({ children }: { children: ReactNode }) => (
                <CanvasViewProvider builderActiveTab="build" testTargetName={null} onSelectTestTarget={() => {}} isApplyingChanges={false} buildInterrupted>
                    {children}
                </CanvasViewProvider>
            ),
        });
        expect(result.current.isDraft).toBe(true);
    });
});

describe("useDerivedCardStatus — connector save-success gate", () => {
    const connector = { name: "slack-sales", type: "connector", status: "validated", origin: "session" };

    test("a validated connector ABSENT from the live list reads Draft, not Ready (failed save) — the fix", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ name: "slack-sales", type: "connector", status: "validated", origin: "session" }), connector), { wrapper: wrap({ liveConnectors: [] }) });
        expect(result.current.isReady).toBe(false);
        expect(result.current.isDraft).toBe(true);
    });

    test("a validated connector PRESENT in the live list reads Ready (real save)", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ name: "slack-sales", type: "connector", status: "validated", origin: "session" }), connector), { wrapper: wrap({ liveConnectors: [{ name: "slack-sales" }] }) });
        expect(result.current.isReady).toBe(true);
    });

    test("matches by normalized name (manifest 'SlackSales' vs platform 'slack_sales')", () => {
        const c = { name: "SlackSales", type: "connector", status: "validated", origin: "session" };
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ name: "SlackSales", type: "connector", status: "validated", origin: "session" }), c), { wrapper: wrap({ liveConnectors: [{ name: "slack_sales" }] }) });
        expect(result.current.isReady).toBe(true);
    });

    test("stays optimistic (Ready) while the connectors list is still loading (undefined)", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ name: "slack-sales", type: "connector", status: "validated", origin: "session" }), connector), { wrapper: wrap({ liveConnectors: undefined }) });
        expect(result.current.isReady).toBe(true);
    });
});

describe("useDerivedCardStatus — changed since validation", () => {
    const validated = { name: "A", type: "agent", status: "validated", origin: "session" };

    test("an idle validated component edited since the stamp is not Ready, and says why", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ status: "validated", origin: "session", changedSinceValidation: true }), validated), { wrapper: wrap() });
        expect(result.current.isChangedSinceValidation).toBe(true);
        expect(result.current.isReady).toBe(false);
        expect(result.current.statusText).toBe("Changed since validation");
    });

    test("the same component, unchanged, reads Ready", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ status: "validated", origin: "session" }), validated), { wrapper: wrap() });
        expect(result.current.isChangedSinceValidation).toBe(false);
        expect(result.current.isReady).toBe(true);
        expect(result.current.statusText).toBeUndefined();
    });

    test("a deployed component edited since the stamp stays Active", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ status: "deployed", origin: "session", changedSinceValidation: true }), { ...validated, status: "deployed" }), { wrapper: wrap() });
        expect(result.current.isActive).toBe(true);
        expect(result.current.isChangedSinceValidation).toBe(false);
        expect(result.current.statusText).toBeUndefined();
    });

    test("Test mode shows no status chrome for it either", () => {
        const { result } = renderHook(() => useDerivedCardStatus(dataFor({ status: "validated", origin: "session", changedSinceValidation: true }), validated), { wrapper: wrap({ builderActiveTab: "test" }) });
        expect(result.current.isChangedSinceValidation).toBe(false);
        expect(result.current.statusText).toBeUndefined();
    });
});
