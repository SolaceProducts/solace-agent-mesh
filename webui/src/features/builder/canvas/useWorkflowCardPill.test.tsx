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
import { type DerivedCardStatus } from "./useDerivedCardStatus";
import { deriveWorkflowCardPill, useWorkflowCardPill, type WorkflowCardPillExtras } from "./useWorkflowCardPill";

const IDLE: DerivedCardStatus = {
    isGenerating: false,
    isQueued: false,
    isWaitingForInput: false,
    isBuildingDeps: false,
    isIdle: true,
    isActive: false,
    isReady: false,
    isChangedSinceValidation: false,
    isDraft: false,
    isEditing: false,
    showsDraft: false,
    showsQueued: false,
    statusText: undefined,
    effectiveStatus: undefined,
};

const OWN_ID = "01a160d0-2474-7426-830a-ae0397fcdc9a";
const OWN_CARD_NAME = "agent_01a160d0_2474_7426_830a_ae0397fcdc9a";

const derived = (over: Partial<DerivedCardStatus>): DerivedCardStatus => ({ ...IDLE, ...over });
const extras = (over: Partial<WorkflowCardPillExtras> = {}): WorkflowCardPillExtras => ({ buildInterrupted: false, ...over });

describe("deriveWorkflowCardPill", () => {
    test("idle deployed component reads Active", () => {
        const pill = deriveWorkflowCardPill(derived({ isActive: true, effectiveStatus: "deployed" }), extras());
        expect(pill).toMatchObject({ active: true, ready: false, generating: false, editing: false, draft: false });
    });

    test("idle validated component reads Ready, not Active", () => {
        const pill = deriveWorkflowCardPill(derived({ isReady: true, effectiveStatus: "validated" }), extras());
        expect(pill).toMatchObject({ ready: true, active: false });
    });

    test("a validated component edited since the stamp reads Changed since validation, not Ready", () => {
        const pill = deriveWorkflowCardPill(derived({ isChangedSinceValidation: true, isDraft: true, effectiveStatus: "validated" }), extras());
        expect(pill).toMatchObject({ ready: false, active: false, statusText: "Changed since validation" });
    });

    test("regenerating deployed component reads Editing", () => {
        const pill = deriveWorkflowCardPill(derived({ isGenerating: true, isEditing: true, isIdle: false, effectiveStatus: "deployed" }), extras());
        expect(pill).toMatchObject({ generating: true, editing: true });
    });

    test("fresh generation stays Creating, never Editing", () => {
        const pill = deriveWorkflowCardPill(derived({ isGenerating: true, isIdle: false, effectiveStatus: "draft" }), extras());
        expect(pill).toMatchObject({ generating: true, editing: false });
    });

    test("queued and draft cards map straight through", () => {
        expect(deriveWorkflowCardPill(derived({ isQueued: true, isIdle: false }), extras()).queued).toBe(true);
        expect(deriveWorkflowCardPill(derived({ isDraft: true, effectiveStatus: "draft" }), extras()).draft).toBe(true);
    });

    test("a failed component reads Error", () => {
        expect(deriveWorkflowCardPill(derived({ effectiveStatus: "error" }), extras()).error).toBe(true);
    });

    test("an in-flight regeneration wins over a stale Error", () => {
        const pill = deriveWorkflowCardPill(derived({ isGenerating: true, isIdle: false, effectiveStatus: "error" }), extras());
        expect(pill).toMatchObject({ error: false, generating: true });
    });

    test("waiting for input shows the waiting text and suppresses terminal and error pills", () => {
        const pill = deriveWorkflowCardPill(derived({ isWaitingForInput: true, isIdle: false, effectiveStatus: "error" }), extras());
        expect(pill).toMatchObject({ statusText: "Waiting for Input...", error: false, active: false, ready: false });
    });

    test("a connector dependency waiting for input shows the waiting text and holds back Active", () => {
        const pill = deriveWorkflowCardPill(derived({ isActive: true, effectiveStatus: "deployed" }), extras({ depWaitingForInput: true }));
        expect(pill).toMatchObject({ statusText: "Waiting for Input...", active: false, queued: false });
    });

    test("a connector dependency waiting for input also holds back Draft", () => {
        const pill = deriveWorkflowCardPill(derived({ isDraft: true, effectiveStatus: "draft" }), extras({ depWaitingForInput: true }));
        expect(pill).toMatchObject({ statusText: "Waiting for Input...", draft: false });
    });

    test("building dependencies shows the pill and holds back queued, draft and done", () => {
        const pill = deriveWorkflowCardPill(derived({ isBuildingDeps: true, isQueued: true, isIdle: false, isDraft: false }), extras());
        expect(pill).toMatchObject({ buildingDeps: true, queued: false, active: false, ready: false });
    });

    test("waiting for input holds back the building-dependencies flag", () => {
        const pill = deriveWorkflowCardPill(derived({ isWaitingForInput: true, isBuildingDeps: true, isIdle: false }), extras());
        expect(pill.buildingDeps).toBe(false);
    });

    test("an interrupted build reads Paused instead of Creating or Draft", () => {
        const queued = deriveWorkflowCardPill(derived({ isQueued: true, isIdle: false }), extras({ buildInterrupted: true }));
        expect(queued).toMatchObject({ statusText: "Paused", queued: false });
        const draft = deriveWorkflowCardPill(derived({ isDraft: true, effectiveStatus: "draft" }), extras({ buildInterrupted: true }));
        expect(draft).toMatchObject({ statusText: "Paused", draft: false });
    });

    test("a dependency wait wins over Paused", () => {
        const pill = deriveWorkflowCardPill(derived({ isDraft: true, effectiveStatus: "draft" }), extras({ depWaitingForInput: true, buildInterrupted: true }));
        expect(pill.statusText).toBe("Waiting for Input...");
    });

    test("waiting for input wins over generating on the text, and the card still reads generating", () => {
        const pill = deriveWorkflowCardPill(derived({ isWaitingForInput: true, isGenerating: true, isIdle: false }), extras());
        expect(pill).toMatchObject({ statusText: "Waiting for Input...", generating: true });
    });

    test("an interrupted build does not mark a finished component Paused", () => {
        const pill = deriveWorkflowCardPill(derived({ isActive: true, effectiveStatus: "deployed" }), extras({ buildInterrupted: true }));
        expect(pill).toMatchObject({ statusText: undefined, active: true });
    });

    describe("workflow start card with unfinished child agents", () => {
        const DEPLOYED = derived({ isActive: true, effectiveStatus: "deployed" });
        const VALIDATED = derived({ isReady: true, effectiveStatus: "validated" });
        const building = { pendingChildren: { builderResponding: true } };
        const idle = { pendingChildren: { builderResponding: false } };

        test("a deployed workflow reads Editing while the builder is working on a child", () => {
            const pill = deriveWorkflowCardPill(DEPLOYED, extras(building));
            expect(pill).toMatchObject({ generating: true, editing: true, active: false, ready: false });
        });

        test("a validated, undeployed workflow reads Creating while the builder is working on a child", () => {
            const pill = deriveWorkflowCardPill(VALIDATED, extras(building));
            expect(pill).toMatchObject({ generating: true, editing: false, active: false, ready: false });
        });

        test("children being done leaves the workflow Active", () => {
            const pill = deriveWorkflowCardPill(DEPLOYED, extras({ pendingChildren: undefined }));
            expect(pill).toMatchObject({ active: true, generating: false });
        });

        test("an interrupted build reads Paused on a deployed workflow with pending children, not Editing", () => {
            const pill = deriveWorkflowCardPill(DEPLOYED, extras({ ...idle, buildInterrupted: true }));
            expect(pill).toMatchObject({ statusText: "Paused", generating: false, editing: false, active: false, ready: false });
        });

        test("an interrupted build reads Paused on a validated workflow with pending children, not Creating", () => {
            const pill = deriveWorkflowCardPill(VALIDATED, extras({ ...idle, buildInterrupted: true }));
            expect(pill).toMatchObject({ statusText: "Paused", generating: false, editing: false, active: false, ready: false });
        });

        test("a responding builder wins over an interrupted flag on pending children: the workflow keeps working, never Paused", () => {
            const pill = deriveWorkflowCardPill(DEPLOYED, extras({ ...building, buildInterrupted: true }));
            expect(pill).toMatchObject({ statusText: undefined, generating: true, editing: true, active: false });
        });

        test("an interrupted build with every child done leaves the workflow Ready without Paused", () => {
            const pill = deriveWorkflowCardPill(VALIDATED, extras({ pendingChildren: undefined, buildInterrupted: true }));
            expect(pill).toMatchObject({ statusText: undefined, ready: true, generating: false });
        });

        test("an idle builder with no sign of a stopped build leaves a deployed workflow Active, neither spinning nor Paused", () => {
            const pill = deriveWorkflowCardPill(DEPLOYED, extras({ ...idle, buildInterrupted: false }));
            expect(pill).toMatchObject({ statusText: undefined, active: true, generating: false, editing: false });
        });

        test("an idle builder with no sign of a stopped build leaves a validated workflow Ready, neither spinning nor Paused", () => {
            const pill = deriveWorkflowCardPill(VALIDATED, extras({ ...idle, buildInterrupted: false }));
            expect(pill).toMatchObject({ statusText: undefined, ready: true, generating: false, editing: false });
        });

        test("a responding builder with every child done leaves the workflow Ready", () => {
            const pill = deriveWorkflowCardPill(VALIDATED, extras({ pendingChildren: undefined }));
            expect(pill).toMatchObject({ statusText: undefined, ready: true, generating: false });
        });

        test("a dependency wait wins over Paused on a workflow with pending children", () => {
            const pill = deriveWorkflowCardPill(VALIDATED, extras({ ...idle, buildInterrupted: true, depWaitingForInput: true }));
            expect(pill.statusText).toBe("Waiting for Input...");
        });

        test("pending children do not make an unbuilt draft workflow generate", () => {
            const pill = deriveWorkflowCardPill(derived({ isDraft: true, effectiveStatus: "draft" }), extras(building));
            expect(pill).toMatchObject({ generating: false, draft: true });
        });
    });
});

function canvasData(over: Partial<ComponentCanvasData> = {}): ComponentCanvasData {
    return {
        name: "UiOrders",
        filename: "ui-orders.yaml",
        type: "agent",
        status: "deployed",
        origin: "session",
        yamlText: null,
        isLoading: false,
        isGenerating: false,
        isQueued: false,
        isWaitingForInput: false,
        isBuildingDeps: false,
        isBuilderResponding: false,
        dependencyProgress: [],
        ...over,
    };
}

function wrap(opts: { tab?: BuilderActiveTab; isApplyingChanges?: boolean; liveAgentCards?: ReadonlyArray<{ name?: string; displayName?: string }> } = {}) {
    return ({ children }: { children: ReactNode }) => (
        <CanvasViewProvider builderActiveTab={opts.tab ?? "build"} testTargetName={null} onSelectTestTarget={() => {}} isApplyingChanges={opts.isApplyingChanges ?? false} liveAgentCards={opts.liveAgentCards}>
            {children}
        </CanvasViewProvider>
    );
}

describe("useWorkflowCardPill", () => {
    const component = { name: "UiOrders", type: "agent", origin: "session", status: "deployed" };

    test("a deployed agent being regenerated reads Editing, not Creating", () => {
        const { result } = renderHook(() => useWorkflowCardPill(canvasData({ isGenerating: true }), component, extras()), { wrapper: wrap() });
        expect(result.current).toMatchObject({ generating: true, editing: true });
    });

    test("an apply pass keeps a regenerating agent on Editing even after its manifest status left deployed", () => {
        const { result } = renderHook(() => useWorkflowCardPill(canvasData({ isGenerating: true, status: "draft" }), { ...component, status: "draft" }, extras()), {
            wrapper: wrap({ isApplyingChanges: true }),
        });
        expect(result.current.editing).toBe(true);
    });

    test("Test mode collapses the card to neutral: no Active, no Creating, no Waiting text", () => {
        const { result } = renderHook(() => useWorkflowCardPill(canvasData(), component, extras({ depWaitingForInput: true })), { wrapper: wrap({ tab: "test" }) });
        expect(result.current).toEqual({
            generating: false,
            editing: false,
            queued: false,
            buildingDeps: false,
            ready: false,
            active: false,
            draft: false,
            error: false,
            statusText: undefined,
        });
    });

    test("Test mode also neutralises a regenerating card", () => {
        const { result } = renderHook(() => useWorkflowCardPill(canvasData({ isGenerating: true }), component, extras()), { wrapper: wrap({ tab: "test" }) });
        expect(result.current).toMatchObject({ generating: false, editing: false });
    });

    test("Build mode still shows the dependency waiting text", () => {
        const { result } = renderHook(() => useWorkflowCardPill(canvasData(), component, extras({ depWaitingForInput: true })), { wrapper: wrap() });
        expect(result.current.statusText).toBe("Waiting for Input...");
    });

    test("an agent with no canvas data falls back to its manifest status", () => {
        const deployed = renderHook(() => useWorkflowCardPill(null, { name: "Platform", type: "agent", status: "deployed" }, extras()), { wrapper: wrap() });
        expect(deployed.result.current.active).toBe(true);
        const unbuilt = renderHook(() => useWorkflowCardPill(null, { name: "Planned", type: "agent", status: "draft" }, extras()), { wrapper: wrap() });
        expect(unbuilt.result.current).toMatchObject({ draft: true, queued: false });
    });

    test("an agent whose own record is live on the mesh reads Active even when its manifest status lags", () => {
        const { result } = renderHook(() => useWorkflowCardPill(canvasData({ status: "validated", id: OWN_ID }), { ...component, id: OWN_ID, status: "validated" }, extras()), {
            wrapper: wrap({ liveAgentCards: [{ name: OWN_CARD_NAME }] }),
        });
        expect(result.current).toMatchObject({ active: true, ready: false });
    });

    test("an agent sharing its name with a live agent another session deployed does not read Active", () => {
        const { result } = renderHook(() => useWorkflowCardPill(canvasData({ status: "draft" }), { ...component, status: "draft" }, extras()), {
            wrapper: wrap({ liveAgentCards: [{ name: OWN_CARD_NAME, displayName: "UiOrders" }] }),
        });
        expect(result.current).toMatchObject({ active: false, draft: true });
    });
});
