// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type FunctionComponent, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { builderKeys } from "@/api/builder/keys";

const TARGET = "Éclair Café";
const TEST_AGENT_PATH = "/api/internal/platform/builder/sessions/build-session/test-agent";

type Component = { name: string; type: string; filename: string; status: string; origin: string };

const deps = vi.hoisted(() => ({
    liveAgents: [] as { name?: string; displayName?: string; display_name?: string }[],
    buildResponding: false,
    sessionId: "build-session",
    components: [] as Record<string, unknown>[],
    existingAgent: null as Record<string, unknown> | null,
    post: vi.fn(),
    testSessionLifecycle: vi.fn(),
}));

let TestModePanel: typeof import("./TestModePanel").TestModePanel;

async function loadPanelOverStubbedDependencies() {
    vi.resetModules();
    vi.doMock("@/api/client", () => ({
        api: {
            webui: {
                post: deps.post,
                get: async (url: string) => (url === `/api/internal/platform/builder/sessions/${deps.sessionId}/test-agent` && deps.existingAgent ? { status: 200, ok: true, json: async () => deps.existingAgent } : { status: 404, ok: false }),
            },
        },
    }));
    vi.doMock("@/providers/contexts", async importOriginal => ({
        ...(await importOriginal<typeof import("@/providers/contexts")>()),
        useChatContext: () => ({ sessionId: deps.sessionId, agents: deps.liveAgents, isResponding: deps.buildResponding }),
    }));
    vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => ({ data: [] }) }));
    vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => false }));
    vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: () => new Map() }));
    vi.doMock("./hooks/useBuilderManifest", () => ({ useBuilderManifest: () => ({ manifest: { components: deps.components } }) }));
    vi.doMock("./hooks/useTestSessionLifecycle", () => ({
        useTestSessionLifecycle: (args: unknown) => {
            deps.testSessionLifecycle(args);
            return { testSessionId: null, sessionError: null, retry: vi.fn() };
        },
    }));
    ({ TestModePanel } = await import("./TestModePanel"));
}

const component = (type: string, status = "deployed", name = TARGET): Component => ({ name, type, filename: "c.yaml", status, origin: "session" });

const testAgentFor = (kind: string, sourceComponentName = TARGET) => ({
    name: "test-agent",
    sourceComponentName,
    sourceComponentKind: kind,
    deploymentStatus: "deployed",
    runtimeStatus: { status: "running" },
});

const httpError = (status: number, message: string) => Object.assign(new Error(message), { status });

const networkHop = () => new Promise(resolve => setTimeout(resolve, 15));

const rejectAfterHop = (error: unknown) =>
    deps.post.mockImplementation(async () => {
        await networkHop();
        throw error;
    });

const failCreateWith = (status: number, message: string) => rejectAfterHop(httpError(status, message));

const succeedCreate = (kind = "workflow", sourceComponentName = TARGET) =>
    deps.post.mockImplementation(async () => {
        deps.existingAgent = testAgentFor(kind, sourceComponentName);
        return deps.existingAgent;
    });

function newSession() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
    return { client, enterTestMode: (props: PanelProps = {}) => render(panel(props), { wrapper }) };
}

type PanelProps = NonNullable<Parameters<typeof TestModePanel>[0]>;
const panel = (props: PanelProps = {}) => createElement(TestModePanel as FunctionComponent<PanelProps>, props);
const postedComponentNames = () => deps.post.mock.calls.map(([, body]) => (body as { componentName: string }).componentName);

const findErrorText = (message: string) => screen.findByText(message);
const spinner = () => screen.queryByText(/Spinning up a test agent/);
const tryAgain = () => screen.queryByRole("button", { name: "Try again" });
const settle = () => act(async () => {});
const QUIET_PERIOD_MS = 300;
const quietPeriod = () =>
    act(async () => {
        await new Promise(resolve => setTimeout(resolve, QUIET_PERIOD_MS));
    });

beforeEach(async () => {
    deps.liveAgents = [{ name: TARGET }];
    deps.buildResponding = false;
    deps.sessionId = "build-session";
    deps.components = [component("workflow")];
    deps.existingAgent = null;
    deps.post.mockReset();
    deps.testSessionLifecycle.mockReset();
    await loadPanelOverStubbedDependencies();
});

describe("TestModePanel: test-agent create failure for a live target", () => {
    it.each([
        [404, `component not found: ${TARGET}`],
        [500, "prior ephemeral test agent is still being torn down"],
    ])("shows the error and Try again, not a spinner, when the create fails with %i", async (status, message) => {
        failCreateWith(status, message);
        newSession().enterTestMode();

        await findErrorText(message);
        expect(tryAgain()).not.toBeNull();
        expect(spinner()).toBeNull();
        expect(deps.post).toHaveBeenCalledTimes(1);
        expect(deps.post).toHaveBeenCalledWith(TEST_AGENT_PATH, { componentName: TARGET, componentKind: "workflow" });
    });

    it("keeps the error on screen and sends no further creates while it is shown", async () => {
        failCreateWith(500, "boom");
        newSession().enterTestMode();
        await findErrorText("boom");

        await quietPeriod();

        expect(screen.getByText("boom")).toBeInTheDocument();
        expect(deps.post).toHaveBeenCalledTimes(1);
    });

    it("keeps the error on screen for a skill whose manifest status is already deployed", async () => {
        deps.components = [component("skill")];
        failCreateWith(404, "skill not found");
        newSession().enterTestMode();

        await findErrorText("skill not found");
        await quietPeriod();

        expect(tryAgain()).not.toBeNull();
        expect(deps.post).toHaveBeenCalledTimes(1);
    });

    it("Try again fires the create exactly once and shows the new failure if it fails again", async () => {
        failCreateWith(500, "first failure");
        newSession().enterTestMode();
        await findErrorText("first failure");

        failCreateWith(500, "second failure");
        fireEvent.click(tryAgain()!);

        await findErrorText("second failure");
        await quietPeriod();
        expect(deps.post).toHaveBeenCalledTimes(2);
        expect(spinner()).toBeNull();
    });

    it("Try again recovers into the test chat when the retry succeeds", async () => {
        failCreateWith(500, "transient");
        newSession().enterTestMode();
        await findErrorText("transient");

        succeedCreate();
        fireEvent.click(tryAgain()!);

        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalled());
        expect(screen.queryByText("transient")).toBeNull();
        expect(tryAgain()).toBeNull();
        expect(deps.post).toHaveBeenCalledTimes(2);
    });

    it.each([
        ["name", { name: TARGET }],
        ["displayName", { displayName: TARGET }],
        ["display_name", { display_name: TARGET }],
    ])("retries once on its own when the target was not live at failure and then goes live (listed by %s)", async (_shape, liveEntry) => {
        deps.liveAgents = [];
        failCreateWith(404, "not deployed yet");
        const { enterTestMode } = newSession();
        const view = enterTestMode();
        await findErrorText("not deployed yet");
        expect(deps.post).toHaveBeenCalledTimes(1);

        succeedCreate();
        deps.liveAgents = [liveEntry];
        view.rerender(panel());

        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalled());
        expect(deps.post).toHaveBeenCalledTimes(2);
    });

    it("keeps the error of a second failure even when the target went live while that retry was in flight", async () => {
        deps.liveAgents = [];
        failCreateWith(404, "not deployed yet");
        const view = newSession().enterTestMode();
        await findErrorText("not deployed yet");

        deps.post.mockImplementation(async () => {
            await networkHop();
            deps.liveAgents = [{ name: TARGET }];
            throw httpError(500, "second failure");
        });
        fireEvent.click(tryAgain()!);
        await findErrorText("second failure");
        view.rerender(panel());
        await quietPeriod();

        expect(screen.getByText("second failure")).toBeInTheDocument();
        expect(deps.post).toHaveBeenCalledTimes(2);
    });

    it("clears the error on Try again right away, and creates once the build turn that was running ends", async () => {
        failCreateWith(500, "first failure");
        const view = newSession().enterTestMode();
        await findErrorText("first failure");

        deps.buildResponding = true;
        succeedCreate();
        fireEvent.click(tryAgain()!);
        view.rerender(panel());
        await settle();

        expect(screen.queryByText("first failure")).toBeNull();
        expect(spinner()).not.toBeNull();
        expect(deps.post).toHaveBeenCalledTimes(1);

        deps.buildResponding = false;
        view.rerender(panel());

        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalled());
        expect(deps.post).toHaveBeenCalledTimes(2);
    });

    it("does not show a failed target's error against a different target that is already ready", async () => {
        const OTHER = "Other Agent";
        deps.components = [component("workflow"), component("workflow", "deployed", OTHER)];
        deps.liveAgents = [{ name: TARGET }, { name: OTHER }];
        failCreateWith(500, "first target failed");
        const { client, enterTestMode } = newSession();
        const view = enterTestMode({ testTargetOverride: TARGET });
        await findErrorText("first target failed");

        client.setQueryData(builderKeys.testAgent("build-session"), testAgentFor("workflow", OTHER));
        view.rerender(panel({ testTargetOverride: OTHER }));

        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalledWith(expect.objectContaining({ targetName: OTHER })));
        expect(screen.queryByText("first target failed")).toBeNull();
        expect(deps.post).toHaveBeenCalledTimes(1);
    });

    it("Try again keeps working when it is used twice in a row", async () => {
        failCreateWith(500, "first failure");
        newSession().enterTestMode();
        await findErrorText("first failure");

        failCreateWith(500, "second failure");
        fireEvent.click(tryAgain()!);
        await findErrorText("second failure");

        succeedCreate();
        fireEvent.click(tryAgain()!);

        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalled());
        expect(deps.post).toHaveBeenCalledTimes(3);
    });

    it("shows a create failure after a build session change, with exactly one create per session", async () => {
        failCreateWith(500, "first session failure");
        const view = newSession().enterTestMode();
        await findErrorText("first session failure");

        deps.sessionId = "other-session";
        failCreateWith(500, "second session failure");
        view.rerender(panel());

        await findErrorText("second session failure");
        await quietPeriod();
        expect(screen.queryByText("first session failure")).toBeNull();
        expect(deps.post).toHaveBeenCalledTimes(2);
    });

    it("keeps a real error on screen when the target drops off the live list afterwards", async () => {
        failCreateWith(500, "real failure");
        const view = newSession().enterTestMode();
        await findErrorText("real failure");

        deps.liveAgents = [];
        view.rerender(panel());
        await quietPeriod();

        expect(screen.getByText("real failure")).toBeInTheDocument();
        expect(deps.post).toHaveBeenCalledTimes(1);
    });

    it("shows the deploy-first message, not the old error, when the target stops being deployed", async () => {
        failCreateWith(500, "real failure");
        const view = newSession().enterTestMode();
        await findErrorText("real failure");

        deps.components = [component("workflow", "draft")];
        deps.liveAgents = [];
        view.rerender(panel());

        await screen.findByText(/before you can test it/);
        expect(screen.queryByText("real failure")).toBeNull();
    });

    it("shows the empty-manifest message, not the failed target's error, when the target disappears", async () => {
        failCreateWith(500, "original failure");
        const view = newSession().enterTestMode({ testTargetOverride: TARGET });
        await findErrorText("original failure");

        view.rerender(panel({ testTargetOverride: "No Such Component" }));

        await screen.findByText(/Once you.ve built a workflow or agent/);
        expect(screen.queryByText("original failure")).toBeNull();
    });

    it.each([
        ["blank", new Error("  \n")],
        ["empty", new Error("")],
        ["a plain string rejection", "gateway timeout"],
        ["an undefined rejection", undefined],
    ])("falls back to a generic message for %s", async (_kind, error) => {
        rejectAfterHop(error);
        newSession().enterTestMode();

        await findErrorText("Failed to create test agent.");
        expect(tryAgain()).not.toBeNull();
    });
});

describe("TestModePanel: a failed create belongs to the identity it was sent for", () => {
    const OTHER = "Other Agent";
    const addressedAs = (target: { name: string }) => `addr-${target.name}`;
    const original = { resolveAddressableName: addressedAs, testTargetOverride: TARGET };
    const sentAs = (overrides: Partial<{ sessionId: string; componentName: string; componentKind: string }>) => ({ sessionId: "build-session", componentName: addressedAs({ name: TARGET }), componentKind: "workflow", ...overrides });

    const identityChanges = [
        {
            dimension: "target name",
            props: { resolveAddressableName: addressedAs, testTargetOverride: OTHER },
            apply: () => {
                deps.components = [component("workflow"), component("workflow", "deployed", OTHER)];
            },
            nowLive: [addressedAs({ name: OTHER })],
            sent: sentAs({ componentName: addressedAs({ name: OTHER }) }),
        },
        {
            dimension: "target kind",
            props: original,
            apply: () => {
                deps.components = [component("skill")];
            },
            nowLive: [addressedAs({ name: TARGET })],
            sent: sentAs({ componentKind: "skill" }),
        },
        {
            dimension: "build session",
            props: original,
            apply: () => {
                deps.sessionId = "other-session";
            },
            nowLive: [addressedAs({ name: TARGET })],
            sent: sentAs({ sessionId: "other-session" }),
        },
        {
            dimension: "addressable name",
            props: { resolveAddressableName: () => "renamed-addr", testTargetOverride: TARGET },
            apply: () => {},
            nowLive: ["renamed-addr"],
            sent: sentAs({ componentName: "renamed-addr" }),
        },
    ];
    const liveAs = (names: string[]) => names.map(name => ({ name }));
    const expectCreateSentFor = (sent: ReturnType<typeof sentAs>) =>
        expect(deps.post).toHaveBeenLastCalledWith(`/api/internal/platform/builder/sessions/${sent.sessionId}/test-agent`, { componentName: sent.componentName, componentKind: sent.componentKind });

    it.each(identityChanges)("hides the error when the $dimension changes, then creates for the new one once the build turn ends", async ({ props, apply, nowLive, sent }) => {
        deps.liveAgents = liveAs([addressedAs({ name: TARGET })]);
        failCreateWith(500, "original failure");
        const view = newSession().enterTestMode(original);
        await findErrorText("original failure");

        deps.buildResponding = true;
        succeedCreate(sent.componentKind, sent.componentName);
        apply();
        deps.liveAgents = liveAs([addressedAs({ name: TARGET }), ...nowLive]);
        view.rerender(panel(props));
        await settle();

        expect(screen.queryByText("original failure")).toBeNull();
        expect(spinner()).not.toBeNull();
        expect(deps.post).toHaveBeenCalledTimes(1);

        deps.buildResponding = false;
        view.rerender(panel(props));

        await waitFor(() => expect(deps.post).toHaveBeenCalledTimes(2));
        expectCreateSentFor(sent);
        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalledWith(expect.objectContaining({ builderSessionId: sent.sessionId, targetName: props.testTargetOverride })));
    });

    it.each(identityChanges)("creates exactly once when the $dimension changes in the same render the target goes live", async ({ props, apply, nowLive, sent }) => {
        deps.liveAgents = [];
        failCreateWith(404, "not deployed yet");
        const { client, enterTestMode } = newSession();
        const view = enterTestMode(original);
        await findErrorText("not deployed yet");

        if (sent.sessionId !== "build-session") client.setQueryData(builderKeys.testAgent(sent.sessionId), testAgentFor("workflow", "Stale"));
        succeedCreate(sent.componentKind, sent.componentName);
        apply();
        deps.liveAgents = liveAs(nowLive);
        view.rerender(panel(props));

        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalledWith(expect.objectContaining({ builderSessionId: sent.sessionId, targetName: props.testTargetOverride })));
        await quietPeriod();
        expect(deps.post).toHaveBeenCalledTimes(2);
        expectCreateSentFor(sent);
    });

    it("sends exactly one create for the new target when switching away from a failed one", async () => {
        deps.components = [component("workflow"), component("workflow", "deployed", OTHER)];
        deps.liveAgents = [{ name: OTHER }];
        deps.post.mockImplementation(async (_path, body: { componentName: string }) => {
            await networkHop();
            if (body.componentName === TARGET) throw httpError(404, "not deployed yet");
            deps.existingAgent = testAgentFor("workflow", OTHER);
            return deps.existingAgent;
        });
        const view = newSession().enterTestMode({ testTargetOverride: TARGET });
        await findErrorText("not deployed yet");

        view.rerender(panel({ testTargetOverride: OTHER }));

        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalledWith(expect.objectContaining({ targetName: OTHER })));
        await settle();
        expect(postedComponentNames()).toEqual([TARGET, OTHER]);
        expect(screen.queryByText("not deployed yet")).toBeNull();
    });

    it("shows the new target's own failure after switching away from a failed one", async () => {
        deps.components = [component("workflow"), component("workflow", "deployed", OTHER)];
        deps.liveAgents = [{ name: OTHER }];
        deps.post.mockImplementation(async (_path, body: { componentName: string }) => {
            await networkHop();
            throw httpError(500, body.componentName === TARGET ? "first failure" : "second failure");
        });
        const view = newSession().enterTestMode({ testTargetOverride: TARGET });
        await findErrorText("first failure");

        view.rerender(panel({ testTargetOverride: OTHER }));

        await findErrorText("second failure");
        await settle();
        expect(screen.queryByText("first failure")).toBeNull();
        expect(postedComponentNames()).toEqual([TARGET, OTHER]);
    });

    it("scopes the error to the addressable name the create was sent under", async () => {
        failCreateWith(404, "legacy name failure");
        newSession().enterTestMode({ resolveAddressableName: () => "legacy-addr" });

        await findErrorText("legacy name failure");
        expect(deps.post).toHaveBeenCalledWith(TEST_AGENT_PATH, { componentName: "legacy-addr", componentKind: "workflow" });
        expect(tryAgain()).not.toBeNull();
    });

    it("auto-retries once when the target goes live under its addressable name", async () => {
        deps.liveAgents = [];
        failCreateWith(404, "not deployed yet");
        const view = newSession().enterTestMode({ resolveAddressableName: () => "legacy-addr" });
        await findErrorText("not deployed yet");

        succeedCreate("workflow", "legacy-addr");
        deps.liveAgents = [{ name: "legacy-addr" }];
        view.rerender(panel({ resolveAddressableName: () => "legacy-addr" }));

        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalled());
        expect(deps.post).toHaveBeenCalledTimes(2);
    });

    it("auto-retries a skill when its manifest status drops out of deployed and comes back", async () => {
        deps.components = [component("skill")];
        failCreateWith(404, "skill failure");
        const view = newSession().enterTestMode();
        await findErrorText("skill failure");

        deps.components = [component("skill", "draft")];
        view.rerender(panel());
        await settle();
        expect(deps.post).toHaveBeenCalledTimes(1);

        succeedCreate("skill");
        deps.components = [component("skill")];
        view.rerender(panel());

        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalled());
        expect(deps.post).toHaveBeenCalledTimes(2);
    });
});

describe("TestModePanel: test-agent create success and re-entry", () => {
    it("creates the test agent once and mounts the test chat", async () => {
        succeedCreate();
        newSession().enterTestMode();

        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalled());
        expect(deps.testSessionLifecycle).toHaveBeenCalledWith(expect.objectContaining({ builderSessionId: "build-session", targetName: TARGET }));
        expect(spinner()).toBeNull();
        expect(deps.post).toHaveBeenCalledTimes(1);
    });

    it("does not create a second test agent when Test mode is entered again", async () => {
        succeedCreate();
        const { enterTestMode } = newSession();
        const first = enterTestMode();
        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalled());
        first.unmount();
        deps.testSessionLifecycle.mockClear();

        enterTestMode();
        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalled());
        await settle();

        expect(deps.post).toHaveBeenCalledTimes(1);
    });

    it("tries once per entry after a failure instead of looping, and recovers when the backend does", async () => {
        failCreateWith(500, "down");
        const { enterTestMode } = newSession();
        const first = enterTestMode();
        await findErrorText("down");
        first.unmount();

        succeedCreate();
        enterTestMode();
        await waitFor(() => expect(deps.testSessionLifecycle).toHaveBeenCalled());

        expect(deps.post).toHaveBeenCalledTimes(2);
    });
});
