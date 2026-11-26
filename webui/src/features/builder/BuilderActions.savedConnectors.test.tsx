// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentProps, createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

vi.mock("./hooks/usePlatformCheck", () => ({ usePlatformCheck: () => ({ check: { kind: "passed", warnings: [] }, key: null, checkAgain: () => {} }) }));

import type { BuildOperationResult } from "@/api/builder/applyTypes";

import { v2PlatformClient } from "@/testing/helpers/platformResponses";
import { STAMPED_VERSION, withCurrentStamp } from "@/testing/helpers/validationStamp";

import type { BuildManifest } from "./hooks/useBuilderManifest";

function newQueryWrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

const op = (kind: string, name: string, action: BuildOperationResult["action"], extra: Partial<BuildOperationResult> = {}): BuildOperationResult =>
    ({ kind, name, action, status: "succeeded", resourceId: `id-${name}`, ...extra }) as BuildOperationResult;

const summary = { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 };

const manifest: BuildManifest = {
    name: "my-build",
    full_validation_passed: true,
    components: [
        { filename: "db.yaml", type: "connector", name: "db", status: "validated", origin: "session" },
        { filename: "tok.yaml", type: "connector", name: "tok", status: "validated", origin: "session" },
        { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session" },
    ],
};

describe("BuilderActions — recording saved connectors", () => {
    let BuilderActions: typeof import("./BuilderActions").BuilderActions;
    let queryWrapper: ReturnType<typeof newQueryWrapper>;
    const applyBuild = vi.fn();
    const recordSavedComponents = vi.fn();
    const versionsBeforeSave = vi.fn();
    const readAt = () => new Map([["db.yaml", 1]]);
    const readOf = () => ({ at: readAt(), failed: [] as string[] });
    const addNotification = vi.fn();
    const displayError = vi.fn();
    const handleSubmit = vi.fn();
    const listVersions = vi.fn();
    let mockSessionId: string;

    const settled = () => act(() => new Promise<void>(resolve => setTimeout(resolve, 50)));

    const renderSave = (props: Partial<ComponentProps<typeof BuilderActions>> = {}) =>
        render(<BuilderActions configYaml="" configType="agent" manifest={manifest} fetchConfigByFilename={vi.fn().mockResolvedValue("name: x")} autoSave {...props} />, { wrapper: queryWrapper });

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        sessionStorage.clear();
        localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
        recordSavedComponents.mockResolvedValue(new Map());
        versionsBeforeSave.mockResolvedValue(readOf());
        mockSessionId = "test-session";
        listVersions.mockResolvedValue([0, 1, 2]);

        vi.doMock("@/ui/button", () => ({ Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => <button onClick={onClick}>{children}</button> }));
        vi.doMock("@/api/client", () => ({ api: { platform: v2PlatformClient({ get: () => ({ data: [] }) }) } }));
        vi.doMock("@/utils/api", () => ({ getErrorMessage: (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback) }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({ addNotification, displayError, uploadArtifactFile: vi.fn(), sessionId: mockSessionId, allArtifacts: [], handleSubmit }),
        }));
        vi.doMock("@/providers/contexts/AuthContext", () => ({ useAuthContext: () => ({ userInfo: { username: "sam_dev_user" } }) }));
        vi.doMock("./hooks/useWorkflowDeployment", () => ({ useWorkflowDeployment: () => ({ deploy: vi.fn(), isLoading: false }) }));
        vi.doMock("./ConnectorEditDialog", () => ({ ConnectorEditDialog: () => null }));
        vi.doMock("@/api/artifacts", () => ({ useChatArtifacts: () => ({ data: [], isLoading: false, refetch: () => Promise.resolve() }) }));
        vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: (m: BuildManifest | null) => new Map<string, number>(m?.components?.map(c => [c.filename, STAMPED_VERSION])) }));
        vi.doMock("@/api/skills", () => ({ useSaveSkillFromConfigMutation: () => ({ mutateAsync: vi.fn() }) }));
        vi.doMock("@/common/product-analytics/report", () => ({ report: vi.fn(), reportingSessionId: () => "session-one" }));
        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => true }));
        vi.doMock("@/api/builder", async importOriginal => ({
            ...(await importOriginal<typeof import("@/api/builder")>()),
            useApplyBuildMutation: () => ({ mutateAsync: (vars: unknown) => applyBuild(vars) }),
            listManifestVersions: (...args: unknown[]) => listVersions(...args),
        }));
        const recorder = { versionsBeforeSave: () => versionsBeforeSave(), record: (...args: unknown[]) => recordSavedComponents(...args) };
        vi.doMock("./hooks/useRecordSavedComponents", () => ({ useRecordSavedComponents: () => recorder }));

        BuilderActions = withCurrentStamp((await import("./BuilderActions")).BuilderActions, () => STAMPED_VERSION);
        queryWrapper = newQueryWrapper();
    });

    test.each([
        ["an updated connector", [op("connector", "db", "update"), op("agent", "TestAgent", "unchanged")], { changed: ["db"], matched: ["TestAgent"], declined: [] }],
        ["a connector planned unchanged", [op("connector", "db", "unchanged"), op("agent", "TestAgent", "update")], { changed: ["TestAgent"], matched: ["db"], declined: [] }],
        ["a connector whose edit was partly declined", [op("connector", "tok", "update", { changesDeclined: true })], { changed: ["tok"], matched: [], declined: ["tok"] }],
    ])("hands the recorder the proof of the save: %s", async (_label, operations, proof) => {
        applyBuild.mockResolvedValue({ operations, summary });

        renderSave();

        await waitFor(() => expect(recordSavedComponents).toHaveBeenCalledTimes(1));
        expect(recordSavedComponents).toHaveBeenCalledWith(proof, readAt(), expect.any(Array));
    });

    test("reads the versions a save is about to send before the save reads any config", async () => {
        const order: string[] = [];
        versionsBeforeSave.mockImplementation(async () => {
            order.push("versions");
            return readOf();
        });
        const fetchConfigByFilename = vi.fn(async () => {
            order.push("config");
            return "name: x";
        });
        applyBuild.mockResolvedValue({ operations: [op("connector", "db", "update")], summary });

        renderSave({ fetchConfigByFilename });

        await waitFor(() => expect(recordSavedComponents).toHaveBeenCalledTimes(1));
        expect(order[0]).toBe("versions");
        expect(order).toContain("config");
    });

    test("records nothing when the build request is rejected", async () => {
        applyBuild.mockRejectedValue(new Error("boom"));

        renderSave();

        await waitFor(() => expect(applyBuild).toHaveBeenCalled());
        await settled();
        expect(recordSavedComponents).not.toHaveBeenCalled();
    });

    test("records nothing when an operation in the build failed", async () => {
        applyBuild.mockResolvedValue({ operations: [op("connector", "db", "update"), op("agent", "TestAgent", "update", { status: "failed", error: { message: "boom", errorId: "e1", httpStatus: 422 } })], summary });

        renderSave();

        await waitFor(() => expect(applyBuild).toHaveBeenCalled());
        await settled();
        expect(recordSavedComponents).not.toHaveBeenCalled();
    });

    test("records the connectors a build saved even when a skill beside it failed, after reporting the failure", async () => {
        const withSkill: BuildManifest = { ...manifest, components: [...(manifest.components ?? []), { filename: "skill.yaml", type: "skill", name: "broken_skill", status: "validated", origin: "session" }] };
        applyBuild.mockResolvedValue({ operations: [op("connector", "db", "update")], summary });
        let reportsWhenRecorded = -1;
        recordSavedComponents.mockImplementation(async () => {
            reportsWhenRecorded = displayError.mock.calls.length + handleSubmit.mock.calls.length;
            return new Map();
        });

        renderSave({ manifest: withSkill });

        await waitFor(() => expect(recordSavedComponents).toHaveBeenCalledTimes(1));
        expect(recordSavedComponents).toHaveBeenCalledWith({ changed: ["db"], matched: [], declined: [] }, readAt(), expect.any(Array));
        expect(reportsWhenRecorded).toBeGreaterThan(0);
    });

    describe("with the real recorder", () => {
        const saveManifest = vi.fn();
        const stampedBy = (m: BuildManifest) => saveManifest.mock.calls.map(([updater]) => (updater as (prev: BuildManifest) => BuildManifest)(m).components?.find(c => c.name === "db")?.deployed_version);

        beforeEach(async () => {
            saveManifest.mockReset().mockResolvedValue({ ok: true });
            vi.doUnmock("./hooks/useRecordSavedComponents");
            vi.resetModules();
            BuilderActions = withCurrentStamp((await import("./BuilderActions")).BuilderActions, () => STAMPED_VERSION);
            queryWrapper = newQueryWrapper();
        });

        test("stamps the saved connector in the manifest it was given", async () => {
            applyBuild.mockResolvedValue({ operations: [op("connector", "db", "update")], summary });

            renderSave({ saveManifest });

            await waitFor(() => expect(stampedBy(manifest)).toContain(2));
            expect(listVersions).toHaveBeenCalledWith("test-session", "db.yaml");
        });

        test("leaves a connector whose edit was declined unstamped", async () => {
            applyBuild.mockResolvedValue({ operations: [op("connector", "db", "update", { changesDeclined: true })], summary });

            renderSave({ saveManifest });

            await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("saved successfully"), "success"));
            await settled();
            expect(stampedBy(manifest)).not.toContain(2);
        });

        test.each([
            ["no manifest writer", { saveManifest: undefined }, "test-session", true],
            ["no session", { saveManifest }, "", false],
        ])("records nothing with %s", async (_label, props, sessionId, versionsAreRead) => {
            mockSessionId = sessionId;
            applyBuild.mockResolvedValue({ operations: [op("connector", "db", "update")], summary });

            renderSave(props);

            await waitFor(() => expect(applyBuild).toHaveBeenCalled());
            await settled();
            expect(listVersions.mock.calls.length > 0).toBe(versionsAreRead);
            expect(stampedBy(manifest)).toEqual([]);
        });
    });
});
