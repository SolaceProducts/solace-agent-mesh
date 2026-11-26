// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { v2PlatformClient } from "@/testing/helpers/platformResponses";
import { STAMPED_VERSION, withCurrentStamp } from "@/testing/helpers/validationStamp";

import type { BuildManifest } from "./hooks/useBuilderManifest";
import { PLATFORM_CHECK_CHIP } from "./platformCheck";
import { PlatformCheckStatus } from "./PlatformCheckStatus";

const BUILDS_PATH = "/api/internal/platform/builder/builds";

const AGENT_YAML = `
apps:
  - name: TestAgent
    app_config:
      agent_name: TestAgent
      description: A test agent with a sufficiently long description.
      instruction: |
        ${"x".repeat(120)}
      agent_card:
        description: Another description on agent_card.
`;

const MANIFEST: BuildManifest = {
    name: "my-build",
    full_validation_passed: true,
    components: [{ filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated" }],
};

const BLOCKED_MANIFEST: BuildManifest = {
    ...MANIFEST,
    components: [...MANIFEST.components!, { filename: "connector.yaml", type: "connector", name: "NeedsKey", origin: "session", status: "blocked" }],
};

const ACCEPTED_DRY_RUN = { dryRun: true, dropped: [], operations: [{ kind: "agent", name: "TestAgent", action: "create", status: "planned" }], summary: { created: 1, updated: 0, unchanged: 0, failed: 0, skipped: 0 } };

const REFUSAL = "agent TestAgent: model alias is unknown";

function newQueryWrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

describe("BuilderActions, the platform check run live", () => {
    let BuilderActions: typeof import("./BuilderActions").BuilderActions;
    let queryWrapper: ReturnType<typeof newQueryWrapper>;
    const platformGet = vi.fn();
    const platformPost = vi.fn();
    const handleSubmit = vi.fn();
    const chat = { addNotification: vi.fn(), displayError: vi.fn(), uploadArtifactFile: vi.fn(), sessionId: "test-session", selectedAgentName: "Builder", allArtifacts: [], handleSubmit, isResponding: false, isCancelling: false };
    const REFUSED_DRY_RUN = {
        dryRun: true,
        dropped: [],
        operations: [{ kind: "agent", name: "TestAgent", action: "create", status: "failed", error: { message: "model alias is unknown", httpStatus: 422, errorId: "err-1" } }],
        summary: { created: 0, updated: 0, unchanged: 0, failed: 1, skipped: 0 },
    };

    const dryRuns = () => platformPost.mock.calls.filter(([url, body]) => url === BUILDS_PATH && (body as { dryRun?: boolean }).dryRun);

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        chat.isResponding = false;
        sessionStorage.clear();
        localStorage.clear();
        vi.doMock("@/api/client", () => ({ api: { platform: v2PlatformClient({ get: platformGet, post: platformPost }) } }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({ useChatContext: () => chat }));
        vi.doMock("@/providers/contexts/AuthContext", () => ({ useAuthContext: () => ({ userInfo: { username: "sam_dev_user" } }) }));
        vi.doMock("./hooks/useWorkflowDeployment", () => ({ useWorkflowDeployment: () => ({ deploy: vi.fn(), isLoading: false }) }));
        vi.doMock("./ConnectorEditDialog", () => ({ ConnectorEditDialog: () => null }));
        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: [], isLoading: false, refetch: () => Promise.resolve() }),
            getArtifactContentWithValidation: vi.fn(),
        }));
        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => true }));
        vi.doMock("@/common/product-analytics/report", () => ({ report: vi.fn(), reportingSessionId: () => "session-one" }));
        vi.doMock("@/api/builder", async importOriginal => ({
            ...(await importOriginal<typeof import("@/api/builder")>()),
            listManifestVersions: () => Promise.resolve([STAMPED_VERSION]),
        }));
        platformGet.mockResolvedValue({ data: [], meta: { pagination: { pageNumber: 1, nextPage: null } } });

        BuilderActions = withCurrentStamp((await import("./BuilderActions")).BuilderActions, () => STAMPED_VERSION);
        queryWrapper = newQueryWrapper();
    });

    let latestApi: import("./BuilderActions").BuildActivateApi | null;
    let rerenderBuild: () => void;

    function element(autoSave = false, manifest = MANIFEST) {
        return (
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifest}
                autoSave={autoSave}
                fetchConfigByFilename={vi.fn(async () => AGENT_YAML)}
                onBuildActivateApiChange={api => {
                    latestApi = api;
                }}
            />
        );
    }

    function openCold(autoSave = false, manifest = MANIFEST) {
        latestApi = null;
        const view = render(element(autoSave, manifest), { wrapper: queryWrapper });
        rerenderBuild = () => view.rerender(element(autoSave, manifest));
    }

    function openAfterATurn(autoSave = false, manifest = MANIFEST) {
        chat.isResponding = true;
        openCold(autoSave, manifest);
        chat.isResponding = false;
        rerenderBuild();
    }

    test("a validated build opened cold is checked, and a refusal is shown and offered to the Builder, not sent", async () => {
        platformPost.mockResolvedValue(REFUSED_DRY_RUN);

        openCold();

        await waitFor(() => expect(latestApi?.platformCheck.kind).toBe("refused"));
        await new Promise(resolve => setTimeout(resolve, 100));
        expect(dryRuns()).toHaveLength(1);
        expect(handleSubmit).not.toHaveBeenCalled();
        expect(latestApi?.canAskBuilder).toBe(true);
        expect(latestApi?.canBuildActivate).toBe(false);

        latestApi?.askBuilder();

        expect(handleSubmit).toHaveBeenCalledTimes(1);
        const [, , message, , chip, , , , metadata] = handleSubmit.mock.calls[0];
        expect(message).toContain(REFUSAL);
        expect(chip).toBe(PLATFORM_CHECK_CHIP);
        expect(metadata).toEqual({ resume_build: true });
    });

    test("a refusal found after a Builder turn ended in this mount posts exactly one platform-check message", async () => {
        platformPost.mockResolvedValue(REFUSED_DRY_RUN);

        openAfterATurn();

        await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(1));
        await new Promise(resolve => setTimeout(resolve, 100));
        expect(dryRuns()).toHaveLength(1);
        expect(handleSubmit).toHaveBeenCalledTimes(1);
        const [, , message, , chip, , , , metadata] = handleSubmit.mock.calls[0];
        expect(message).toContain("Platform check");
        expect(message).toContain(REFUSAL);
        expect(chip).toBe(PLATFORM_CHECK_CHIP);
        expect(metadata).toEqual({ auto_fix: true, resume_build: true });
    });

    test("a refusal of the whole request posts one message too", async () => {
        platformPost.mockRejectedValue(Object.assign(new Error("build name is not allowed"), { status: 422 }));

        openAfterATurn();

        await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(1));
        await new Promise(resolve => setTimeout(resolve, 100));
        expect(handleSubmit).toHaveBeenCalledTimes(1);
        expect(handleSubmit.mock.calls[0][2]).toContain("build name is not allowed");
    });

    test("a build the platform accepts posts nothing, cold or after a turn", async () => {
        platformPost.mockResolvedValue({ dryRun: true, dropped: [], operations: [{ kind: "agent", name: "TestAgent", action: "create", status: "planned" }], summary: { created: 1, updated: 0, unchanged: 0, failed: 0, skipped: 0 } });

        openAfterATurn();

        await waitFor(() => expect(latestApi?.canBuildActivate).toBe(true));
        await new Promise(resolve => setTimeout(resolve, 100));
        expect(handleSubmit).not.toHaveBeenCalled();
    });

    test("a save that fails on the same refusal and the check that finds it post one message between them", async () => {
        platformPost.mockResolvedValue(REFUSED_DRY_RUN);

        openAfterATurn(true);

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        await new Promise(resolve => setTimeout(resolve, 300));
        expect(handleSubmit).toHaveBeenCalledTimes(1);
    });

    test("a build with a blocked component is not checked, and a refusal is not posted for a check the page hides", async () => {
        platformPost.mockResolvedValue(REFUSED_DRY_RUN);

        openAfterATurn(false, BLOCKED_MANIFEST);

        await new Promise(resolve => setTimeout(resolve, 200));
        expect(dryRuns()).toHaveLength(0);
        expect(handleSubmit).not.toHaveBeenCalled();
        expect(latestApi?.platformCheck.kind).toBe("idle");
    });

    test("the real automatic fix posts two refusals, holds the third and offers the ask, and a pass earns a fresh budget", async () => {
        const ASK = "Ask the Builder to fix these";
        const rebuilt = (n: number): BuildManifest => ({ ...MANIFEST, name: `my-build-${n}`, components: [{ filename: "agent.yaml", type: "agent", name: `TestAgent${n}`, status: "validated" }] });
        const settle = () => new Promise(resolve => setTimeout(resolve, 100));
        const askButton = () => {
            const { unmount } = render(<PlatformCheckStatus view={{ check: latestApi!.platformCheck, canAskBuilder: latestApi!.canAskBuilder }} />);
            const shown = screen.queryByRole("button", { name: ASK }) !== null;
            unmount();
            return shown;
        };
        platformPost.mockResolvedValue(REFUSED_DRY_RUN);

        chat.isResponding = true;
        latestApi = null;
        const view = render(element(false, rebuilt(0)), { wrapper: queryWrapper });
        chat.isResponding = false;
        view.rerender(element(false, rebuilt(0)));
        await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(1));
        expect(handleSubmit.mock.calls[0][8]).toEqual({ auto_fix: true, resume_build: true });

        view.rerender(element(false, rebuilt(1)));
        await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(2));
        expect(handleSubmit.mock.calls[1][8]).toEqual({ auto_fix: true, resume_build: true });

        view.rerender(element(false, rebuilt(2)));
        await waitFor(() => expect(dryRuns()).toHaveLength(3));
        await waitFor(() => expect(latestApi?.canAskBuilder).toBe(true));
        await settle();
        expect(handleSubmit).toHaveBeenCalledTimes(2);
        expect(askButton()).toBe(true);

        platformPost.mockResolvedValue(ACCEPTED_DRY_RUN);
        view.rerender(element(false, rebuilt(3)));
        await waitFor(() => expect(latestApi?.platformCheck.kind).toBe("passed"));
        await settle();
        expect(handleSubmit).toHaveBeenCalledTimes(2);

        platformPost.mockResolvedValue(REFUSED_DRY_RUN);
        view.rerender(element(false, rebuilt(4)));
        await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(3));
        expect(handleSubmit.mock.calls[2][8]).toEqual({ auto_fix: true, resume_build: true });
    });

    test("the side panel's floating Build & Activate button cannot appear with server apply on, because the check never runs there and so never passes", async () => {
        const sidePanel = (Actions: typeof BuilderActions) => render(<Actions configYaml="" configType="agent" manifest={MANIFEST} />, { wrapper: queryWrapper });

        const flagOn = sidePanel(BuilderActions);
        await new Promise(resolve => setTimeout(resolve, 100));
        expect(screen.queryByRole("button", { name: /Build & Activate/ })).toBeNull();
        expect(dryRuns()).toHaveLength(0);
        flagOn.unmount();

        vi.resetModules();
        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => false }));
        const FlagOff = withCurrentStamp((await import("./BuilderActions")).BuilderActions, () => STAMPED_VERSION);
        sidePanel(FlagOff);
        expect(await screen.findByRole("button", { name: /Build & Activate/ })).toBeInTheDocument();
    });

    test.each([
        ["asked before overwriting", false],
        ["not asked before overwriting", true],
    ])("a Build & Activate save that the platform refuses discards the pass the check had remembered (%s)", async (_label, skipConfirm) => {
        if (skipConfirm) localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
        platformPost.mockResolvedValue(ACCEPTED_DRY_RUN);
        openCold();
        await waitFor(() => expect(latestApi?.platformCheck.kind).toBe("passed"));

        platformPost.mockResolvedValue(skipConfirm ? { ...REFUSED_DRY_RUN, dryRun: false } : REFUSED_DRY_RUN);
        await act(async () => {
            await latestApi?.run();
        });

        await waitFor(() => expect(latestApi?.platformCheck.kind).toBe("refused"));
    });
});
