// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

vi.mock("./hooks/usePlatformCheck", () => ({ usePlatformCheck: () => ({ check: { kind: "passed", warnings: [] }, key: null, checkAgain: () => {} }) }));

import { respondingAsV2, v2DeploymentOf, v2Page, v2PageOf, v2ResourceOf } from "@/testing/helpers/platformResponses";
import { STAMPED_VERSION, withCurrentStamp } from "@/testing/helpers/validationStamp";

import type { BuildManifest } from "./hooks/useBuilderManifest";

const BUILDS_PATH = "/api/internal/platform/builder/builds";
const SKILLS_PATH = "/api/v2/platform/skills";
const AGENTS_PATH = "/api/v2/platform/agents";
const DIALOG = /Replace existing components?\?/;

function newQueryWrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

const readBytes = (file: Blob): Promise<ArrayBuffer> =>
    new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result as ArrayBuffer);
        reader.onerror = () => reject(reader.error);
        reader.readAsArrayBuffer(file);
    });

const SKILL_YAML = `
name: TestSkill
description: A test skill with a sufficiently long description.
instruction_content: |
  Do the thing the skill is for.
`;
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
    components: [
        { filename: "skill.yaml", type: "skill", name: "TestSkill", status: "validated" },
        { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated" },
    ],
};

describe("BuilderActions, a skill already on the platform, over HTTP only", () => {
    let BuilderActions: typeof import("./BuilderActions").BuilderActions;
    let queryWrapper: ReturnType<typeof newQueryWrapper>;
    const addNotification = vi.fn();
    const platformGet = vi.fn();
    const platformPost = vi.fn();
    const platformPut = vi.fn();
    const originalArrayBuffer = Blob.prototype.arrayBuffer;

    const log: string[] = [];
    const requests = () => log.filter(request => /\/skills|\/builder\/builds/.test(request));

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        sessionStorage.clear();
        localStorage.clear();
        Blob.prototype.arrayBuffer = function (this: Blob) {
            return readBytes(this);
        };
        vi.doMock("@/ui/button", () => ({
            Button: ({ children, onClick, disabled }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean }) => (
                <button onClick={onClick} disabled={disabled}>
                    {children}
                </button>
            ),
        }));
        vi.doMock("@/api/client", () => ({ api: { platform: { get: respondingAsV2(platformGet), post: respondingAsV2(platformPost), put: respondingAsV2(platformPut), delete: vi.fn() } } }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({ addNotification, displayError: vi.fn(), uploadArtifactFile: vi.fn(), sessionId: "test-session", allArtifacts: [], handleSubmit: vi.fn(), isResponding: false }),
        }));
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

        log.length = 0;
        platformGet.mockImplementation(async (url: string) => {
            const path = String(url).split("?")[0];
            log.push(`GET ${path}`);
            if (path === SKILLS_PATH) return v2PageOf(path, [{ id: "skill-9", name: "TestSkill", contentHash: "0".repeat(64), discoveryStatus: "ready" }]);
            if (path.startsWith(`${SKILLS_PATH}/`)) return v2ResourceOf(path, { id: "skill-9", name: "TestSkill", discoveryStatus: "ready" });
            return v2Page([]);
        });
        platformPost.mockImplementation(async (url: string, body?: { dryRun?: boolean }) => {
            log.push(`POST ${url}${body?.dryRun ? " (dry run)" : ""}`);
            if (url === BUILDS_PATH) {
                return {
                    dryRun: Boolean(body?.dryRun),
                    operations: [{ kind: "agent", name: "TestAgent", action: "update", status: body?.dryRun ? "planned" : "succeeded", resourceId: "agent-1" }],
                    summary: { created: 0, updated: 1, unchanged: 0, failed: 0, skipped: 0 },
                };
            }
            return {};
        });
        platformPut.mockImplementation(async (url: string) => {
            log.push(`PUT ${url}`);
            return v2ResourceOf(url, { id: "skill-9", name: "TestSkill", discoveryStatus: "pending" });
        });

        BuilderActions = withCurrentStamp((await import("./BuilderActions")).BuilderActions, () => STAMPED_VERSION);
        queryWrapper = newQueryWrapper();
    });

    afterEach(() => {
        Blob.prototype.arrayBuffer = originalArrayBuffer;
    });

    function renderBuild() {
        render(<BuilderActions configYaml="" configType="agent" manifest={MANIFEST} fetchConfigByFilename={vi.fn(async (filename: string) => (filename === "skill.yaml" ? SKILL_YAML : AGENT_YAML))} autoSave />, { wrapper: queryWrapper });
    }

    test("a skill whose bundle differs is listed, planned around, confirmed, uploaded, and only then is the build applied", async () => {
        renderBuild();

        await screen.findByText(DIALOG);
        expect(requests().filter(r => r.startsWith("POST"))).toEqual([`POST ${BUILDS_PATH} (dry run)`]);

        screen.getByRole("button", { name: "Replace and save" }).click();

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Updated in place: TestSkill"), "success"));
        expect(requests()).toEqual([`GET ${SKILLS_PATH}`, `POST ${BUILDS_PATH} (dry run)`, `POST ${BUILDS_PATH} (dry run)`, `PUT ${SKILLS_PATH}/skill-9/bundle`, `GET ${SKILLS_PATH}/skill-9`, `POST ${BUILDS_PATH}`]);
    });

    test("declining the question uploads nothing and applies nothing", async () => {
        renderBuild();

        await screen.findByText(DIALOG);
        screen.getByRole("button", { name: "Cancel" }).click();
        await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
        await new Promise(resolve => setTimeout(resolve, 100));

        expect(requests()).toEqual([`GET ${SKILLS_PATH}`, `POST ${BUILDS_PATH} (dry run)`]);
    });

    test("a skill saved earlier in the session is looked up again and left alone: no upload, and the build is applied again", async () => {
        localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
        const { bundleHash } = await import("@/api/skills/service");
        const stored: Array<{ id: string; name: string; contentHash?: string; discoveryStatus: string }> = [];
        platformGet.mockImplementation(async (url: string) => {
            const path = String(url).split("?")[0];
            log.push(`GET ${path}`);
            if (path === SKILLS_PATH) return v2PageOf(path, stored);
            if (path.startsWith(`${SKILLS_PATH}/`)) return v2ResourceOf(path, stored.find(skill => path.endsWith(`/${skill.id}`)) ?? stored[0]);
            return path === AGENTS_PATH ? v2PageOf(path, [{ id: "agent-1", name: "TestAgent", deploymentStatus: "not_deployed" }]) : v2Page([]);
        });
        const buildResponse = (body?: { dryRun?: boolean }) => ({
            dryRun: Boolean(body?.dryRun),
            operations: [{ kind: "agent", name: "TestAgent", action: "update", status: body?.dryRun ? "planned" : "succeeded", resourceId: "agent-1" }],
            summary: { created: 0, updated: 1, unchanged: 0, failed: 0, skipped: 0 },
        });
        platformPost.mockImplementation(async (url: string, body?: unknown) => {
            log.push(`POST ${url}${(body as { dryRun?: boolean } | undefined)?.dryRun ? " (dry run)" : ""}`);
            if (url === BUILDS_PATH) return buildResponse(body as { dryRun?: boolean });
            if (url === SKILLS_PATH) {
                stored.push({ id: "skill-9", name: "TestSkill", contentHash: await bundleHash((body as FormData).get("file") as File), discoveryStatus: "ready" });
                return v2ResourceOf(url, { id: "skill-9", name: "TestSkill", discoveryStatus: "pending" });
            }
            if (url.endsWith("Deployments")) return v2DeploymentOf(body);
            return {};
        });
        let latest: import("./BuilderActions").BuildActivateApi | null = null;
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={MANIFEST}
                fetchConfigByFilename={vi.fn(async (filename: string) => (filename === "skill.yaml" ? SKILL_YAML : AGENT_YAML))}
                onBuildActivateApiChange={next => {
                    latest = next;
                }}
            />,
            { wrapper: queryWrapper }
        );
        await waitFor(() => expect(latest).not.toBeNull());

        await latest!.run();
        await waitFor(() => expect(requests().filter(r => r === `POST ${BUILDS_PATH}`)).toHaveLength(1));
        const afterFirst = requests();

        await latest!.run();
        await waitFor(() => expect(requests().filter(r => r === `POST ${BUILDS_PATH}`)).toHaveLength(2));

        expect(afterFirst.filter(r => r === `GET ${SKILLS_PATH}`)).toHaveLength(1);
        expect(requests().filter(r => r === `GET ${SKILLS_PATH}`)).toHaveLength(2);
        expect(requests().filter(r => r === `POST ${SKILLS_PATH}`)).toHaveLength(1);
        expect(requests().filter(r => r.endsWith("/bundle"))).toEqual([]);
        expect(latest!.failure.kind).toBe("none");
    });
});
