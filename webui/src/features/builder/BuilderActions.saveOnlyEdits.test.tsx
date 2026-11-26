// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

vi.mock("./hooks/usePlatformCheck", () => ({ usePlatformCheck: () => ({ check: { kind: "passed", warnings: [] }, key: null, checkAgain: () => {} }) }));

import { TooltipProvider } from "@/ui/tooltip";

import { respondingAsV2, v2DeploymentOf, v2Error, v2Page, v2PageOf, v2ResourceOf } from "@/testing/helpers/platformResponses";
import { type Build, type Model, type ModelEvent, observe, start, step } from "@/testing/helpers/saveOnlyModel";
import { withCurrentStamp } from "@/testing/helpers/validationStamp";

import { isBuildCompleteCtaShowing } from "./builderUtils";
import { saveOnlyFilenames } from "./componentDirtyState";
import { useBuildActivateBridge } from "./hooks/useBuildActivateBridge";
import type { BuildManifest } from "./hooks/useBuilderManifest";
import { InlinePlanCard } from "./InlinePlanCard";

const BUILDS_PATH = "/api/internal/platform/builder/builds";
const SKILLS_PATH = "/api/v2/platform/skills";
const CONNECTORS_PATH = "/api/v2/platform/connectors";
const AGENTS_PATH = "/api/v2/platform/agents";
const DIALOG = /Replace existing components?\?/;
const SESSION = "test-session";
const OTHER_SESSION = "another-session";
const OWNERSHIP_KEY = "sam_builder_written_resources";

const skillYamlAt = (version: number) => `
name: TestSkill
description: A test skill with a sufficiently long description.
instruction_content: |
  Do the thing the skill is for, as of edit ${version}.
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
const connectorAt = (version: number) => JSON.stringify({ name: "db", type: "sql", subtype: "postgres", values: { hostname: `v${version}.example.invalid` } });
const CONNECTOR_JSON = JSON.stringify({ name: "db", type: "sql", subtype: "postgres", values: { hostname: "one.example.invalid" } });

type Kind = "connector" | "skill";

const instantsByStamp = new Map<string, string>();
function instantOf(stamp: string): string {
    if (!instantsByStamp.has(stamp)) instantsByStamp.set(stamp, new Date(Date.UTC(2026, 0, 1) + instantsByStamp.size * 1000).toISOString());
    return instantsByStamp.get(stamp) as string;
}
const stampOnWire = (kind: Kind, stamp: string) => (kind === "connector" ? instantOf(stamp) : stamp);

const SAVED: Record<Kind, { id: string; filename: string; name: string }> = {
    connector: { id: "conn-1", filename: "db.json", name: "db" },
    skill: { id: "skill-1", filename: "skill.yaml", name: "TestSkill" },
};

const manifestFor = (kind: Kind, extra: Record<string, unknown> = {}): BuildManifest => ({
    name: "my-build",
    full_validation_passed: true,
    components: [{ filename: SAVED[kind].filename, type: kind, name: SAVED[kind].name, status: "validated", origin: "session", ...extra }],
});

function newQueryWrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

describe("BuilderActions, a save-only build edited or reopened", () => {
    let BuilderActions: typeof import("./BuilderActions").BuilderActions;
    let bundleHash: typeof import("@/api/skills/service").bundleHash;
    let queryWrapper: ReturnType<typeof newQueryWrapper>;

    const addNotification = vi.fn();
    const displayError = vi.fn();
    const handleSubmit = vi.fn();
    const platformGet = vi.fn();
    const platformPost = vi.fn();
    const platformPut = vi.fn();
    const originalArrayBuffer = Blob.prototype.arrayBuffer;

    let responding = false;
    let sessionId = SESSION;
    let duringWrite: (() => void) | undefined;
    let duringPlan: (() => void | Promise<void>) | undefined;
    let serverApplyEnabled = true;
    let versions = new Map<string, number>();
    let readBackFails = false;
    let skillDrift = 0;
    let refuseWrites = 0;
    let duringLookup: (() => void) | undefined;
    let bridged = false;
    let bridge: import("./BuilderActions").BuildActivateApi | null = null;
    let plannedActions: Array<"update" | "unchanged"> = [];
    let buildWarnings: string[] = [];
    let pinnedReadFailsWith: number | undefined;
    let recorderCalls: string[][] = [];
    let readFiles: string[][] = [];
    let recordedFiles: string[][] = [];
    let duringAnyGet: (() => void | Promise<void>) | undefined;
    let versionsOnceRead: Map<string, number> | undefined;
    let holdBeforeRead: Promise<void> | undefined;
    let versionReads = 0;
    let failedLookups = new Set<string>();
    let versionsReadThrows = false;
    let missingConfigs = new Set<string>();
    let applyFailsWith: number | undefined;
    let rejections = 0;
    let connectorOnPlatform = false;
    let deployedOnPlatform: Record<string, string[]> = {};
    let realPinnedRead = false;
    const webuiGet = vi.fn();
    let planApproved = false;
    let buildRequests: Array<{ dryRun: boolean; names: string[] }> = [];
    let contentCompare = false;
    let platformConnectorContent = "";
    let successfulWrites = 0;
    let manifestRecordNow: number | undefined;
    const recordedByFile = new Map<string, number | undefined>();
    let configFetches: Array<[string, { skipAccumulated?: boolean; version?: number } | undefined]> = [];
    let platformStamp: Record<Kind, string> = { skill: "", connector: "" };
    let writes = 0;
    const log: string[] = [];

    const readBytes = (file: Blob): Promise<ArrayBuffer> =>
        new Promise((resolve, reject) => {
            const reader = new FileReader();
            reader.onload = () => resolve(reader.result as ArrayBuffer);
            reader.onerror = () => reject(reader.error);
            reader.readAsArrayBuffer(file);
        });

    const applies = () => log.filter(r => r === `POST ${BUILDS_PATH}`).length;
    const uploads = () => log.filter(r => r.endsWith("/bundle")).length;
    const writesToPlatform = () => applies() + uploads();
    const turnsOfTheLoop = async (turns: number) => {
        for (let turn = 0; turn < turns; turn++) await act(() => new Promise<void>(resolve => setTimeout(resolve, 0)));
    };
    const settle = () => turnsOfTheLoop(12);

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        sessionStorage.clear();
        localStorage.clear();
        responding = false;
        sessionId = SESSION;
        duringWrite = undefined;
        duringPlan = undefined;
        serverApplyEnabled = true;
        readBackFails = false;
        skillDrift = 0;
        refuseWrites = 0;
        duringLookup = undefined;
        bridged = false;
        bridge = null;
        plannedActions = [];
        buildWarnings = [];
        pinnedReadFailsWith = undefined;
        recorderCalls = [];
        readFiles = [];
        recordedFiles = [];
        duringAnyGet = undefined;
        versionsOnceRead = undefined;
        holdBeforeRead = undefined;
        versionReads = 0;
        failedLookups = new Set();
        versionsReadThrows = false;
        missingConfigs = new Set();
        applyFailsWith = undefined;
        rejections = 0;
        connectorOnPlatform = false;
        deployedOnPlatform = {};
        realPinnedRead = false;
        planApproved = false;
        buildRequests = [];
        contentCompare = false;
        platformConnectorContent = "";
        successfulWrites = 0;
        manifestRecordNow = undefined;
        recordedByFile.clear();
        webuiGet.mockReset();
        configFetches = [];
        versions = new Map();
        platformStamp = { skill: "hash-written-by-session", connector: "t-written-by-session" };
        writes = 0;
        log.length = 0;
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
        vi.doMock("@/api/client", () => ({ api: { webui: { get: (...args: unknown[]) => webuiGet(...args) }, platform: { get: respondingAsV2(platformGet), post: respondingAsV2(platformPost), put: respondingAsV2(platformPut), delete: vi.fn() } } }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({ addNotification, displayError, uploadArtifactFile: vi.fn(), sessionId, allArtifacts: [], handleSubmit, isResponding: responding }),
        }));
        vi.doMock("@/providers/contexts/AuthContext", () => ({ useAuthContext: () => ({ userInfo: { username: "sam_dev_user" } }) }));
        vi.doMock("./hooks/useWorkflowDeployment", () => ({ useWorkflowDeployment: () => ({ deploy: vi.fn(), isLoading: false }) }));
        vi.doMock("./ConnectorEditDialog", () => ({ ConnectorEditDialog: () => null }));
        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: [], isLoading: false, refetch: () => Promise.resolve() }),
            getArtifactContentWithValidation: vi.fn(),
        }));
        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => serverApplyEnabled }));
        vi.doMock("@/common/product-analytics/report", () => ({ report: vi.fn(), reportingSessionId: () => "session-one" }));
        vi.doMock("@/api/builder", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/builder")>()), listManifestVersions: () => Promise.resolve([]) }));
        vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: () => versions }));
        const recorder = {
            versionsBeforeSave: async (components: Array<{ name: string; filename: string; type?: string; origin?: string }>) => {
                recorderCalls.push(components.map(c => c.name));
                readFiles.push(components.map(c => c.filename));
                versionReads++;
                await holdBeforeRead;
                if (versionsReadThrows) throw new Error("versions read threw");
                const saveOnly = saveOnlyFilenames(components);
                const at = new Map([...(versionsOnceRead ?? versions)].filter(([filename]) => saveOnly.includes(filename) && !failedLookups.has(filename)));
                return { at, failed: saveOnly.filter(filename => failedLookups.has(filename)) };
            },
            record: async (proof: { changed: string[]; matched: string[] }, readAt: ReadonlyMap<string, number>, components: Array<{ filename: string }>) => {
                recordedFiles.push(components.map(c => c.filename));
                const recorded = new Map([...proof.changed, ...proof.matched].flatMap(name => (readAt.has(filenameOf(name)) ? [[filenameOf(name), readAt.get(filenameOf(name)) as number] as const] : [])));
                manifestRecordNow = recorded.get(SAVED.connector.filename) ?? recorded.get(SAVED.skill.filename) ?? manifestRecordNow;
                for (const [filename, version] of recorded) recordedByFile.set(filename, version);
                return recorded;
            },
        };
        vi.doMock("./hooks/useRecordSavedComponents", () => ({ useRecordSavedComponents: () => recorder }));

        ({ bundleHash } = await import("@/api/skills/service"));

        platformGet.mockImplementation(async (url: string) => {
            const path = String(url).split("?")[0];
            log.push(`GET ${path}`);
            const interrupt = duringAnyGet;
            duringAnyGet = undefined;
            await interrupt?.();
            if (path === SKILLS_PATH) duringLookup?.();
            const deployedHere = Object.entries(deployedOnPlatform).find(([listing]) => path.endsWith(listing))?.[1];
            if (deployedHere)
                return v2PageOf(
                    path,
                    deployedHere.map(name => ({ id: `id-${name}`, name, deploymentStatus: "deployed" }))
                );
            if (path === CONNECTORS_PATH) return v2PageOf(path, connectorOnPlatform ? [{ id: SAVED.connector.id, name: SAVED.connector.name }] : []);
            if (path === SKILLS_PATH) return v2PageOf(path, [platformSkill()]);
            if (path === `${SKILLS_PATH}/${SAVED.skill.id}` || path === `${CONNECTORS_PATH}/${SAVED.connector.id}`) {
                if (readBackFails) throw new Error("offline");
                return path.startsWith(SKILLS_PATH) ? v2ResourceOf(path, platformSkill()) : v2ResourceOf(path, platformConnector());
            }
            return v2Page([]);
        });
        platformPost.mockImplementation(async (url: string, body?: unknown) => {
            const dryRun = Boolean((body as { dryRun?: boolean } | undefined)?.dryRun);
            log.push(`POST ${url}${dryRun ? " (dry run)" : ""}`);
            const isWrite = url === BUILDS_PATH && !dryRun;
            if (url === CONNECTORS_PATH) {
                if (connectorOnPlatform) return v2Error(409, 'a connector with name "db" already exists');
                connectorOnPlatform = true;
                platformStamp.connector = "t-created-per-kind";
                return v2ResourceOf(url, platformConnector());
            }
            if (url === BUILDS_PATH && !dryRun && applyFailsWith !== undefined) {
                const status = applyFailsWith;
                applyFailsWith = undefined;
                throw Object.assign(new Error(`the platform rejected the connector config (${++rejections})`), { status });
            }
            const sentContent = url === BUILDS_PATH ? ((body as { components?: Array<{ content?: string }> }).components ?? [])[0]?.content : undefined;
            const unchangedOnPlatform = contentCompare && url === BUILDS_PATH && sentContent === platformConnectorContent;
            if (isWrite && !unchangedOnPlatform && refuseWrites > 0) {
                refuseWrites--;
                throw Object.assign(new Error("the platform refused the write"), { status: 500 });
            }
            if (url === BUILDS_PATH) {
                buildRequests.push({ dryRun, names: ((body as { components?: Array<{ name: string }> }).components ?? []).map(c => c.name) });
                if (dryRun) await duringPlan?.();
                if (!dryRun && !unchangedOnPlatform) {
                    platformStamp.connector = `t-written-${++writes}`;
                    if (contentCompare) {
                        platformConnectorContent = sentContent ?? "";
                        successfulWrites++;
                    }
                    duringWrite?.();
                }
                const action = contentCompare ? (unchangedOnPlatform ? "unchanged" : "update") : dryRun ? (plannedActions.shift() ?? "update") : plannedActions.length > 0 || buildWarnings.length > 0 ? "unchanged" : "update";
                const agentsSent = ((body as { components?: Array<{ kind: string; name: string }> }).components ?? []).filter(c => c.kind === "agent");
                const agentOperations = agentsSent.map(agent => ({ kind: "agent", name: agent.name, action: "create", status: dryRun ? "planned" : "succeeded", resourceId: `agent-${agent.name}` }));
                return {
                    dryRun,
                    operations: [{ kind: "connector", name: SAVED.connector.name, action, status: dryRun ? "planned" : "succeeded", resourceId: SAVED.connector.id, changedFields: ["values"], warnings: buildWarnings }, ...agentOperations],
                    summary: { created: 0, updated: action === "update" ? 1 : 0, unchanged: action === "update" ? 0 : 1, failed: 0, skipped: 0 },
                };
            }
            if (url.endsWith("Deployments")) return v2DeploymentOf(body);
            if (url === AGENTS_PATH) return v2ResourceOf(url, { id: `agent-${(body as { name: string }).name}`, name: (body as { name: string }).name });
            return {};
        });
        platformPut.mockImplementation(async (url: string, body?: unknown) => {
            log.push(`PUT ${url}`);
            if (url !== `${SKILLS_PATH}/${SAVED.skill.id}/bundle`) return {};
            if (refuseWrites > 0) {
                refuseWrites--;
                return v2Error(500, "the platform refused the write");
            }
            platformStamp.skill = (await bundleHash((body as FormData).get("file") as File)) ?? "";
            successfulWrites++;
            duringWrite?.();
            return v2ResourceOf(url, platformSkill());
        });
        BuilderActions = withCurrentStamp((await import("./BuilderActions")).BuilderActions, filename => versions.get(filename));
        queryWrapper = newQueryWrapper();
    });

    afterEach(async () => {
        cleanup();
        await turnsOfTheLoop(10);
        Blob.prototype.arrayBuffer = originalArrayBuffer;
    });

    const platformSkill = () => ({ id: SAVED.skill.id, name: SAVED.skill.name, contentHash: platformStamp.skill, discoveryStatus: "ready" });
    const platformConnector = () => ({ id: SAVED.connector.id, name: SAVED.connector.name, updatedAt: instantOf(platformStamp.connector) });

    const filenameOf = (name: string) => Object.values(SAVED).find(s => s.name === name)?.filename ?? "";

    const rememberOwnWrite = (kind: Kind, stamp: string | null = platformStamp[kind]) => {
        const record = [{ id: SAVED[kind].id, kind, ...(stamp === null ? {} : { stamp: stampOnWire(kind, stamp) }) }];
        localStorage.setItem(OWNERSHIP_KEY, JSON.stringify({ [SESSION]: record, [OTHER_SESSION]: record }));
    };
    const rememberLegacyWrite = (kind: Kind) => localStorage.setItem(OWNERSHIP_KEY, JSON.stringify({ [SESSION]: [SAVED[kind].id] }));

    async function platformHoldsSkillAt(version: number) {
        const { saveSkillFromConfig } = await import("@/api/skills/service");
        const { skillsApi } = await import("@/api/skills/versioned");
        const { extractSkillBundleConfig } = await import("./builderUtils");
        await saveSkillFromConfig(skillsApi, { config: extractSkillBundleConfig(skillYamlAt(version))!, confirmReplace: async () => {} });
        log.length = 0;
    }

    async function sessionSavedAt(kind: Kind, version: number) {
        versions = new Map([[SAVED[kind].filename, version]]);
        if (kind === "skill") await platformHoldsSkillAt(version);
        rememberOwnWrite(kind);
    }

    const readConfig = async (filename: string, options?: { skipAccumulated?: boolean; version?: number }) => {
        configFetches.push([filename, options]);
        if (missingConfigs.has(filename)) return null;
        if (realPinnedRead && options?.version !== undefined) return (await import("./hooks/artifactUtils")).fetchArtifactAtVersion(sessionId, filename, options.version);
        if (pinnedReadFailsWith !== undefined && options?.version !== undefined) throw Object.assign(new Error("the platform could not be reached"), { status: pinnedReadFailsWith });
        if (filename === "agent.yaml") return AGENT_YAML;
        if (contentCompare && filename === SAVED.connector.filename) return connectorAt(options?.version ?? versions.get(filename) ?? 0);
        return filename === SAVED.skill.filename ? skillYamlAt((options?.version ?? versions.get(filename) ?? 0) + skillDrift) : CONNECTOR_JSON;
    };
    const element = (manifest: BuildManifest) => (
        <BuilderActions
            configYaml=""
            configType="agent"
            manifest={manifest}
            autoSave={planApproved}
            onAutoSaveComplete={() => {
                planApproved = false;
            }}
            fetchConfigByFilename={readConfig}
            {...(bridged
                ? {
                      onBuildActivateApiChange: (api: import("./BuilderActions").BuildActivateApi | null) => {
                          bridge = api;
                      },
                  }
                : {})}
        />
    );

    type View = ReturnType<typeof render>;

    async function takeATurn(view: View, manifest: BuildManifest, during?: () => void) {
        responding = true;
        view.rerender(element(manifest));
        await settle();
        during?.();
        view.rerender(element(manifest));
        await settle();
        responding = false;
        view.rerender(element(manifest));
        await settle();
    }

    async function mountAndTakeATurn(manifest: BuildManifest, during?: () => void) {
        const view = render(element(manifest), { wrapper: queryWrapper });
        await settle();
        await takeATurn(view, manifest, during);
        return view;
    }

    const mixedManifest = (): BuildManifest => ({
        name: "my-build",
        full_validation_passed: true,
        components: [
            { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session", deployed_version: 1 },
            { filename: SAVED.skill.filename, type: "skill", name: SAVED.skill.name, status: "validated", origin: "session", deployed_version: 1 },
        ],
    });
    const bothSavedAt = async (version: number) => {
        versions = new Map([
            [SAVED.connector.filename, version],
            [SAVED.skill.filename, version],
        ]);
        await platformHoldsSkillAt(version);
        const record = [
            { id: SAVED.connector.id, kind: "connector", stamp: stampOnWire("connector", platformStamp.connector) },
            { id: SAVED.skill.id, kind: "skill", stamp: platformStamp.skill },
        ];
        localStorage.setItem(OWNERSHIP_KEY, JSON.stringify({ [SESSION]: record, [OTHER_SESSION]: record }));
    };

    const editTo = (filename: string, version: number) => () => {
        versions = new Map([[filename, version]]);
    };

    describe.each<Kind>(["connector", "skill"])("a %s", kind => {
        const saved = SAVED[kind];
        const written = () => (kind === "connector" ? applies() : uploads());

        describe("a session reopened, sent a message that does not touch the component", () => {
            test("the platform still holds what the session saved: writes nothing and asks nothing", async () => {
                await sessionSavedAt(kind, 3);

                await mountAndTakeATurn(manifestFor(kind, { deployed_version: 3 }));

                expect(writesToPlatform()).toBe(0);
                expect(screen.queryByText(DIALOG)).toBeNull();
            });

            test("another session has replaced it since: never written over unasked", async () => {
                await sessionSavedAt(kind, 3);
                platformStamp[kind] = "t-or-hash-of-someone-else";

                await mountAndTakeATurn(manifestFor(kind, { deployed_version: 3 }));

                expect(writesToPlatform()).toBe(0);
                if (kind === "skill") await screen.findByText(DIALOG);
                else expect(screen.queryByText(DIALOG)).toBeNull();
            });

            test("with no record of what was saved, the first turn of a mount saves it as before, and asks about a platform copy it did not write", async () => {
                versions = new Map([[saved.filename, 3]]);
                platformStamp[kind] = "t-or-hash-of-someone-else";

                await mountAndTakeATurn(manifestFor(kind, { deployed_version: undefined }));

                await screen.findByText(DIALOG);
                expect(writesToPlatform()).toBe(0);
            });
        });

        describe("an edit made in the session", () => {
            test("is saved once the turn that made it ends, without asking, when the platform still holds what the session wrote", async () => {
                await sessionSavedAt(kind, 1);

                await mountAndTakeATurn(manifestFor(kind, { deployed_version: 1 }), editTo(saved.filename, 2));

                await waitFor(() => expect(written()).toBe(1));
                expect(screen.queryByText(DIALOG)).toBeNull();
            });

            test("is not saved while the turn that is making it is still running", async () => {
                await sessionSavedAt(kind, 1);
                const manifest = manifestFor(kind, { deployed_version: 1 });
                const view = render(element(manifest), { wrapper: queryWrapper });
                await settle();

                responding = true;
                view.rerender(element(manifest));
                await settle();
                versions = new Map([[saved.filename, 2]]);
                view.rerender(element(manifest));
                await settle();

                expect(writesToPlatform()).toBe(0);

                responding = false;
                view.rerender(element(manifest));
                await waitFor(() => expect(written()).toBe(1));
            });

            test("is not saved a second time once the save has recorded the version it was at", async () => {
                await sessionSavedAt(kind, 1);
                const manifest = manifestFor(kind, { deployed_version: 1 });
                const view = await mountAndTakeATurn(manifest, editTo(saved.filename, 2));
                await waitFor(() => expect(written()).toBe(1));

                await takeATurn(view, manifest);
                await takeATurn(view, manifest);

                await waitFor(() => expect(written()).toBe(1));
            });

            test("is saved after a later edit too, when the manifest lost the record of the first save", async () => {
                await sessionSavedAt(kind, 1);
                const manifest = manifestFor(kind, { deployed_version: undefined });
                const view = await mountAndTakeATurn(manifest);
                const afterFirstSave = writesToPlatform();

                await takeATurn(view, manifest, editTo(saved.filename, 2));

                await waitFor(() => expect(writesToPlatform()).toBe(afterFirstSave + 1));
            });

            test("is saved, and the one after it too, when an edit lands while the save is in flight", async () => {
                await sessionSavedAt(kind, 1);
                const manifest = manifestFor(kind, { deployed_version: 1 });
                duringWrite = () => {
                    duringWrite = undefined;
                    versions = new Map([[saved.filename, 3]]);
                };

                await mountAndTakeATurn(manifest, editTo(saved.filename, 2));

                await waitFor(() => expect(written()).toBe(2));
            });

            test("waits for a turn when the session was only reopened: nothing says the author wants it saved yet", async () => {
                await sessionSavedAt(kind, 2);

                render(element(manifestFor(kind, { deployed_version: 1 })), { wrapper: queryWrapper });
                await settle();

                expect(writesToPlatform()).toBe(0);
            });

            test("is judged by this session's own record of what it saved, not by one made in another session", async () => {
                await sessionSavedAt(kind, 1);
                const manifest = manifestFor(kind, { deployed_version: undefined });
                const view = await mountAndTakeATurn(manifest);
                const afterFirstSave = writesToPlatform();

                rememberOwnWrite(kind);
                sessionId = OTHER_SESSION;
                view.rerender(element(manifest));
                await takeATurn(view, manifest, editTo(saved.filename, 2));

                await waitFor(() => expect(writesToPlatform()).toBe(afterFirstSave + (kind === "skill" ? 1 : 0)));
            });

            describe("in a session whose component was replaced since it wrote it", () => {
                const editedAndReopened = (stamp: string | null) => {
                    rememberOwnWrite(kind, stamp);
                    versions = new Map([[saved.filename, 2]]);
                    return manifestFor(kind, { deployed_version: 1 });
                };

                test.each([
                    ["the platform now holds something else", () => (platformStamp[kind] = "t-or-hash-of-someone-else"), () => editedAndReopened("t-or-hash-the-session-wrote")],
                    ["its platform copy cannot be read back", () => (readBackFails = true), () => editedAndReopened("t-or-hash-the-session-wrote")],
                    ["the write was recorded without a stamp", () => undefined, () => editedAndReopened(null)],
                    [
                        "the write was recorded before stamps were kept",
                        () => undefined,
                        () => {
                            rememberLegacyWrite(kind);
                            versions = new Map([[saved.filename, 2]]);
                            return manifestFor(kind, { deployed_version: 1 });
                        },
                    ],
                ])("%s: asks first", async (_label, changePlatform, reopen) => {
                    const manifest = reopen();
                    changePlatform();

                    await mountAndTakeATurn(manifest);

                    await screen.findByText(DIALOG);
                    expect(writesToPlatform()).toBe(0);
                });

                test("replaces it once the user confirms, and does not ask again for the edit after", async () => {
                    platformStamp[kind] = "t-or-hash-of-someone-else";
                    const manifest = editedAndReopened("t-or-hash-the-session-wrote");
                    const view = await mountAndTakeATurn(manifest);

                    await screen.findByText(DIALOG);
                    screen.getByRole("button", { name: "Replace and save" }).click();
                    await waitFor(() => expect(written()).toBe(1));
                    await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());

                    await takeATurn(view, manifest, editTo(saved.filename, 3));

                    expect(screen.queryByText(DIALOG)).toBeNull();
                    await waitFor(() => expect(written()).toBe(2));
                });

                test("leaves the platform copy alone when the user declines", async () => {
                    platformStamp[kind] = "t-or-hash-of-someone-else";
                    await mountAndTakeATurn(editedAndReopened("t-or-hash-the-session-wrote"));

                    await screen.findByText(DIALOG);
                    screen.getByRole("button", { name: "Cancel" }).click();
                    await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
                    await settle();

                    expect(writesToPlatform()).toBe(0);
                });
            });
        });
    });

    describe("with server-side apply off", () => {
        beforeEach(() => {
            serverApplyEnabled = false;
        });

        test("an edited skill is still saved, since a skill is updated through its own upload", async () => {
            await sessionSavedAt("skill", 1);

            await mountAndTakeATurn(manifestFor("skill", { deployed_version: 1 }), editTo(SAVED.skill.filename, 2));

            await waitFor(() => expect(uploads()).toBe(1));
        });

        test("an edited skill whose platform copy was replaced since the session wrote it asks first, as it does with apply on", async () => {
            await sessionSavedAt("skill", 1);
            platformStamp.skill = "hash-of-someone-else";

            await mountAndTakeATurn(manifestFor("skill", { deployed_version: 1 }), editTo(SAVED.skill.filename, 2));

            await screen.findByText(DIALOG);
            expect(uploads()).toBe(0);
        });

        test("a skill changed after the guard looked and before its own question is asked about, not replaced", async () => {
            await sessionSavedAt("skill", 1);
            duringLookup = () => {
                duringLookup = undefined;
                platformStamp.skill = "hash-of-someone-else-after-the-check";
            };

            await mountAndTakeATurn(manifestFor("skill", { deployed_version: 1 }), editTo(SAVED.skill.filename, 2));

            await screen.findByText(DIALOG);
            expect(uploads()).toBe(0);
        });

        test("an edited connector is not saved: the per-kind path cannot update one", async () => {
            await sessionSavedAt("connector", 1);

            await mountAndTakeATurn(manifestFor("connector", { deployed_version: 1 }), editTo(SAVED.connector.filename, 2));

            expect(applies()).toBe(0);
        });
    });

    describe("a skill whose bundle changes in a way its YAML version does not show", () => {
        test("is saved at the end of the turn that changed it, on the first turn of a mount and on a later one", async () => {
            await sessionSavedAt("skill", 1);
            const manifest = manifestFor("skill", { deployed_version: 1 });
            const view = await mountAndTakeATurn(manifest, () => (skillDrift = 100));
            await waitFor(() => expect(uploads()).toBe(1));

            await takeATurn(view, manifest, () => (skillDrift = 200));

            await waitFor(() => expect(uploads()).toBe(2));
        });

        test("is put to the save at the end of an unrelated turn, which compares bundles: nothing is written and nothing is announced", async () => {
            await sessionSavedAt("skill", 1);

            await mountAndTakeATurn(manifestFor("skill", { deployed_version: 1 }));

            expect(uploads()).toBe(0);
            expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("saved successfully"), "success");
        });
    });

    describe("a save reads each connector and skill at the version it records", () => {
        test.each<Kind>(["connector", "skill"])("a %s edit is read at the version standing when the save began, not the latest", async kind => {
            await sessionSavedAt(kind, 1);
            duringWrite = undefined;

            await mountAndTakeATurn(manifestFor(kind, { deployed_version: 1 }), editTo(SAVED[kind].filename, 2));

            const reads = configFetches.filter(([filename]) => filename === SAVED[kind].filename).map(([, options]) => options?.version);
            expect(reads).toContain(2);
            expect(reads.every(version => version !== undefined)).toBe(true);
        });
    });

    describe("a config moved to a new file keeps the number its name carried", () => {
        test("a record above the new file's version is not trusted: the connector is put to the save", async () => {
            rememberOwnWrite("connector");
            versions = new Map([[SAVED.connector.filename, 1]]);

            await mountAndTakeATurn(manifestFor("connector", { deployed_version: 6 }));

            await waitFor(() => expect(applies()).toBe(1));
        });
    });

    describe("a build with a connector and a skill", () => {
        test.each([
            ["asking", false],
            ["the user's opt-out from being asked set", true],
        ])("an unrelated turn leaves a connector replaced elsewhere alone, with %s: nothing says it changed, so it is not sent", async (_label, optOut) => {
            await bothSavedAt(1);
            platformStamp.connector = "t-of-someone-else";
            if (optOut) localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");

            await mountAndTakeATurn(mixedManifest());

            expect(applies()).toBe(0);
            expect(screen.queryByText(DIALOG)).toBeNull();
        });

        test("a connector edit is saved with it, and a skill edit with it, each once", async () => {
            await bothSavedAt(1);

            await mountAndTakeATurn(mixedManifest(), () => {
                versions = new Map([
                    [SAVED.connector.filename, 2],
                    [SAVED.skill.filename, 2],
                ]);
            });

            await waitFor(() => expect(applies()).toBe(1));
            await waitFor(() => expect(uploads()).toBe(1));
        });

        test("a replacement asked about stays one question for what was already asked, and a component changed while the dialog was open is asked about before it is replaced", async () => {
            await bothSavedAt(1);
            platformStamp.skill = "hash-of-someone-else";

            await mountAndTakeATurn(mixedManifest(), () => {
                versions = new Map([
                    [SAVED.connector.filename, 2],
                    [SAVED.skill.filename, 1],
                ]);
            });
            await screen.findByText(DIALOG);
            expect(screen.queryByText("db")).toBeNull();
            platformStamp.connector = "t-of-a-colleague-while-the-dialog-was-open";
            screen.getByRole("button", { name: "Replace and save" }).click();

            await waitFor(() => expect(screen.getByText("db")).toBeTruthy());
            expect(screen.queryByText("TestSkill")).toBeNull();
            expect(applies()).toBe(0);
            expect(uploads()).toBe(0);

            screen.getByRole("button", { name: "Replace and save" }).click();

            await waitFor(() => expect(applies()).toBe(1));
            await waitFor(() => expect(uploads()).toBe(1));
            await settle();
        });
    });

    describe("a write the platform refuses", () => {
        test.each<Kind>(["connector", "skill"])("a %s edit stays unsaved, is not retried in a loop, and goes through when the author retries", async kind => {
            await sessionSavedAt(kind, 1);
            refuseWrites = 1;
            const written = () => (kind === "connector" ? applies() : uploads());

            await mountAndTakeATurn(manifestFor(kind, { deployed_version: 1 }), editTo(SAVED[kind].filename, 2));

            await waitFor(() => expect(written()).toBe(1));
            expect(platformStamp[kind]).toBe(kind === "connector" ? "t-written-by-session" : platformStamp.skill);
            const retry = await screen.findByRole("button", { name: "Retry" });
            await settle();
            await waitFor(() => expect(written()).toBe(1));

            retry.click();

            await waitFor(() => expect(written()).toBe(2));
        });
    });

    describe("a platform copy changed between the check and the write", () => {
        test("after the save looked and before it planned: asked about, not replaced", async () => {
            await sessionSavedAt("connector", 1);
            duringPlan = () => {
                platformStamp.connector = "t-of-someone-else-after-the-check";
            };

            await mountAndTakeATurn(manifestFor("connector", { deployed_version: 1 }), editTo(SAVED.connector.filename, 2));

            await screen.findByText(DIALOG);
            expect(applies()).toBe(0);
        });
    });

    describe("a session switch", () => {
        test("leaves the session it arrives at alone until its own agent has responded", async () => {
            await sessionSavedAt("connector", 1);
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = await mountAndTakeATurn(manifest, editTo(SAVED.connector.filename, 2));
            await waitFor(() => expect(applies()).toBe(1));

            rememberOwnWrite("connector");
            sessionId = OTHER_SESSION;
            versions = new Map([[SAVED.connector.filename, 3]]);
            view.rerender(element(manifest));
            await settle();

            await waitFor(() => expect(applies()).toBe(1));
        });

        test("withdraws the question, and writes nothing in the session left", async () => {
            await sessionSavedAt("connector", 1);
            platformStamp.connector = "t-of-someone-else";
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = await mountAndTakeATurn(manifest, editTo(SAVED.connector.filename, 2));
            await screen.findByText(DIALOG);

            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));

            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await settle();
            expect(writesToPlatform()).toBe(0);

            rememberOwnWrite("connector");
            await takeATurn(view, manifest, editTo(SAVED.connector.filename, 3));

            await waitFor(() => expect(applies()).toBe(1));
        });

        test("leaves Try again in the session it arrives at to deploy, not to save what nobody there asked for", async () => {
            bridged = true;
            await sessionSavedAt("skill", 1);
            const manifest = manifestFor("skill", { deployed_version: 1 });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            await takeATurn(view, manifest);
            platformStamp.skill = "hash-of-someone-else";
            await takeATurn(view, manifest, editTo(SAVED.skill.filename, 2));
            await screen.findByText(DIALOG);
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());

            bridge?.retry();
            await settle();

            expect(screen.queryByText(DIALOG)).toBeNull();
            expect(uploads()).toBe(0);
        });
    });

    describe("a question the author declined", () => {
        const manifestOf = (kind: Kind | "both") => (kind === "both" ? mixedManifest() : manifestFor(kind, { deployed_version: 1 }));
        const savedAtOne = async (kind: Kind | "both") => {
            if (kind === "both") await bothSavedAt(1);
            else await sessionSavedAt(kind, 1);
        };
        const editOne = (kind: Kind | "both", version: number) => () => {
            versions = new Map(kind === "both" ? [[SAVED.skill.filename, version]] : [[SAVED[kind].filename, version]]);
            if (kind === "both") versions.set(SAVED.connector.filename, version);
        };

        test.each<[string, Kind | "both"]>([
            ["a connector", "connector"],
            ["a skill", "skill"],
            ["a connector and a skill", "both"],
        ])("%s: later turns that edit nothing do not ask again, a new edit does, and Try again asks at once", async (_label, kind) => {
            await savedAtOne(kind);
            platformStamp.connector = "t-of-someone-else";
            platformStamp.skill = "hash-of-someone-else";
            const manifest = manifestOf(kind);
            const view = await mountAndTakeATurn(manifest, editOne(kind, 2));
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());

            await takeATurn(view, manifest);
            await takeATurn(view, manifest);

            expect(screen.queryByText(DIALOG)).toBeNull();
            expect(writesToPlatform()).toBe(0);

            await takeATurn(view, manifest, editOne(kind, 3));

            await screen.findByText(DIALOG);
        });

        test("Try again after a decline asks at once", async () => {
            await sessionSavedAt("skill", 1);
            platformStamp.skill = "hash-of-someone-else";
            await mountAndTakeATurn(manifestFor("skill", { deployed_version: 1 }), editTo(SAVED.skill.filename, 2));
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            const retry = await screen.findByRole("button", { name: "Retry" });

            retry.click();

            await screen.findByText(DIALOG);
        });
    });

    describe("Try again on a build with a connector and a skill", () => {
        test("sends only what changed, so a connector somebody else changed is not reverted, with the opt-out set", async () => {
            await bothSavedAt(1);
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            refuseWrites = 1;
            await mountAndTakeATurn(mixedManifest(), () => {
                versions = new Map([
                    [SAVED.connector.filename, 1],
                    [SAVED.skill.filename, 2],
                ]);
            });
            const retry = await screen.findByRole("button", { name: "Retry" });
            platformStamp.connector = "t-of-a-colleague";

            retry.click();

            await waitFor(() => expect(uploads()).toBe(2));
            expect(applies()).toBe(0);
        });
    });

    describe("a read at a pinned version that fails", () => {
        test.each<Kind>(["connector", "skill"])("a %s save lands on the Try again banner, and the builder is not sent to fix a config that is fine", async kind => {
            await sessionSavedAt(kind, 1);
            pinnedReadFailsWith = 503;

            await mountAndTakeATurn(manifestFor(kind, { deployed_version: 1 }), editTo(SAVED[kind].filename, 2));

            await screen.findByRole("button", { name: "Retry" });
            expect(handleSubmit).not.toHaveBeenCalled();
            expect(writesToPlatform()).toBe(0);
        });
    });

    describe("a platform copy that changes while a question is being put", () => {
        test("a skill changed after the guard looked and before the question: asked about, not replaced", async () => {
            await sessionSavedAt("skill", 1);
            duringLookup = () => {
                duringLookup = undefined;
                platformStamp.skill = "hash-of-someone-else-after-the-check";
            };

            await mountAndTakeATurn(manifestFor("skill", { deployed_version: 1 }), editTo(SAVED.skill.filename, 2));

            await screen.findByText(DIALOG);
            expect(uploads()).toBe(0);
        });

        test("a connector the first plan found unchanged, changed while the question about a skill was open: planned again and asked about before the apply", async () => {
            await bothSavedAt(1);
            platformStamp.skill = "hash-of-someone-else";
            plannedActions = ["unchanged", "update"];

            await mountAndTakeATurn(mixedManifest(), () => {
                versions = new Map([
                    [SAVED.connector.filename, 2],
                    [SAVED.skill.filename, 1],
                ]);
            });
            await screen.findByText(DIALOG);
            expect(screen.queryByText("db")).toBeNull();
            platformStamp.connector = "t-of-a-colleague-while-the-dialog-was-open";
            screen.getByRole("button", { name: "Replace and save" }).click();

            await waitFor(() => expect(screen.getByText("db")).toBeTruthy());
            expect(applies()).toBe(0);
        });
    });

    describe("what a save says", () => {
        test("a save-only save that wrote says so", async () => {
            await sessionSavedAt("connector", 1);

            await mountAndTakeATurn(manifestFor("connector", { deployed_version: 1 }), editTo(SAVED.connector.filename, 2));

            await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("saved successfully"), "success"));
        });

        test("a save that wrote nothing but was warned by the server passes the warning on, and says nothing else", async () => {
            versions = new Map([[SAVED.connector.filename, 1]]);
            rememberOwnWrite("connector");
            buildWarnings = ["part of the connector's config was declined"];

            await mountAndTakeATurn(manifestFor("connector", { deployed_version: undefined }));

            await waitFor(() => expect(addNotification).toHaveBeenCalledWith("db: part of the connector's config was declined", "warning"));
            expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("saved successfully"), "success");
        });
    });

    describe("a skill build waits on the end of a turn, not its start", () => {
        test("a turn that has begun and not ended sends nothing", async () => {
            await sessionSavedAt("skill", 1);
            skillDrift = 100;
            const manifest = manifestFor("skill", { deployed_version: 1 });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();

            responding = true;
            view.rerender(element(manifest));
            await settle();
            expect(uploads()).toBe(0);

            responding = false;
            view.rerender(element(manifest));
            await waitFor(() => expect(uploads()).toBe(1));
        });
    });

    describe("a number the manifest carried over from another file", () => {
        test("is not preferred to what this mount saved: later edits are saved too", async () => {
            rememberOwnWrite("connector");
            versions = new Map([[SAVED.connector.filename, 1]]);
            const manifest = manifestFor("connector", { deployed_version: 6 });
            const view = await mountAndTakeATurn(manifest);
            await waitFor(() => expect(applies()).toBe(1));

            await takeATurn(view, manifest, editTo(SAVED.connector.filename, 2));
            await takeATurn(view, manifest, editTo(SAVED.connector.filename, 3));

            await waitFor(() => expect(applies()).toBe(3));
        });
    });

    describe("what a save is handed to record", () => {
        test("is every component to be read, and the components it sends to record: a connector proven unchanged is read and not recorded", async () => {
            await bothSavedAt(1);

            await mountAndTakeATurn(mixedManifest());

            await waitFor(() => expect(recordedFiles.length).toBeGreaterThan(0));
            expect(recorderCalls.every(names => names.includes(SAVED.connector.name) && names.includes(SAVED.skill.name))).toBe(true);
            expect(recordedFiles.every(files => !files.includes(SAVED.connector.filename))).toBe(true);
        });
    });

    describe("a session switch with a save in flight", () => {
        test("the question is not put in the session switched to, and no banner is left there", async () => {
            await sessionSavedAt("connector", 1);
            platformStamp.connector = "t-of-someone-else";
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            let planStarted = false;
            let finishPlan = () => {};
            duringPlan = () => {
                duringPlan = undefined;
                planStarted = true;
                return new Promise<void>(resolve => {
                    finishPlan = resolve;
                });
            };
            responding = true;
            view.rerender(element(manifest));
            await settle();
            versions = new Map([[SAVED.connector.filename, 2]]);
            view.rerender(element(manifest));
            responding = false;
            view.rerender(element(manifest));
            await waitFor(() => expect(planStarted).toBe(true));

            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            finishPlan();
            await settle();

            expect(screen.queryByText(DIALOG)).toBeNull();
            expect(screen.queryByText(/Save cancelled/)).toBeNull();
            expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
            expect(applies()).toBe(0);
        });
    });

    describe("every way of starting a save", () => {
        type EntryPoint = "Try again" | "Build & Activate" | "plan approval";
        const ENTRY_POINTS: EntryPoint[] = ["Try again", "Build & Activate", "plan approval"];
        const enter = async (entry: EntryPoint, view: View, manifest: BuildManifest) => {
            if (entry === "plan approval") {
                planApproved = true;
                view.rerender(element(manifest));
                return;
            }
            (await screen.findByRole("button", { name: entry === "Try again" ? "Retry" : "Build & Activate" })).click();
        };

        test.each(ENTRY_POINTS)("%s on a connector and a skill sends only what changed, so a connector somebody else changed is not reverted, with the opt-out set", async entry => {
            await bothSavedAt(1);
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            refuseWrites = 1;
            const manifest = mixedManifest();
            const view = await mountAndTakeATurn(manifest, () => {
                versions = new Map([
                    [SAVED.connector.filename, 1],
                    [SAVED.skill.filename, 2],
                ]);
            });
            await screen.findByRole("button", { name: "Retry" });
            platformStamp.connector = "t-of-a-colleague";

            await enter(entry, view, manifest);

            await waitFor(() => expect(uploads()).toBe(2));
            await settle();
            expect(applies()).toBe(0);
        });

        test.each(ENTRY_POINTS)("%s lifts a decline: after its write failed, the next turn asks again", async entry => {
            await sessionSavedAt("skill", 1);
            const manifest = manifestFor("skill", { deployed_version: 1 });
            platformStamp.skill = "hash-of-someone-else";
            const view = await mountAndTakeATurn(manifest, editTo(SAVED.skill.filename, 2));
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            refuseWrites = 1;

            await enter(entry, view, manifest);

            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Replace and save" }).click();
            await waitFor(() => expect(uploads()).toBe(1));
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await takeATurn(view, manifest);
            await screen.findByText(DIALOG);
        });

        test("a deployable build sends its connector with the agent: only a save-only build leaves a proven-unchanged one out", async () => {
            rememberOwnWrite("connector");
            versions = new Map([
                [SAVED.connector.filename, 1],
                ["agent.yaml", 1],
            ]);
            const manifest: BuildManifest = {
                name: "my-build",
                full_validation_passed: true,
                components: [
                    { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session" },
                    { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session", deployed_version: 1 },
                ],
            };
            planApproved = true;

            render(element(manifest), { wrapper: queryWrapper });
            await settle();

            expect(buildRequests.length).toBeGreaterThan(0);
            expect(buildRequests.every(request => request.names.includes("db") && request.names.includes("TestAgent"))).toBe(true);
        });

        test.each([
            ["an agent", "agent", "agent.yaml"],
            ["a workflow", "workflow", "wf.yaml"],
            ["a gateway", "gateway", "gw.yaml"],
        ])("%s config is read at its latest version, not at the one the list shows, which trails an edit made in the turn", async (_label, type, filename) => {
            rememberOwnWrite("connector");
            versions = new Map([
                [SAVED.connector.filename, 1],
                [filename, 1],
            ]);
            const manifest: BuildManifest = {
                name: "my-build",
                full_validation_passed: true,
                components: [
                    { filename, type, name: "TestAgent", status: "validated", origin: "session" },
                    { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session", deployed_version: 1 },
                ],
            };
            planApproved = true;

            render(element(manifest), { wrapper: queryWrapper });
            await settle();

            const versionsRead = (name: string) => configFetches.filter(([read]) => read === name).map(([, options]) => options?.version);
            expect(versionsRead(filename).length).toBeGreaterThan(0);
            expect(versionsRead(filename).every(version => version === undefined)).toBe(true);
            expect(versionsRead(SAVED.connector.filename).every(version => version === 1)).toBe(true);
        });
    });

    describe("a read at a pinned version that fails, through the real artifact read", () => {
        const answers: Array<[string, () => Promise<unknown>]> = [
            ["the network is down", () => Promise.reject(new TypeError("Failed to fetch"))],
            ["the platform answers 503", async () => ({ ok: false, status: 503 })],
            ["the copy the save has just listed is gone (404)", async () => ({ ok: false, status: 404 })],
            ["the session has lapsed (401)", async () => ({ ok: false, status: 401 })],
        ];

        test.each(answers.flatMap(([label, answer]) => (["connector", "skill"] as Kind[]).map(kind => [label, kind, answer] as const)))(
            "%s, a %s: lands on Try again, and the builder is not sent to fix a config that is fine",
            async (_label, kind, answer) => {
                await sessionSavedAt(kind, 1);
                realPinnedRead = true;
                webuiGet.mockImplementation(answer);

                await mountAndTakeATurn(manifestFor(kind, { deployed_version: 1 }), editTo(SAVED[kind].filename, 2));

                await screen.findByRole("button", { name: "Retry" });
                expect(handleSubmit).not.toHaveBeenCalled();
                expect(writesToPlatform()).toBe(0);
            }
        );
    });

    describe("with server-side apply off, a connector", () => {
        const connectorWithoutRecord = (): BuildManifest => ({ ...mixedManifest(), components: mixedManifest().components?.map(c => (c.type === "connector" ? { ...c, deployed_version: undefined } : c)) });
        const connectorPosts = () => log.filter(r => r === `POST ${CONNECTORS_PATH}`).length;

        beforeEach(() => {
            serverApplyEnabled = false;
            connectorOnPlatform = true;
        });

        test("the session wrote, which the platform holds, is not created again at each turn end: it would be refused each time", async () => {
            await bothSavedAt(1);
            const manifest = connectorWithoutRecord();

            const view = await mountAndTakeATurn(manifest);
            await takeATurn(view, manifest);
            await takeATurn(view, manifest);
            await settle();

            expect(connectorPosts()).toBe(0);
            expect(displayError).not.toHaveBeenCalled();
            expect(handleSubmit).not.toHaveBeenCalled();
        });

        test("the session did not write, which the platform holds, is still sent, so that a name somebody else holds is reported", async () => {
            versions = new Map([
                [SAVED.connector.filename, 1],
                [SAVED.skill.filename, 1],
            ]);
            await platformHoldsSkillAt(1);
            const onlyTheSkill = [{ id: SAVED.skill.id, kind: "skill", stamp: platformStamp.skill }];
            localStorage.setItem(OWNERSHIP_KEY, JSON.stringify({ [SESSION]: onlyTheSkill }));

            await mountAndTakeATurn(connectorWithoutRecord());

            await waitFor(() => expect(connectorPosts()).toBeGreaterThan(0));
            await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        });
    });

    describe("a decline leaves exactly one way back on the plan card", () => {
        type Component = BuildManifest["components"] extends (infer C)[] | undefined ? C : never;
        const connector = (extra: Partial<Component> = {}): Component => ({ filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session", ...extra });
        const skill = (extra: Partial<Component> = {}): Component => ({ filename: SAVED.skill.filename, type: "skill", name: SAVED.skill.name, status: "validated", origin: "session", ...extra });
        const agent = (extra: Partial<Component> = {}): Component => ({ filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session", ...extra });
        const workflow = (extra: Partial<Component> = {}): Component => ({ filename: "wf.yaml", type: "workflow", name: "TestFlow", status: "validated", origin: "session", ...extra });
        const gateway = (extra: Partial<Component> = {}): Component => ({ filename: "gw.yaml", type: "gateway", name: "TestGateway", status: "validated", origin: "session", ...extra });
        const recorded = { deployed_version: 1 };

        interface State {
            label: string;
            components: Component[];
            versions: Array<[string, number]>;
            deployed: Record<string, string[]>;
            connectorAsks: boolean;
            entry?: "try again";
        }
        const cleanAgent = ["agent.yaml", 1] as [string, number];
        const STATES: State[] = [
            { label: "a skill never saved", components: [skill()], versions: [[SAVED.skill.filename, 1]], deployed: {}, connectorAsks: false },
            { label: "a connector never saved", components: [connector()], versions: [[SAVED.connector.filename, 1]], deployed: {}, connectorAsks: true },
            { label: "a skill edited since its save", components: [skill(recorded)], versions: [[SAVED.skill.filename, 2]], deployed: {}, connectorAsks: false },
            { label: "a connector edited since its save", components: [connector(recorded)], versions: [[SAVED.connector.filename, 2]], deployed: {}, connectorAsks: true },
            {
                label: "a connector and a skill never saved",
                components: [connector(), skill()],
                versions: [
                    [SAVED.connector.filename, 1],
                    [SAVED.skill.filename, 1],
                ],
                deployed: {},
                connectorAsks: false,
            },
            { label: "an undeployed agent and a connector never saved", components: [agent(), connector()], versions: [cleanAgent, [SAVED.connector.filename, 1]], deployed: {}, connectorAsks: true },
            { label: "a deployed, clean agent and a connector never saved", components: [agent(recorded), connector()], versions: [cleanAgent, [SAVED.connector.filename, 1]], deployed: { "/agents": ["TestAgent"] }, connectorAsks: true },
            {
                label: "a deployed, clean agent and a connector edited since its save",
                components: [agent(recorded), connector(recorded)],
                versions: [cleanAgent, [SAVED.connector.filename, 2]],
                deployed: { "/agents": ["TestAgent"] },
                connectorAsks: true,
            },
            {
                label: "a deployed, clean workflow and a connector never saved",
                components: [workflow(recorded), connector()],
                versions: [
                    ["wf.yaml", 1],
                    [SAVED.connector.filename, 1],
                ],
                deployed: { "/workflows": ["TestFlow"] },
                connectorAsks: true,
            },
            {
                label: "a deployed, clean gateway and a connector edited since its save",
                components: [gateway({ ...recorded, status: "deployed" }), connector(recorded)],
                versions: [
                    ["gw.yaml", 1],
                    [SAVED.connector.filename, 2],
                ],
                deployed: { "/entrypoints": ["TestGateway"] },
                connectorAsks: true,
                entry: "try again",
            },
            {
                label: "a deployed, clean gateway and a skill edited since its save",
                components: [gateway({ ...recorded, status: "deployed" }), skill(recorded)],
                versions: [
                    ["gw.yaml", 1],
                    [SAVED.skill.filename, 2],
                ],
                deployed: { "/entrypoints": ["TestGateway"] },
                connectorAsks: false,
                entry: "try again",
            },
            {
                label: "an undeployed agent, a blocked component and a connector never saved",
                components: [agent(), agent({ filename: "other.yaml", name: "OtherAgent", status: "blocked" }), connector()],
                versions: [[SAVED.connector.filename, 1]],
                deployed: {},
                connectorAsks: true,
                entry: "try again",
            },
            {
                label: "an undeployed agent, a component not yet validated and a connector never saved",
                components: [agent(), agent({ filename: "other.yaml", name: "OtherAgent", status: "draft" }), connector()],
                versions: [[SAVED.connector.filename, 1]],
                deployed: {},
                connectorAsks: true,
                entry: "try again",
            },
            {
                label: "an agent edited since its deploy and a connector never saved",
                components: [agent(recorded), connector()],
                versions: [
                    ["agent.yaml", 2],
                    [SAVED.connector.filename, 1],
                ],
                deployed: { "/agents": ["TestAgent"] },
                connectorAsks: true,
            },
        ];
        const CASES = STATES.flatMap(state => ([true, false] as const).filter(serverApply => serverApply || !state.connectorAsks).map(serverApply => [`${state.label}, server-side apply ${serverApply ? "on" : "off"}`, state, serverApply] as const));

        let tryAgainNow: (() => void) | undefined;

        function PlanCardOnBridge({ manifest }: Readonly<{ manifest: BuildManifest }>) {
            const bridgeState = useBuildActivateBridge();
            const [autoSave, setAutoSave] = React.useState(true);
            const buildCompleteCtaShowing = isBuildCompleteCtaShowing(manifest.components, { versions, serverApplyEnabled });
            tryAgainNow = bridgeState.handleRedeploy;
            return (
                <TooltipProvider>
                    <BuilderActions
                        configYaml=""
                        configType="agent"
                        manifest={manifest}
                        autoSave={autoSave}
                        onAutoSaveComplete={() => setAutoSave(false)}
                        fetchConfigByFilename={readConfig}
                        onBuildActivateApiChange={bridgeState.handleBuildActivateApiChange}
                    />
                    <InlinePlanCard
                        manifest={manifest}
                        isApproved
                        onApprove={vi.fn()}
                        onRequestChanges={vi.fn()}
                        canBuildActivate={bridgeState.buildActivate.can && !buildCompleteCtaShowing}
                        isBuildActivating={bridgeState.buildActivate.busy}
                        onBuildActivate={bridgeState.handleBuildActivate}
                        deployFailure={bridgeState.deployFailure}
                        onRetry={bridgeState.handleRedeploy}
                    />
                </TooltipProvider>
            );
        }

        const onTheCard = () => ({
            buildActivate: screen.queryByRole("button", { name: "Build & Activate" }) !== null,
            tryAgain: screen.queryByRole("button", { name: "Try again" }) !== null,
            banner: screen.queryByText(/Save cancelled/) !== null,
        });

        async function declineOnTheCard(state: State, serverApply: boolean) {
            localStorage.setItem("sam_builder_skip_activate_confirm", "true");
            serverApplyEnabled = serverApply;
            versions = new Map(state.versions);
            deployedOnPlatform = state.deployed;
            connectorOnPlatform = true;
            render(<PlanCardOnBridge manifest={{ name: "my-build", full_validation_passed: true, components: state.components }} />, { wrapper: queryWrapper });
            if (state.entry === "try again") await act(async () => tryAgainNow?.());
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await settle();
        }

        test.each(CASES)("%s: the card shows Build & Activate or the banner with Try again, and never both or neither", async (_label, state, serverApply) => {
            await declineOnTheCard(state, serverApply);

            const shown = onTheCard();
            expect(shown.buildActivate !== (shown.tryAgain && shown.banner), JSON.stringify(shown)).toBe(true);
            expect(shown.tryAgain).toBe(shown.banner);
        });

        test("an agent the manifest does not yet record as deployed, beside a connector never saved, goes back to Build & Activate", async () => {
            await declineOnTheCard(STATES[6], true);

            expect(onTheCard()).toEqual({ buildActivate: true, tryAgain: false, banner: false });
        });

        test("an undeployed agent beside a connector never saved goes back to Build & Activate", async () => {
            await declineOnTheCard(STATES[5], true);

            expect(onTheCard()).toEqual({ buildActivate: true, tryAgain: false, banner: false });
        });

        test("a deployed gateway beside a connector edited since its save is offered the banner, since the completion card hides Build & Activate", async () => {
            await declineOnTheCard(STATES[9], true);

            expect(onTheCard()).toEqual({ buildActivate: false, tryAgain: true, banner: true });
        });

        test.each([
            ["a connector alone", STATES[1], { buildActivate: false, tryAgain: true, banner: true }],
            ["an undeployed agent and a connector", STATES[5], { buildActivate: true, tryAgain: false, banner: false }],
        ])("a real failure reads as one, and declining the question Try again then asks leaves the way back for %s only", async (_label, state, expected) => {
            localStorage.setItem("sam_builder_skip_activate_confirm", "true");
            versions = new Map(state.versions);
            connectorOnPlatform = true;
            applyFailsWith = 500;
            render(<PlanCardOnBridge manifest={{ name: "my-build", full_validation_passed: true, components: state.components }} />, { wrapper: queryWrapper });
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Replace and save" }).click();
            await waitFor(() => expect(screen.getByText(/the platform rejected the connector config/)).toBeInTheDocument());
            expect(screen.getByText(/couldn.t be completed/)).toBeInTheDocument();
            expect(screen.queryByText(/Save cancelled/)).toBeNull();

            screen.getByRole("button", { name: "Try again" }).click();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await settle();

            expect(onTheCard()).toEqual(expected);
            expect(screen.queryByText(/the platform rejected the connector config/)).toBeNull();
        });

        test("Try again after the decline asks the question again", async () => {
            await declineOnTheCard(STATES[9], true);

            screen.getByRole("button", { name: "Try again" }).click();

            await screen.findByText(DIALOG);
        });
    });

    describe("a decline is the session's own", () => {
        test.each<Kind>(["connector", "skill"])("a %s declined in one session does not hold against another's build, and its banner does not follow", async kind => {
            versions = new Map([[SAVED[kind].filename, 1]]);
            platformStamp.connector = "t-of-someone-else";
            platformStamp.skill = "hash-of-someone-else";
            const manifest = manifestFor(kind);
            const view = await mountAndTakeATurn(manifest);
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await settle();
            expect(screen.queryByText(/Save cancelled/)).not.toBeNull();

            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await settle();
            expect(screen.queryByText(/Save cancelled/)).toBeNull();
            await takeATurn(view, manifest);

            await screen.findByText(DIALOG);
        });

        test("an edit that lands while the question is open is asked about once the question is declined, not taken for part of the decline", async () => {
            await sessionSavedAt("connector", 1);
            platformStamp.connector = "t-of-someone-else";
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = await mountAndTakeATurn(manifest, editTo(SAVED.connector.filename, 2));
            await screen.findByText(DIALOG);
            await takeATurn(view, manifest, editTo(SAVED.connector.filename, 3));

            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(readFiles.length).toBe(2));
            await screen.findByText(DIALOG);

            expect(writesToPlatform()).toBe(0);
        });
    });

    describe("a save in flight at a session switch", () => {
        const savedThroughASwitchEndingWith = async (status?: number, leaving: "switch" | "unmount" = "switch") => {
            await sessionSavedAt("connector", 1);
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            let planStarted = false;
            let finishPlan = () => {};
            duringPlan = () => {
                duringPlan = undefined;
                planStarted = true;
                return new Promise<void>(resolve => {
                    finishPlan = resolve;
                });
            };
            responding = true;
            view.rerender(element(manifest));
            await settle();
            versions = new Map([[SAVED.connector.filename, 2]]);
            view.rerender(element(manifest));
            responding = false;
            view.rerender(element(manifest));
            await waitFor(() => expect(planStarted).toBe(true));
            applyFailsWith = status;

            sessionId = OTHER_SESSION;
            if (leaving === "unmount") view.unmount();
            else view.rerender(element(manifest));
            finishPlan();
            await settle();
        };

        test.each([500, 422])("that is left by unmounting the Builder, then fails with %i, shows and sends nothing", async status => {
            await savedThroughASwitchEndingWith(status, "unmount");

            expect(displayError).not.toHaveBeenCalled();
            expect(handleSubmit).not.toHaveBeenCalled();
        });

        test("that fails leaves a line in the console, so that it is not lost without a trace", async () => {
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

            await savedThroughASwitchEndingWith(500);

            expect(warn).toHaveBeenCalledWith("[builder] a save ended after the Builder left its session", expect.objectContaining({ outcome: "failed", status: 500 }));
            warn.mockRestore();
        });

        test.each([500, 422])("that then fails with %i leaves no banner, Retry, modal or fix for the builder in the session switched to", async status => {
            await savedThroughASwitchEndingWith(status);

            expect(screen.queryByText(/rejected the connector config/)).toBeNull();
            expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
            expect(displayError).not.toHaveBeenCalled();
            expect(handleSubmit).not.toHaveBeenCalled();
        });

        test("that then succeeds says nothing in the session switched to, though the platform holds it", async () => {
            await savedThroughASwitchEndingWith();

            expect(applies()).toBe(1);
            expect(addNotification).not.toHaveBeenCalled();
        });
    });

    describe("the auto-save acts again only for a reason it has not seen", () => {
        test("a skill edit turn is one save pass when the artifact list catches up with the save's own read while it runs", async () => {
            await sessionSavedAt("skill", 1);
            const manifest = manifestFor("skill", { deployed_version: 1 });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            responding = true;
            view.rerender(element(manifest));
            await settle();
            versionsOnceRead = new Map([[SAVED.skill.filename, 2]]);
            view.rerender(element(manifest));
            await settle();
            let release = () => {};
            holdBeforeRead = new Promise<void>(resolve => (release = resolve));
            responding = false;
            view.rerender(element(manifest));
            await waitFor(() => expect(versionReads).toBe(1));
            versions = new Map([[SAVED.skill.filename, 2]]);
            view.rerender(element(manifest));
            await settle();
            holdBeforeRead = undefined;
            versionsOnceRead = undefined;
            release();

            await waitFor(() => expect(uploads()).toBe(1));
            await settle();
            await settle();

            expect(versionReads).toBe(1);
            expect(log.filter(r => r === `GET ${SKILLS_PATH}`).length).toBe(1);
        });

        test("a write the platform refused in a build with a connector and a skill is not tried again until the author says so, though the edits left shrank", async () => {
            await bothSavedAt(1);
            refuseWrites = 1;

            await mountAndTakeATurn(mixedManifest(), () => {
                versions = new Map([
                    [SAVED.connector.filename, 2],
                    [SAVED.skill.filename, 2],
                ]);
            });
            await settle();
            await settle();

            expect(uploads()).toBe(1);
            expect(applies()).toBe(1);
            expect(displayError).toHaveBeenCalledTimes(1);
        });
    });

    describe("what a save is handed to record, and where a session switch leaves the next turn", () => {
        test("both calls get the components the save started with, not a manifest the agent repointed meanwhile", async () => {
            await sessionSavedAt("connector", 1);
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const repointed = manifestFor("connector", { deployed_version: 1, filename: "db_v2.json" });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            responding = true;
            view.rerender(element(manifest));
            await settle();
            versions = new Map([[SAVED.connector.filename, 2]]);
            view.rerender(element(manifest));
            await settle();
            duringAnyGet = async () => {
                await act(async () => {
                    view.rerender(element(repointed));
                });
            };
            responding = false;
            view.rerender(element(manifest));

            await waitFor(() => expect(recordedFiles.length).toBeGreaterThan(0));

            expect(readFiles[0]).toEqual([SAVED.connector.filename]);
            expect(recordedFiles[0]).toEqual([SAVED.connector.filename]);
        });

        test("a question withdrawn by a switch is not a decline in the session arrived at", async () => {
            await sessionSavedAt("skill", 1);
            platformStamp.skill = "hash-of-someone-else";
            const manifest = manifestFor("skill", { deployed_version: 1 });
            const view = await mountAndTakeATurn(manifest, editTo(SAVED.skill.filename, 2));
            await screen.findByText(DIALOG);
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await settle();
            rememberOwnWrite("skill");

            await takeATurn(view, manifest);

            await waitFor(() => expect(uploads()).toBe(1));
        });

        test("a switch that lands while the arriving session's turn has begun still saves at that turn's end", async () => {
            await sessionSavedAt("connector", 1);
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = await mountAndTakeATurn(manifest);
            rememberOwnWrite("connector");
            sessionId = OTHER_SESSION;
            responding = true;
            view.rerender(element(manifest));
            await settle();
            versions = new Map([[SAVED.connector.filename, 2]]);
            view.rerender(element(manifest));
            await settle();

            responding = false;
            view.rerender(element(manifest));

            await waitFor(() => expect(applies()).toBe(1));
        });
    });

    describe("the hook and the model make the same decisions", () => {
        const E = {
            ts: "turnStart",
            te: "turnEnd",
            lu: "listCatchUp",
            cycle: "cycle",
            reload: "reload",
            sw: "switch",
            retry: "retry",
            yes: "yes",
            no: "no",
            fail: "breakWrite",
            editC: "editC",
            editS: "editS",
            editCS: "editCS",
            otherC: "otherC",
            otherS: "otherS",
        } as const satisfies Record<string, ModelEvent>;
        const { ts, te, lu, cycle, reload, sw, retry, yes, no, fail, editC, editS, editCS, otherC, otherS } = E;

        const editOf = (build: Build): ModelEvent => ({ connector: editC, skill: editS, both: editCS })[build];
        const otherOf = (build: Build): ModelEvent => (build === "connector" ? otherC : otherS);
        type Trail = (build: Build) => ModelEvent[];

        const TRAILS: Record<string, Trail> = {
            "a turn that edits nothing": () => [ts, te],
            "an edit, saved at the end of its turn": b => [ts, editOf(b), te],
            "an edit before the turn": b => [editOf(b), ts, te],
            "an edit in a turn, then the list catching up": b => [ts, editOf(b), te, lu],
            "an edit, then more turns": b => [ts, editOf(b), te, lu, ts, te, ts, te],
            "two edits, one turn each": b => [ts, editOf(b), te, lu, ts, editOf(b), te, lu],
            "an edit while the agent is still working": b => [ts, editOf(b), editOf(b), te, lu],
            "somebody else changes it, then an edit": b => [otherOf(b), ts, editOf(b), te, lu],
            "somebody else changes it, an edit, the question answered yes": b => [otherOf(b), ts, editOf(b), te, yes, lu],
            "somebody else changes it, an edit, the question declined, later turns": b => [otherOf(b), ts, editOf(b), te, no, lu, ts, te, ts, te],
            "declined, then the list catching up with the question open": b => [otherOf(b), ts, editOf(b), te, lu, no, ts, te],
            "declined, then the list catching up": b => [otherOf(b), ts, editOf(b), te, no, lu, ts, te],
            "declined, then a new edit": b => [otherOf(b), ts, editOf(b), te, no, lu, ts, editOf(b), te, lu],
            "declined, then Try again": b => [otherOf(b), ts, editOf(b), te, no, lu, retry],
            "declined, then Try again, then yes": b => [otherOf(b), ts, editOf(b), te, no, lu, retry, yes],
            "declined, a status cycle, later turns": b => [otherOf(b), ts, editOf(b), te, lu, no, cycle, ts, te],
            "a first question at turn start, an edit during the turn, declined, then the list catching up": b => [ts, editOf(b), te, no, lu],
            "declined under list lag, a status cycle, then the list catching up": b => [otherOf(b), ts, editOf(b), te, no, cycle, lu, ts, te],
            "a refused write, then Try again": b => [fail, ts, editOf(b), te, lu, retry],
            "a refused write, then the list catching up, then more turns": b => [fail, ts, editOf(b), te, lu, ts, te, ts, te],
            "a refused write, an edit, a turn": b => [fail, ts, editOf(b), te, lu, ts, editOf(b), te, lu],
            "a refused write, then a status cycle": b => [fail, ts, editOf(b), te, lu, cycle],
            "a refused write, a switch, then a turn": b => [fail, ts, editOf(b), te, lu, sw, ts, te],
            "a refused write, a switch, then Try again": b => [fail, ts, editOf(b), te, lu, sw, retry],
            "a reload, then an edit": b => [reload, ts, editOf(b), te, lu],
            "an edit, a reload, a turn": b => [ts, editOf(b), te, lu, reload, ts, te],
            "an edit, the question open, a reload": b => [otherOf(b), ts, editOf(b), te, reload, ts, te],
            "a switch, then an edit in the session arrived at": b => [sw, ts, editOf(b), te, lu],
            "a switch while the question is open, then a turn": b => [otherOf(b), ts, editOf(b), te, sw, ts, te],
            "a switch while the question is open, then Try again": b => [otherOf(b), ts, editOf(b), te, sw, retry],
            "declined, then a switch and a turn in the session arrived at": b => [otherOf(b), ts, editOf(b), te, no, lu, sw, ts, te],
            "declined, then a switch and an edit in the session arrived at": b => [otherOf(b), ts, editOf(b), te, no, lu, sw, ts, editOf(b), te, lu],
            "a status cycle after a save": b => [ts, editOf(b), te, lu, cycle, ts, te],
            "somebody else changes it between turns": b => [ts, te, otherOf(b), ts, te, ts, editOf(b), te, lu],
            "Try again with nothing to retry": b => [ts, te, retry, ts, editOf(b), te, lu, retry],
        };

        const MIXED: Record<string, ModelEvent[]> = {
            "both edited in one turn, the skill replaced, declined, then the list catching up": [otherS, ts, editCS, te, no, lu, ts, te],
            "both edited in one turn, the skill replaced, the list catching up before the answer": [otherS, ts, editCS, te, lu, no, ts, te],
            "both edited in one turn, the skill replaced, answered yes": [otherS, ts, editCS, te, yes, lu],
            "both edited in one turn, a refused write, then the list catching up": [fail, ts, editCS, te, lu, ts, te],
            "both edited in one turn, then more turns": [ts, editCS, te, lu, ts, te, ts, te],
            "only the connector edited": [ts, editC, te, lu],
            "only the skill edited": [ts, editS, te, lu],
            "the connector edited while the skill's question is open": [otherS, ts, editS, te, editC, lu, no, ts, te],
            "somebody else changes the connector, the skill edited": [otherC, ts, editS, te, lu],
            "somebody else changes both, both edited, answered yes": [otherC, otherS, ts, editCS, te, yes, lu],
            "both edited, a reload, a turn": [ts, editCS, te, lu, reload, ts, te],
            "both edited, declined, a switch, a turn": [otherS, ts, editCS, te, no, lu, sw, ts, te],
            "both saved by this mount, somebody else changes the connector, then a turn": [ts, editCS, te, lu, otherC, ts, te],
            "declined, Try again, yes, the skill replaced again, then a turn": [otherS, ts, editCS, te, no, lu, retry, yes, otherS, ts, te],
        };

        const ALL = Object.keys(TRAILS);
        const STARTS: Array<{ label: string; asks: boolean; recordKept: boolean; owned: boolean; trails: string[] }> = [
            { label: "asking, the record kept, written by the session", asks: true, recordKept: true, owned: true, trails: ALL },
            {
                label: "asking, no record, not written by the session",
                asks: true,
                recordKept: false,
                owned: false,
                trails: [
                    "a turn that edits nothing",
                    "an edit, saved at the end of its turn",
                    "an edit, then more turns",
                    "declined, then the list catching up",
                    "a first question at turn start, an edit during the turn, declined, then the list catching up",
                    "declined, then Try again, then yes",
                    "a refused write, then Try again",
                    "a reload, then an edit",
                    "declined, then a switch and a turn in the session arrived at",
                    "somebody else changes it between turns",
                ],
            },
            {
                label: "the opt-out set",
                asks: false,
                recordKept: false,
                owned: false,
                trails: ["an edit, saved at the end of its turn", "somebody else changes it, then an edit", "a refused write, then the list catching up, then more turns", "a switch, then an edit in the session arrived at"],
            },
        ];
        const BUILDS: Build[] = ["connector", "skill", "both"];
        const MIXED_STARTS = [
            { label: "asking, the record kept, written by the session", asks: true, recordKept: true, owned: true },
            { label: "asking, no record, not written by the session", asks: true, recordKept: false, owned: false },
            { label: "the opt-out set", asks: false, recordKept: true, owned: false },
        ];
        const cases = [
            ...STARTS.flatMap(({ label, asks, recordKept, owned, trails }) => BUILDS.flatMap(build => trails.map(trail => [build, label, trail, TRAILS[trail](build), { asks, recordKept, owned }] as const))),
            ...MIXED_STARTS.flatMap(({ label, ...start }) => Object.entries(MIXED).map(([trail, events]) => ["both", label, trail, events, start] as const)),
        ];

        test.each(cases)("a %s build, %s: %s", async (build, _start, _trail, events, { asks, recordKept, owned }) => {
            const kinds: Kind[] = build === "both" ? ["skill", "connector"] : [build];
            const files = kinds.map(kind => SAVED[kind].filename);
            let switches = 0;
            let others = 0;
            bridged = true;
            contentCompare = true;
            const truth = new Map(files.map(file => [file, 1]));
            versions = new Map(truth);
            versionsOnceRead = new Map(truth);
            if (kinds.includes("skill")) await platformHoldsSkillAt(1);
            if (kinds.includes("connector")) platformConnectorContent = connectorAt(1);
            for (const file of files) recordedByFile.set(file, recordKept ? 1 : undefined);
            if (owned) {
                const records = kinds.map(kind => ({ id: SAVED[kind].id, kind, stamp: stampOnWire(kind, platformStamp[kind]) }));
                localStorage.setItem(OWNERSHIP_KEY, JSON.stringify({ [SESSION]: records, [OTHER_SESSION]: records }));
            }
            if (!asks) localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            successfulWrites = 0;
            const currentManifest = (status = "validated"): BuildManifest => ({
                name: "my-build",
                full_validation_passed: true,
                components: kinds.map(kind => ({ filename: SAVED[kind].filename, type: kind, name: SAVED[kind].name, status, origin: "session", deployed_version: recordedByFile.get(SAVED[kind].filename) })),
            });
            let view = render(element(currentManifest()), { wrapper: queryWrapper });
            await settle();
            let model: Model = start(build, asks, recordKept, owned);

            const edit =
                (...edited: Kind[]) =>
                async () => {
                    for (const kind of edited) truth.set(SAVED[kind].filename, (truth.get(SAVED[kind].filename) ?? 0) + 1);
                    versionsOnceRead = new Map(truth);
                    if (!responding) versions = new Map(truth);
                    view.rerender(element(currentManifest()));
                };
            const replaced = (kind: Kind) => async () => {
                others++;
                platformStamp[kind] = `someone-else-${others}`;
                if (kind === "connector") platformConnectorContent = `someone-else-${others}`;
                view.rerender(element(currentManifest()));
            };

            const drive: Record<ModelEvent, () => Promise<void>> = {
                turnStart: async () => {
                    responding = true;
                    view.rerender(element(currentManifest()));
                },
                turnEnd: async () => {
                    responding = false;
                    view.rerender(element(currentManifest()));
                },
                editC: edit("connector"),
                editS: edit("skill"),
                editCS: edit("connector", "skill"),
                listCatchUp: async () => {
                    versions = new Map(truth);
                    view.rerender(element(currentManifest()));
                },
                otherC: replaced("connector"),
                otherS: replaced("skill"),
                cycle: async () => {
                    view.rerender(element(currentManifest("validating")));
                    await settle();
                    view.rerender(element(currentManifest()));
                },
                reload: async () => {
                    view.unmount();
                    responding = false;
                    versions = new Map(truth);
                    view = render(element(currentManifest()), { wrapper: queryWrapper });
                },
                switch: async () => {
                    sessionId = `session-switched-to-${++switches}`;
                    view.rerender(element(currentManifest()));
                },
                retry: async () => bridge?.retry(),
                yes: async () => screen.getByRole("button", { name: "Replace and save" }).click(),
                no: async () => screen.getByRole("button", { name: "Cancel" }).click(),
                breakWrite: async () => {
                    refuseWrites = 1;
                },
            };

            const happened: ModelEvent[] = [];
            for (const event of events) {
                const next = step(model, event);
                if (!next) continue;
                happened.push(event);
                await drive[event]();
                await settle();
                model = next;
                const seen = { writes: successfulWrites, asking: screen.queryByText(DIALOG) !== null, retryOffered: bridge?.failure.kind === "retryable" || bridge?.failure.kind === "declined" };
                expect(seen, `after ${happened.join(" > ")}`).toEqual(observe(model));
            }
        });
    });

    describe("an artifact list that trails the save's own read", () => {
        const savedWhileTheListTrails = async (onTheEdit: () => void) => {
            await sessionSavedAt("skill", 1);
            onTheEdit();
            const manifest = manifestFor("skill", { deployed_version: 1 });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            responding = true;
            view.rerender(element(manifest));
            await settle();
            versionsOnceRead = new Map([[SAVED.skill.filename, 2]]);
            view.rerender(element(manifest));
            await settle();
            responding = false;
            view.rerender(element(manifest));
            const catchUp = async () => {
                versions = new Map([[SAVED.skill.filename, 2]]);
                versionsOnceRead = undefined;
                view.rerender(element(manifest));
                await settle();
                await settle();
            };
            return { view, manifest, catchUp };
        };

        test.each(["after", "before"] as const)("a declined question is not asked again when the list catches up %s the answer", async when => {
            const { view, manifest, catchUp } = await savedWhileTheListTrails(() => {
                platformStamp.skill = "hash-of-someone-else";
            });
            await screen.findByText(DIALOG);
            if (when === "before") await catchUp();

            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            if (when === "after") await catchUp();
            await settle();
            await takeATurn(view, manifest);

            expect(screen.queryByText(DIALOG)).toBeNull();
            expect(versionReads).toBe(1);
        });

        test("a write the platform refused is not tried again when the list catches up", async () => {
            const { catchUp } = await savedWhileTheListTrails(() => {
                refuseWrites = 1;
            });
            await waitFor(() => expect(uploads()).toBe(1));

            await catchUp();

            expect(uploads()).toBe(1);
            expect(versionReads).toBe(1);
        });
    });

    describe("with server-side apply off, a connector the per-kind path created", () => {
        const connectorPosts = () => log.filter(r => r === `POST ${CONNECTORS_PATH}`).length;
        const ownershipRecord = () => (JSON.parse(localStorage.getItem(OWNERSHIP_KEY) ?? "{}") as Record<string, Array<{ id: string; kind: string; stamp?: string }>>)[SESSION] ?? [];
        const buildWithoutRecord = (withSkill: boolean): BuildManifest => {
            const connector = { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session" };
            const skill = { filename: SAVED.skill.filename, type: "skill", name: SAVED.skill.name, status: "validated", origin: "session", deployed_version: 1 };
            return { name: "my-build", full_validation_passed: true, components: withSkill ? [connector, skill] : [connector] } as BuildManifest;
        };

        test.each([
            ["a connector alone", false],
            ["a connector and a skill", true],
        ])("%s is remembered, and is not created again after a reload that lost the manifest's record of it", async (_label, withSkill) => {
            serverApplyEnabled = false;
            if (withSkill) await sessionSavedAt("skill", 1);
            versions = new Map([
                [SAVED.connector.filename, 1],
                [SAVED.skill.filename, 1],
            ]);
            const first = await mountAndTakeATurn(buildWithoutRecord(withSkill));
            await waitFor(() => expect(connectorPosts()).toBe(1));
            await settle();
            expect(ownershipRecord()).toContainEqual(expect.objectContaining({ id: SAVED.connector.id, kind: "connector", stamp: instantOf("t-created-per-kind") }));

            first.unmount();
            const reloaded = render(element(buildWithoutRecord(withSkill)), { wrapper: queryWrapper });
            await settle();
            await takeATurn(reloaded, buildWithoutRecord(withSkill));
            await takeATurn(reloaded, buildWithoutRecord(withSkill));
            await settle();

            expect(connectorPosts()).toBe(1);
            expect(displayError).not.toHaveBeenCalled();
            expect(handleSubmit).not.toHaveBeenCalled();
            expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("Updated in place"), expect.anything());
        });

        test("a connector it created is recorded as saved at the version the save read", async () => {
            serverApplyEnabled = false;
            versions = new Map([[SAVED.connector.filename, 1]]);
            await mountAndTakeATurn(buildWithoutRecord(false));
            await waitFor(() => expect(manifestRecordNow).toBe(1));
        });
    });

    describe("what the per-kind skip is keyed on", () => {
        test("a connector the session wrote is not created again when the manifest names it differently from the platform only in case or punctuation", async () => {
            serverApplyEnabled = false;
            connectorOnPlatform = true;
            await bothSavedAt(1);
            const renamed: BuildManifest = { ...mixedManifest(), components: mixedManifest().components?.map(c => (c.type === "connector" ? { ...c, name: "DB", deployed_version: undefined } : c)) };

            const view = await mountAndTakeATurn(renamed);
            await takeATurn(view, renamed);
            await settle();

            expect(log.filter(r => r === `POST ${CONNECTORS_PATH}`).length).toBe(0);
        });

        test("an agent that is named like a connector the session wrote is still saved", async () => {
            serverApplyEnabled = false;
            connectorOnPlatform = true;
            rememberOwnWrite("connector");
            versions = new Map([
                [SAVED.connector.filename, 1],
                ["agent.yaml", 1],
            ]);
            const manifest: BuildManifest = {
                name: "my-build",
                full_validation_passed: true,
                components: [
                    { filename: "agent.yaml", type: "agent", name: "DB", status: "validated", origin: "session" },
                    { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session", deployed_version: 1 },
                ],
            };
            planApproved = true;

            render(element(manifest), { wrapper: queryWrapper });
            await settle();

            expect(log.filter(r => r.startsWith("POST ") && r.includes("/agents")).length).toBeGreaterThan(0);
        });
    });

    describe("a save that ends in a session the Builder has left", () => {
        test("a Build & Activate whose skill question is withdrawn by the switch deploys nothing and says nothing in the session switched to", async () => {
            serverApplyEnabled = false;
            await sessionSavedAt("skill", 1);
            platformStamp.skill = "hash-of-someone-else";
            versions = new Map([
                [SAVED.skill.filename, 2],
                ["agent.yaml", 1],
            ]);
            const manifest: BuildManifest = {
                name: "my-build",
                full_validation_passed: true,
                components: [
                    { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session" },
                    { filename: SAVED.skill.filename, type: "skill", name: SAVED.skill.name, status: "validated", origin: "session", deployed_version: 1 },
                ],
            };
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            (await screen.findByRole("button", { name: "Build & Activate" })).click();
            await screen.findByText(DIALOG);
            const before = log.length;

            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await settle();

            expect(log.slice(before)).toEqual([]);
            expect(displayError).not.toHaveBeenCalled();
            expect(addNotification).not.toHaveBeenCalled();
        });

        const secondSaveFailingAfterASwitch = async (kinds: "connector" | "both", refuse: () => void) => {
            bridged = true;
            const edited = (version: number) => () => {
                versions = new Map(
                    kinds === "both"
                        ? [
                              [SAVED.connector.filename, version],
                              [SAVED.skill.filename, version],
                          ]
                        : [[SAVED.connector.filename, version]]
                );
            };
            if (kinds === "both") await bothSavedAt(1);
            else await sessionSavedAt("connector", 1);
            const manifest = kinds === "both" ? mixedManifest() : manifestFor("connector", { deployed_version: 1 });
            const view = await mountAndTakeATurn(manifest, edited(2));
            await waitFor(() => expect(applies()).toBe(1));
            let release = () => {};
            holdBeforeRead = new Promise<void>(resolve => {
                release = resolve;
            });
            await takeATurn(view, manifest, edited(3));
            await waitFor(() => expect(versionReads).toBe(2));
            refuse();
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await settle();
            holdBeforeRead = undefined;
            release();
            await settle();
        };

        test("a save that fails leaves Try again in the session switched to to ask before it saves, as for any session that has saved nothing in this mount", async () => {
            await secondSaveFailingAfterASwitch("connector", () => {
                applyFailsWith = 500;
            });
            expect(applies()).toBe(2);

            bridge?.retry();
            await settle();

            expect(applies()).toBe(2);
            expect(screen.queryByText(DIALOG)).not.toBeNull();
        });

        test("a save that partly fails leaves Try again in the session switched to to ask before it saves, as for any session that has saved nothing in this mount", async () => {
            await secondSaveFailingAfterASwitch("both", () => {
                refuseWrites = 1;
            });
            const attempts = () => log.filter(r => r.endsWith("/bundle")).length;
            const before = { writes: writesToPlatform(), attempts: attempts() };

            bridge?.retry();
            await settle();

            expect(writesToPlatform()).toBe(before.writes);
            expect(attempts()).toBe(before.attempts);
            expect(screen.queryByText(DIALOG)).not.toBeNull();
        });

        test("a skill save that is partly refused after a switch leaves Try again in the session switched to to deploy, not to save: nothing is asked or written there, since the credential answer is false for a skill", async () => {
            bridged = true;
            await sessionSavedAt("skill", 1);
            const manifest = manifestFor("skill", { deployed_version: 1 });
            const edited = (version: number) => () => {
                versions = new Map([[SAVED.skill.filename, version]]);
            };
            const view = await mountAndTakeATurn(manifest, edited(2));
            await waitFor(() => expect(uploads()).toBe(1));
            let release = () => {};
            holdBeforeRead = new Promise<void>(resolve => {
                release = resolve;
            });
            await takeATurn(view, manifest, edited(3));
            await waitFor(() => expect(versionReads).toBe(2));
            refuseWrites = 1;
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await settle();
            holdBeforeRead = undefined;
            release();
            await settle();
            const before = { writes: writesToPlatform(), attempts: log.filter(r => r.endsWith("/bundle")).length };
            expect(before.attempts).toBe(2);

            bridge?.retry();
            await settle();

            expect(log.filter(r => r.endsWith("/bundle")).length).toBe(before.attempts);
            expect(writesToPlatform()).toBe(before.writes);
            expect(screen.queryByText(DIALOG)).toBeNull();
        });

        test("a skill save whose version lookup fails after a switch leaves Try again in the session switched to to deploy, not to save: nothing is asked or written there, since the credential answer is false for a skill", async () => {
            bridged = true;
            await sessionSavedAt("skill", 1);
            const manifest = manifestFor("skill", { deployed_version: 1 });
            const edited = (version: number) => () => {
                versions = new Map([[SAVED.skill.filename, version]]);
            };
            const view = await mountAndTakeATurn(manifest, edited(2));
            await waitFor(() => expect(uploads()).toBe(1));
            let release = () => {};
            holdBeforeRead = new Promise<void>(resolve => {
                release = resolve;
            });
            await takeATurn(view, manifest, edited(3));
            await waitFor(() => expect(versionReads).toBe(2));
            failedLookups = new Set([SAVED.skill.filename]);
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await settle();
            holdBeforeRead = undefined;
            release();
            await settle();
            const before = { writes: writesToPlatform(), attempts: log.filter(r => r.endsWith("/bundle")).length };
            expect(before.attempts).toBe(1);
            failedLookups = new Set();

            bridge?.retry();
            await settle();

            expect(log.filter(r => r.endsWith("/bundle")).length).toBe(before.attempts);
            expect(writesToPlatform()).toBe(before.writes);
            expect(screen.queryByText(DIALOG)).toBeNull();
        });

        test("a skill save whose pinned config read fails after a switch leaves Try again in the session switched to to deploy, not to save: nothing is asked or written there, since the credential answer is false for a skill", async () => {
            bridged = true;
            await sessionSavedAt("skill", 1);
            const manifest = manifestFor("skill", { deployed_version: 1 });
            const edited = (version: number) => () => {
                versions = new Map([[SAVED.skill.filename, version]]);
            };
            const view = await mountAndTakeATurn(manifest, edited(2));
            await waitFor(() => expect(uploads()).toBe(1));
            let release = () => {};
            holdBeforeRead = new Promise<void>(resolve => {
                release = resolve;
            });
            await takeATurn(view, manifest, edited(3));
            await waitFor(() => expect(versionReads).toBe(2));
            pinnedReadFailsWith = 503;
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await settle();
            holdBeforeRead = undefined;
            release();
            await settle();
            const before = { writes: writesToPlatform(), attempts: log.filter(r => r.endsWith("/bundle")).length };
            expect(before.attempts).toBe(1);
            pinnedReadFailsWith = undefined;

            bridge?.retry();
            await settle();

            expect(log.filter(r => r.endsWith("/bundle")).length).toBe(before.attempts);
            expect(writesToPlatform()).toBe(before.writes);
            expect(screen.queryByText(DIALOG)).toBeNull();
        });

        test("a build that fails outright after a switch leaves Try again in a session that has saved this mount to deploy, not to save", async () => {
            bridged = true;
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            await bothSavedAt(1);
            const manifest = mixedManifest();
            const edited = (version: number) => () => {
                versions = new Map([
                    [SAVED.connector.filename, version],
                    [SAVED.skill.filename, version],
                ]);
            };
            sessionId = OTHER_SESSION;
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            await takeATurn(view, manifest, edited(2));
            await waitFor(() => expect(applies()).toBe(1));
            sessionId = SESSION;
            view.rerender(element(manifest));
            await settle();
            let release = () => {};
            holdBeforeRead = new Promise<void>(resolve => {
                release = resolve;
            });
            await takeATurn(view, manifest);
            await waitFor(() => expect(versionReads).toBe(2));
            applyFailsWith = 500;
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await settle();
            holdBeforeRead = undefined;
            release();
            await settle();
            const skillLookups = () => log.filter(request => request === `GET ${SKILLS_PATH}`).length;
            const before = { lookups: skillLookups(), applies: applies() };

            bridge?.retry();
            await settle();

            expect(skillLookups()).toBe(before.lookups);
            expect(applies()).toBe(before.applies);
            expect(screen.queryByText(DIALOG)).toBeNull();
        });
    });

    describe("what the failure surface says after a decline, a failure and a switch", () => {
        test("a decline after a fix was sent to the builder says it was declined, and offers Try again", async () => {
            bridged = true;
            await sessionSavedAt("connector", 1);
            platformStamp.connector = "t-of-someone-else";
            applyFailsWith = 422;
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = await mountAndTakeATurn(manifest, editTo(SAVED.connector.filename, 2));
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Replace and save" }).click();
            await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(1));
            await settle();
            expect(bridge?.failure.kind).toBe("fixing");

            await takeATurn(view, manifest, editTo(SAVED.connector.filename, 3));
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await settle();

            expect(bridge?.failure).toMatchObject({ kind: "declined", message: expect.stringContaining("Save cancelled") });
        });

        test("the budget of fixes sent in one session does not carry into the session switched to", async () => {
            await sessionSavedAt("connector", 1);
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            for (const version of [2, 3, 4]) {
                applyFailsWith = 422;
                await takeATurn(view, manifest, editTo(SAVED.connector.filename, version));
                await settle();
            }
            expect(handleSubmit).toHaveBeenCalledTimes(2);

            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await settle();
            applyFailsWith = 422;
            await takeATurn(view, manifest, editTo(SAVED.connector.filename, 5));
            await settle();

            expect(handleSubmit).toHaveBeenCalledTimes(3);
        });
    });

    describe("a decline in a build with a connector and a skill", () => {
        test("holds against later turns when the connector is left out as proven unchanged, and not only when both were sent", async () => {
            await bothSavedAt(1);
            platformStamp.skill = "hash-of-someone-else";
            const manifest = mixedManifest();
            const view = await mountAndTakeATurn(manifest, () => {
                versions = new Map([
                    [SAVED.connector.filename, 1],
                    [SAVED.skill.filename, 2],
                ]);
            });
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());

            await takeATurn(view, manifest);
            await takeATurn(view, manifest);

            expect(screen.queryByText(DIALOG)).toBeNull();
            expect(uploads()).toBe(0);
            expect(applies()).toBe(0);
        });
    });

    describe("a Build & Activate that finishes after the Builder has moved to another session", () => {
        test("deploys nothing, and says nothing, in the session switched to", async () => {
            await sessionSavedAt("connector", 1);
            versions = new Map([
                [SAVED.connector.filename, 2],
                ["agent.yaml", 1],
            ]);
            const manifest: BuildManifest = {
                name: "my-build",
                full_validation_passed: true,
                components: [
                    { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session" },
                    { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session", deployed_version: 1 },
                ],
            };
            let planStarted = false;
            let finishPlan = () => {};
            duringPlan = () => {
                duringPlan = undefined;
                planStarted = true;
                return new Promise<void>(resolve => {
                    finishPlan = resolve;
                });
            };
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            (await screen.findByRole("button", { name: "Build & Activate" })).click();
            await waitFor(() => expect(planStarted).toBe(true));
            const before = log.length;

            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            finishPlan();
            await settle();

            expect(applies()).toBe(1);
            expect(log.slice(before).filter(request => request.includes("/agents"))).toEqual([]);
            expect(displayError).not.toHaveBeenCalled();
            expect(addNotification).not.toHaveBeenCalled();
        });
    });

    describe("a build with a connector and a skill, edited together while the artifact list trails", () => {
        const savedWhileTheListTrails = async (when: "during" | "after", beforeTheTurn: () => void = () => {}) => {
            await bothSavedAt(1);
            beforeTheTurn();
            const manifest = mixedManifest();
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            responding = true;
            view.rerender(element(manifest));
            await settle();
            versionsOnceRead = new Map([
                [SAVED.connector.filename, 2],
                [SAVED.skill.filename, 2],
            ]);
            let release = () => {};
            if (when === "during")
                holdBeforeRead = new Promise<void>(resolve => {
                    release = resolve;
                });
            responding = false;
            view.rerender(element(manifest));
            const catchUp = async () => {
                versions = new Map([
                    [SAVED.connector.filename, 2],
                    [SAVED.skill.filename, 2],
                ]);
                versionsOnceRead = undefined;
                view.rerender(element(manifest));
                await settle();
            };
            if (when === "during") {
                await waitFor(() => expect(versionReads).toBe(1));
                await catchUp();
                holdBeforeRead = undefined;
                release();
            }
            return { view, manifest, catchUp };
        };
        const sentByTheFirstSave = () => buildRequests.find(request => request.dryRun)?.names ?? [];

        test.each(["during", "after"] as const)("the connector edit is sent with the skill's, and a declined question is neither asked again nor masks it, when the list catches up %s the read", async when => {
            const { view, manifest, catchUp } = await savedWhileTheListTrails(when, () => {
                platformStamp.skill = "hash-of-someone-else";
            });
            await screen.findByText(DIALOG);
            expect(sentByTheFirstSave()).toEqual([SAVED.connector.name]);

            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            if (when === "after") await catchUp();
            await settle();
            await takeATurn(view, manifest);
            await takeATurn(view, manifest);

            expect(screen.queryByText(DIALOG)).toBeNull();
            expect(versionReads).toBe(1);
            expect(applies()).toBe(0);
            expect(uploads()).toBe(0);
        });

        test("a skill write the platform refused is not tried again when the list catches up, and the connector edit landed with it", async () => {
            const { catchUp } = await savedWhileTheListTrails("after", () => {
                refuseWrites = 1;
            });
            await waitFor(() => expect(uploads()).toBe(1));
            await waitFor(() => expect(applies()).toBe(1));

            await catchUp();

            expect(uploads()).toBe(1);
            expect(applies()).toBe(1);
            expect(versionReads).toBe(1);
        });

        test("the question, answered yes, writes both", async () => {
            const { catchUp } = await savedWhileTheListTrails("after", () => {
                platformStamp.skill = "hash-of-someone-else";
            });
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Replace and save" }).click();
            await waitFor(() => expect(applies()).toBe(1));
            await waitFor(() => expect(uploads()).toBe(1));

            await catchUp();
            await settle();

            expect(screen.queryByText(DIALOG)).toBeNull();
            expect(applies()).toBe(1);
            expect(uploads()).toBe(1);
        });
    });

    describe("a save whose version lookup fails", () => {
        const NOT_READ = /Could not read the versions of/;
        const savedNotes = () => addNotification.mock.calls.filter(([message]) => String(message).includes("saved successfully"));

        async function turnEndsWithFailedLookup(manifest: BuildManifest, unreadable: string[]) {
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            responding = true;
            view.rerender(element(manifest));
            await settle();
            failedLookups = new Set(unreadable);
            responding = false;
            view.rerender(element(manifest));
            await settle();
            return view;
        }

        test("a skill is neither written nor read at a version the list trails: the save ends with a banner, and Try again saves the edit once the lookup works", async () => {
            await sessionSavedAt("skill", 1);
            const manifest = manifestFor("skill", { deployed_version: 1 });

            await turnEndsWithFailedLookup(manifest, [SAVED.skill.filename]);

            await screen.findByText(NOT_READ);
            expect(writesToPlatform()).toBe(0);
            expect(savedNotes()).toEqual([]);
            expect(handleSubmit).not.toHaveBeenCalled();

            failedLookups = new Set();
            versions = new Map([[SAVED.skill.filename, 2]]);
            screen.getByRole("button", { name: "Retry" }).click();
            await waitFor(() => expect(uploads()).toBe(1));
            await waitFor(() => expect(screen.queryByText(NOT_READ)).toBeNull());
            expect(displayError).toHaveBeenCalledTimes(1);
            expect(handleSubmit).not.toHaveBeenCalled();
            expect(configFetches.filter(([filename]) => filename === SAVED.skill.filename).map(([, options]) => options?.version)).toEqual([2]);
        });

        test("a connector edited in the turn is not left out as unchanged against a list that trails it, and is not reported as saved", async () => {
            await bothSavedAt(1);

            await turnEndsWithFailedLookup(mixedManifest(), [SAVED.connector.filename]);

            await screen.findByText(NOT_READ);
            expect(writesToPlatform()).toBe(0);
            expect(savedNotes()).toEqual([]);
        });

        test("one lookup failing sends nothing of the build: not the connector whose version was read either", async () => {
            await bothSavedAt(1);

            await turnEndsWithFailedLookup(mixedManifest(), [SAVED.skill.filename]);

            await screen.findByText(NOT_READ);
            expect(writesToPlatform()).toBe(0);
            expect(screen.getByText(/skill\.yaml/)).toBeTruthy();
            expect(screen.queryByText(/db\.json\. Try again/)).toBeNull();
        });

        test("a recorder that throws instead of naming what it could not read ends the save the same way, and the next save is not told it is busy", async () => {
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
            await sessionSavedAt("skill", 1);
            const manifest = manifestFor("skill", { deployed_version: 1 });
            versionsReadThrows = true;

            const view = await mountAndTakeATurn(manifest);

            await screen.findByText(NOT_READ);
            expect(uploads()).toBe(0);
            expect(warn).toHaveBeenCalledWith("[builder] could not read the versions a save is about to send", { error: "versions read threw" });

            versionsReadThrows = false;
            versions = new Map([[SAVED.skill.filename, 2]]);
            await takeATurn(view, manifest);
            await waitFor(() => expect(uploads()).toBe(1));
            expect(screen.queryByText(NOT_READ)).toBeNull();
        });
    });

    describe("a config file the store holds no version of", () => {
        const NOT_READ = /Could not read the versions of/;
        const sentToTheBuilder = () => handleSubmit.mock.calls.map(call => String(call[2]));
        const agentAndConnector = (): BuildManifest => ({
            name: "my-build",
            full_validation_passed: true,
            components: [
                { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session" },
                { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session", deployed_version: 1 },
            ],
        });

        test.each<Kind>(["connector", "skill"])("handleSaveAll run directly (past the validation gate): a %s file that is not there is not an outage: the builder is told to write it, as when the version was never looked up", async kind => {
            bridged = true;
            missingConfigs = new Set([SAVED[kind].filename]);
            const manifest = manifestFor(kind, { deployed_version: 1 });
            render(element(manifest), { wrapper: queryWrapper });
            await settle();
            act(() => {
                void bridge?.run();
            });
            await settle();

            await waitFor(() => expect(sentToTheBuilder().some(message => message.includes(`Could not resolve config for "${SAVED[kind].name}" (${SAVED[kind].filename})`))).toBe(true));
            expect(screen.queryByText(NOT_READ)).toBeNull();
            expect(writesToPlatform()).toBe(0);
        });

        test("handleSaveAll run directly (past the validation gate): an agent build whose connector file is not there tells the builder, and no save is held back as an outage", async () => {
            versions = new Map([["agent.yaml", 1]]);
            missingConfigs = new Set([SAVED.connector.filename]);
            bridged = true;

            render(element(agentAndConnector()), { wrapper: queryWrapper });
            await settle();
            act(() => {
                void bridge?.run();
            });
            await settle();

            await waitFor(() => expect(sentToTheBuilder().some(message => message.includes(`Could not resolve config for "db" (db.json)`))).toBe(true));
            expect(screen.queryByText(NOT_READ)).toBeNull();
            expect(displayError).not.toHaveBeenCalled();
        });

        test("handleSaveAll run directly (past the validation gate): a file with no versions is read unpinned, and still read", async () => {
            versions = new Map([["agent.yaml", 1]]);
            bridged = true;

            render(element(agentAndConnector()), { wrapper: queryWrapper });
            await settle();
            act(() => {
                void bridge?.run();
            });
            await settle();

            expect(configFetches.filter(([filename]) => filename === SAVED.connector.filename).every(([, options]) => options?.version === undefined)).toBe(true);
            expect(configFetches.filter(([filename]) => filename === SAVED.connector.filename).length).toBeGreaterThan(0);
        });
    });

    describe("an agent build whose connector version cannot be read", () => {
        test.each([
            ["with server-side apply", true],
            ["with server-side apply off", false],
        ])("%s ends with the banner and nothing is saved or deployed", async (_label, apply) => {
            serverApplyEnabled = apply;
            connectorOnPlatform = true;
            versions = new Map([
                [SAVED.connector.filename, 1],
                ["agent.yaml", 1],
            ]);
            failedLookups = new Set([SAVED.connector.filename]);
            const manifest: BuildManifest = {
                name: "my-build",
                full_validation_passed: true,
                components: [
                    { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session" },
                    { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session", deployed_version: 1 },
                ],
            };

            render(element(manifest), { wrapper: queryWrapper });
            await settle();
            (await screen.findByRole("button", { name: "Build & Activate" })).click();

            await screen.findByText(/Could not read the versions of "db" \(db\.json\)/);
            expect(writesToPlatform()).toBe(0);
            expect(log.filter(request => request.includes("deploy"))).toEqual([]);
            expect(addNotification.mock.calls.filter(([message]) => /Deployed|Not found on platform/.test(String(message)))).toEqual([]);
            expect(handleSubmit).not.toHaveBeenCalled();
        });
    });

    describe("what the read decides, and what a save that ends on it leaves", () => {
        const agentWithConnector = (): BuildManifest => ({
            name: "my-build",
            full_validation_passed: true,
            components: [
                { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session" },
                { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session", deployed_version: 1 },
            ],
        });
        const readsOf = (filename: string) => configFetches.filter(([name]) => name === filename).map(([, options]) => options?.version);

        test("a file the list still shows but the read finds no version of is read unpinned, not at the list's version", async () => {
            versions = new Map([
                ["agent.yaml", 1],
                [SAVED.connector.filename, 1],
            ]);
            versionsOnceRead = new Map([["agent.yaml", 1]]);
            planApproved = true;

            render(element(agentWithConnector()), { wrapper: queryWrapper });
            await settle();

            expect(readsOf(SAVED.connector.filename).length).toBeGreaterThan(0);
            expect(readsOf(SAVED.connector.filename).every(version => version === undefined)).toBe(true);
        });

        test("the same in a save-only build", async () => {
            versions = new Map([[SAVED.connector.filename, 1]]);
            versionsOnceRead = new Map();
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            planApproved = true;
            view.rerender(element(manifest));
            await settle();

            expect(readsOf(SAVED.connector.filename).length).toBeGreaterThan(0);
            expect(readsOf(SAVED.connector.filename).every(version => version === undefined)).toBe(true);
        });

        test.each([
            ["with server-side apply", true],
            ["with server-side apply off", false],
        ])("a save whose lookup failed makes no platform request at all, %s", async (_label, apply) => {
            serverApplyEnabled = apply;
            connectorOnPlatform = true;
            versions = new Map([
                [SAVED.connector.filename, 1],
                ["agent.yaml", 1],
            ]);
            failedLookups = new Set([SAVED.connector.filename]);
            render(element(agentWithConnector()), { wrapper: queryWrapper });
            await settle();
            log.length = 0;

            (await screen.findByRole("button", { name: "Build & Activate" })).click();
            await screen.findByText(/Could not read the versions of "db" \(db\.json\)/);
            await settle();

            expect(log).toEqual([]);
        });

        test("a build saved once whose next save fails its lookup: Try again saves the edit, it does not only deploy", async () => {
            await sessionSavedAt("skill", 1);
            const manifest = manifestFor("skill", { deployed_version: 1 });
            const view = await mountAndTakeATurn(manifest, editTo(SAVED.skill.filename, 2));
            await waitFor(() => expect(uploads()).toBe(1));
            await takeATurn(view, manifest, () => {
                versions = new Map([[SAVED.skill.filename, 3]]);
                failedLookups = new Set([SAVED.skill.filename]);
            });
            await screen.findByText(/Could not read the versions of/);
            expect(uploads()).toBe(1);

            failedLookups = new Set();
            screen.getByRole("button", { name: "Retry" }).click();

            await waitFor(() => expect(uploads()).toBe(2));
        });

        test("a build that references a platform skill saves its session connector edit, with no banner", async () => {
            await sessionSavedAt("connector", 1);
            const manifest: BuildManifest = {
                name: "my-build",
                full_validation_passed: true,
                components: [
                    { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session", deployed_version: 1 },
                    { filename: "sample-geography.yaml", type: "skill", name: "sample-geography", status: "deployed", origin: "platform" },
                ],
            };

            await mountAndTakeATurn(manifest, editTo(SAVED.connector.filename, 2));
            await settle();

            expect(screen.queryByText(/Could not read the versions of/)).toBeNull();
            expect(applies()).toBe(1);
            expect(handleSubmit).not.toHaveBeenCalled();
            expect(configFetches.map(([filename]) => filename)).not.toContain("sample-geography.yaml");
        });

        test("a declined question about a file the read found no version of is not asked again after a turn that changed nothing", async () => {
            await sessionSavedAt("skill", 1);
            platformStamp.skill = "hash-of-someone-else";
            versions = new Map([[SAVED.skill.filename, 2]]);
            versionsOnceRead = new Map();
            const manifest = manifestFor("skill", { deployed_version: 1 });
            const view = await mountAndTakeATurn(manifest);
            await screen.findByText(DIALOG);
            versionsOnceRead = undefined;
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await settle();

            await takeATurn(view, manifest);
            await settle();

            expect(screen.queryByText(DIALOG)).toBeNull();
        });
    });

    describe("a save under list lag reads and records what it read", () => {
        const savedUnderLag = async () => {
            await bothSavedAt(1);
            const manifest = mixedManifest();
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            responding = true;
            view.rerender(element(manifest));
            await settle();
            versionsOnceRead = new Map([
                [SAVED.connector.filename, 2],
                [SAVED.skill.filename, 2],
            ]);
            responding = false;
            view.rerender(element(manifest));
            await waitFor(() => expect(versionReads).toBe(1));
            await settle();
            await settle();
            return { view, manifest };
        };

        test("every config the save reads is pinned to the version it read, not the version the list shows", async () => {
            await savedUnderLag();

            const pins = configFetches.map(([, options]) => options?.version);
            expect(pins.length).toBeGreaterThan(0);
            expect(pins.every(version => version === 2)).toBe(true);
        });

        test("what the save records is the version it read, not the version the list shows", async () => {
            await savedUnderLag();

            expect(applies()).toBe(1);
            expect(uploads()).toBe(1);
            expect(recordedByFile.get(SAVED.connector.filename)).toBe(2);
            expect(recordedByFile.get(SAVED.skill.filename)).toBe(2);
        });

        test("after the list catches up, later turns save nothing more of the connector", async () => {
            const { view, manifest } = await savedUnderLag();
            versions = new Map([
                [SAVED.connector.filename, 2],
                [SAVED.skill.filename, 2],
            ]);
            versionsOnceRead = undefined;
            view.rerender(element(manifest));
            await settle();

            await takeATurn(view, manifest);
            await takeATurn(view, manifest);

            expect(applies()).toBe(1);
        });
    });

    describe("what a save that outlived its session leaves, and what the others do not", () => {
        test("a withdrawn question leaves no line in the console and no failure state", async () => {
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
            await sessionSavedAt("connector", 1);
            platformStamp.connector = "t-of-someone-else";
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = await mountAndTakeATurn(manifest, editTo(SAVED.connector.filename, 2));
            await screen.findByText(DIALOG);

            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await settle();

            expect(warn.mock.calls.filter(call => String(call[0]).includes("left its session"))).toEqual([]);
            warn.mockRestore();
        });

        test("a save that fails in the session it began in does not say it outlived it", async () => {
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
            await sessionSavedAt("connector", 1);
            applyFailsWith = 500;

            await mountAndTakeATurn(manifestFor("connector", { deployed_version: 1 }), editTo(SAVED.connector.filename, 2));
            await waitFor(() => expect(displayError).toHaveBeenCalled());

            expect(warn.mock.calls.filter(call => String(call[0]).includes("left its session"))).toEqual([]);
            warn.mockRestore();
        });

        test("a per-kind Build & Activate that fails after a switch deploys nothing in the session switched to", async () => {
            serverApplyEnabled = false;
            connectorOnPlatform = true;
            versions = new Map([
                [SAVED.connector.filename, 1],
                ["agent.yaml", 1],
            ]);
            const manifest: BuildManifest = {
                name: "my-build",
                full_validation_passed: true,
                components: [
                    { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session" },
                    { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session" },
                ],
            };
            let release = () => {};
            holdBeforeRead = new Promise<void>(resolve => {
                release = resolve;
            });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            (await screen.findByRole("button", { name: "Build & Activate" })).click();
            await waitFor(() => expect(versionReads).toBe(1));
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await settle();
            const before = log.length;

            holdBeforeRead = undefined;
            release();
            await settle();
            await settle();

            expect(log.slice(before).filter(request => request.includes("deploy"))).toEqual([]);
            expect(log.slice(before).filter(request => request === `GET ${AGENTS_PATH}`)).toHaveLength(1);
        });

        test("a plan-approval save of an agent and a connector that ends after the switch arms no deploy in the session switched to", async () => {
            bridged = true;
            await sessionSavedAt("connector", 1);
            versions = new Map([
                [SAVED.connector.filename, 1],
                ["agent.yaml", 1],
            ]);
            const manifest: BuildManifest = {
                name: "my-build",
                full_validation_passed: true,
                components: [
                    { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session" },
                    { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session", deployed_version: 1 },
                ],
            };
            let release = () => {};
            holdBeforeRead = new Promise<void>(resolve => {
                release = resolve;
            });
            planApproved = true;
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            await waitFor(() => expect(versionReads).toBe(1));
            planApproved = false;
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await settle();
            const before = log.length;

            holdBeforeRead = undefined;
            release();
            await settle();
            await settle();

            expect(applies()).toBe(1);
            expect(log.slice(before).filter(request => request === `GET ${AGENTS_PATH}`)).toHaveLength(1);
            expect(displayError).not.toHaveBeenCalled();
            expect(handleSubmit).not.toHaveBeenCalled();
        });

        const throughASwitch = async (status?: number) => {
            await sessionSavedAt("connector", 1);
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            let planStarted = false;
            let finishPlan = () => {};
            duringPlan = () => {
                duringPlan = undefined;
                planStarted = true;
                return new Promise<void>(resolve => {
                    finishPlan = resolve;
                });
            };
            responding = true;
            view.rerender(element(manifest));
            await settle();
            versions = new Map([[SAVED.connector.filename, 2]]);
            view.rerender(element(manifest));
            responding = false;
            view.rerender(element(manifest));
            await waitFor(() => expect(planStarted).toBe(true));
            applyFailsWith = status;
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            finishPlan();
            await settle();
        };
        const outlivedLines = (warn: ReturnType<typeof vi.spyOn>) => warn.mock.calls.filter((call: unknown[]) => String(call[0]).includes("left its session"));

        test("a save that succeeds after the switch leaves one line saying it saved", async () => {
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

            await throughASwitch();

            expect(applies()).toBe(1);
            expect(outlivedLines(warn)).toEqual([["[builder] a save ended after the Builder left its session", expect.objectContaining({ outcome: "saved", notWritten: [] })]]);
            warn.mockRestore();
        });

        test("a save that succeeds in its own session does not say it outlived it", async () => {
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
            await sessionSavedAt("connector", 1);

            await mountAndTakeATurn(manifestFor("connector", { deployed_version: 1 }), editTo(SAVED.connector.filename, 2));
            await waitFor(() => expect(applies()).toBe(1));
            await settle();

            expect(outlivedLines(warn)).toEqual([]);
            warn.mockRestore();
        });

        test("a plan-approval save that ends after the switch keeps nothing in the session switched to, whose own approval then saves", async () => {
            bridged = true;
            await sessionSavedAt("connector", 1);
            versions = new Map([
                [SAVED.connector.filename, 1],
                ["agent.yaml", 1],
            ]);
            const manifest: BuildManifest = {
                name: "my-build",
                full_validation_passed: true,
                components: [
                    { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session" },
                    { filename: SAVED.connector.filename, type: "connector", name: SAVED.connector.name, status: "validated", origin: "session", deployed_version: 1 },
                ],
            };
            let release = () => {};
            holdBeforeRead = new Promise<void>(resolve => {
                release = resolve;
            });
            planApproved = true;
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            await waitFor(() => expect(versionReads).toBe(1));
            planApproved = false;
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await settle();
            holdBeforeRead = undefined;
            release();
            await settle();
            await settle();

            planApproved = true;
            view.rerender(element(manifest));
            await settle();
            await settle();

            expect(versionReads).toBe(2);
        });

        test.each([
            ["a connector", "connector"],
            ["a skill", "skill"],
        ] as const)("a plan-approval save of %s that ends after the switch records nothing and arms no deploy in the session switched to", async (_label, kind) => {
            bridged = true;
            await sessionSavedAt(kind, 1);
            const manifest = manifestFor(kind, { deployed_version: 1 });
            versions = new Map([[SAVED[kind].filename, 2]]);
            let release = () => {};
            holdBeforeRead = new Promise<void>(resolve => {
                release = resolve;
            });
            planApproved = true;
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            await waitFor(() => expect(versionReads).toBe(1));
            planApproved = false;
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await settle();
            const before = log.length;

            holdBeforeRead = undefined;
            release();
            await settle();
            await settle();

            expect(writesToPlatform()).toBe(1);
            expect(addNotification).not.toHaveBeenCalled();
            expect(log.slice(before).filter(request => request.includes("deploy"))).toEqual([]);
        });

        test("a partial failure after the switch leaves a line with what it did not write", async () => {
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
            bridged = true;
            await bothSavedAt(1);
            const manifest = mixedManifest();
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            let release = () => {};
            holdBeforeRead = new Promise<void>(resolve => {
                release = resolve;
            });
            versions = new Map([
                [SAVED.connector.filename, 2],
                [SAVED.skill.filename, 2],
            ]);
            await takeATurn(view, manifest);
            await waitFor(() => expect(versionReads).toBe(1));
            refuseWrites = 1;
            sessionId = OTHER_SESSION;
            view.rerender(element(manifest));
            await settle();

            holdBeforeRead = undefined;
            release();
            await settle();

            expect(warn).toHaveBeenCalledWith("[builder] a save ended after the Builder left its session", expect.objectContaining({ outcome: "failed", notWritten: [SAVED.skill.name] }));
            warn.mockRestore();
        });
    });

    describe("a decline, and a failure the platform refuses, do not spend the auto-fix budget twice", () => {
        test("two fixes sent, a decline, then a third fixable failure goes to Try again, not to the builder", async () => {
            await sessionSavedAt("connector", 1);
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            for (const version of [2, 3]) {
                applyFailsWith = 422;
                await takeATurn(view, manifest, editTo(SAVED.connector.filename, version));
                await settle();
            }
            expect(handleSubmit).toHaveBeenCalledTimes(2);
            platformStamp.connector = "t-of-someone-else";
            await takeATurn(view, manifest, editTo(SAVED.connector.filename, 4));
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await settle();
            applyFailsWith = 422;
            await takeATurn(view, manifest, editTo(SAVED.connector.filename, 5));
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Replace and save" }).click();
            await settle();
            await settle();

            expect(handleSubmit).toHaveBeenCalledTimes(2);
        });

        test("a connector write the platform refuses, thrown, is not tried again when the list catches up with what the save read", async () => {
            await sessionSavedAt("connector", 1);
            versions = new Map([[SAVED.connector.filename, 2]]);
            const manifest = manifestFor("connector", { deployed_version: 1 });
            const view = render(element(manifest), { wrapper: queryWrapper });
            await settle();
            responding = true;
            view.rerender(element(manifest));
            await settle();
            versionsOnceRead = new Map([[SAVED.connector.filename, 3]]);
            applyFailsWith = 500;
            responding = false;
            view.rerender(element(manifest));
            await waitFor(() => expect(versionReads).toBe(1));
            await settle();
            const appliesAtTheFailure = applies();

            versions = new Map([[SAVED.connector.filename, 3]]);
            versionsOnceRead = undefined;
            view.rerender(element(manifest));
            await settle();
            await settle();

            expect(versionReads).toBe(1);
            expect(applies()).toBe(appliesAtTheFailure);
        });
    });
});
