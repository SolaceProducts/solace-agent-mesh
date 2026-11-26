// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { BuildManifest } from "./useBuilderManifest";

const h = vi.hoisted(() => ({
    state: {
        isResponding: false,
        sessionId: "session-1" as string,
        manifest: null as BuildManifest | null,
        artifactVersions: new Map<string, number>(),
        serverApplyEnabled: false,
    },
    handleSubmit: vi.fn<(...args: unknown[]) => Promise<unknown>>(),
}));

vi.mock("@/providers/contexts", () => ({
    useChatContext: () => ({
        isResponding: h.state.isResponding,
        handleSubmit: h.handleSubmit,
        sessionId: h.state.sessionId,
    }),
}));

vi.mock("./useBuilderManifest", () => ({
    useBuilderManifest: () => ({ manifest: h.state.manifest, isLoading: false, saveManifest: vi.fn() }),
}));

vi.mock("./useArtifactVersions", () => ({
    useArtifactVersions: () => h.state.artifactVersions,
}));

vi.mock("@/hooks/useIsBuilderServerApplyEnabled", () => ({
    useIsBuilderServerApplyEnabled: () => h.state.serverApplyEnabled,
}));

import { AUTO_FULL_VALIDATION_RETRY_MS, decideValidationAsk, FULL_VALIDATION_REQUEST, fullValidationUnreachable, MAX_AUTO_FULL_VALIDATION_ATTEMPTS, useAutoFullValidation } from "./useAutoFullValidation";

function unstampedManifest(): BuildManifest {
    return {
        name: "proj",
        components: [{ filename: "agent.yaml", type: "agent", name: "MyAgent", origin: "session", status: "validated" }],
    } as BuildManifest;
}

function stampedManifest(): BuildManifest {
    return { ...unstampedManifest(), full_validation_passed: true, full_validation_versions: { "agent.yaml": 1 } } as BuildManifest;
}

const CURRENT_VERSIONS = new Map([["agent.yaml", 1]]);

describe("useAutoFullValidation", () => {
    beforeEach(() => {
        vi.useFakeTimers();
        h.state.isResponding = false;
        h.state.sessionId = "session-1";
        h.state.manifest = null;
        h.state.artifactVersions = new Map();
        h.state.serverApplyEnabled = false;
        h.handleSubmit.mockReset();
        h.handleSubmit.mockResolvedValue(undefined);
    });

    afterEach(() => {
        vi.runOnlyPendingTimers();
        vi.useRealTimers();
    });

    test("auto-validates at most MAX attempts, then reports validationStuck", () => {
        h.state.manifest = unstampedManifest();
        const { result } = renderHook(() => useAutoFullValidation());

        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
        expect(result.current.validationStuck).toBe(false);

        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        expect(h.handleSubmit).toHaveBeenCalledTimes(2);

        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        expect(h.handleSubmit).toHaveBeenCalledTimes(MAX_AUTO_FULL_VALIDATION_ATTEMPTS);
        expect(result.current.validationStuck).toBe(true);

        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS * 3));
        expect(h.handleSubmit).toHaveBeenCalledTimes(MAX_AUTO_FULL_VALIDATION_ATTEMPTS);
    });

    test("does not auto-validate when the manifest is already stamped", () => {
        h.state.manifest = stampedManifest();
        h.state.artifactVersions = CURRENT_VERSIONS;
        const { result } = renderHook(() => useAutoFullValidation());

        expect(h.handleSubmit).not.toHaveBeenCalled();
        expect(result.current.validationStuck).toBe(false);
    });

    test("asks again when the stamp predates an edit, with the same request as a missing stamp", () => {
        h.state.manifest = stampedManifest();
        h.state.artifactVersions = new Map([["agent.yaml", 2]]);
        renderHook(() => useAutoFullValidation());

        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
        expect(h.handleSubmit).toHaveBeenCalledWith(expect.anything(), [], FULL_VALIDATION_REQUEST, null, null, null, null, null, { hidden: true });
    });

    test("asks again for a stamp that recorded no versions", () => {
        h.state.manifest = { ...unstampedManifest(), full_validation_passed: true } as BuildManifest;
        h.state.artifactVersions = CURRENT_VERSIONS;
        renderHook(() => useAutoFullValidation());

        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
    });

    test("waits while a version the stamp needs is still loading", () => {
        h.state.manifest = stampedManifest();
        h.state.artifactVersions = new Map();
        const { result } = renderHook(() => useAutoFullValidation());

        expect(h.handleSubmit).not.toHaveBeenCalled();
        expect(result.current.validationStuck).toBe(false);
    });

    test("asks once the loading version turns out to be past the stamp", () => {
        h.state.manifest = stampedManifest();
        h.state.artifactVersions = new Map();
        const { rerender } = renderHook(() => useAutoFullValidation());
        expect(h.handleSubmit).not.toHaveBeenCalled();

        h.state.artifactVersions = new Map([["agent.yaml", 3]]);
        rerender();
        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
    });

    test("stays quiet once the loading version turns out to be the stamped one", () => {
        h.state.manifest = stampedManifest();
        h.state.artifactVersions = new Map();
        const { rerender } = renderHook(() => useAutoFullValidation());

        h.state.artifactVersions = CURRENT_VERSIONS;
        rerender();
        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS * 3));
        expect(h.handleSubmit).not.toHaveBeenCalled();
    });

    describe.each([
        ["with server apply off", false],
        ["with server apply on", true],
    ])("a version lookup that settled without an answer, %s", (_label, serverApplyEnabled) => {
        const settledWithout = (...filenames: string[]) => Object.assign(new Map<string, number>(), { unresolved: new Set(filenames) });

        test.each([
            ["errored", "agent.yaml"],
            ["returned no versions for a deleted config", "agent.yaml"],
        ])("asks instead of waiting forever when the query %s", (_case, filename) => {
            h.state.serverApplyEnabled = serverApplyEnabled;
            h.state.manifest = stampedManifest();
            h.state.artifactVersions = settledWithout(filename);
            renderHook(() => useAutoFullValidation());

            expect(h.handleSubmit).toHaveBeenCalledTimes(1);
            expect(h.handleSubmit).toHaveBeenCalledWith(expect.anything(), [], FULL_VALIDATION_REQUEST, null, null, null, null, null, { hidden: true });
        });

        test("raises validationStuck once the asks go unanswered", () => {
            h.state.serverApplyEnabled = serverApplyEnabled;
            h.state.manifest = stampedManifest();
            h.state.artifactVersions = settledWithout("agent.yaml");
            const { result } = renderHook(() => useAutoFullValidation());

            act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS * MAX_AUTO_FULL_VALIDATION_ATTEMPTS));
            expect(h.handleSubmit).toHaveBeenCalledTimes(MAX_AUTO_FULL_VALIDATION_ATTEMPTS);
            expect(result.current.validationStuck).toBe(true);
        });
    });

    test("asks when a stamp that was current at the end of a turn turns out stale once the versions refetch", () => {
        h.state.manifest = stampedManifest();
        h.state.artifactVersions = CURRENT_VERSIONS;
        h.state.isResponding = true;
        const { rerender } = renderHook(() => useAutoFullValidation());

        h.state.isResponding = false;
        rerender();
        expect(h.handleSubmit).not.toHaveBeenCalled();

        h.state.artifactVersions = new Map([["agent.yaml", 2]]);
        rerender();

        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
        expect(h.handleSubmit.mock.calls[0][2]).toBe(FULL_VALIDATION_REQUEST);
    });

    test("a new stale state re-arms the attempt budget and the same one does not", () => {
        h.state.manifest = stampedManifest();
        h.state.artifactVersions = new Map([["agent.yaml", 2]]);
        const { result, rerender } = renderHook(() => useAutoFullValidation());
        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        expect(h.handleSubmit).toHaveBeenCalledTimes(MAX_AUTO_FULL_VALIDATION_ATTEMPTS);
        expect(result.current.validationStuck).toBe(true);

        h.state.manifest = { ...stampedManifest() } as BuildManifest;
        rerender();
        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS * 3));
        expect(h.handleSubmit).toHaveBeenCalledTimes(MAX_AUTO_FULL_VALIDATION_ATTEMPTS);

        h.state.artifactVersions = new Map([["agent.yaml", 3]]);
        rerender();
        expect(h.handleSubmit).toHaveBeenCalledTimes(MAX_AUTO_FULL_VALIDATION_ATTEMPTS + 1);
        expect(result.current.validationStuck).toBe(false);
    });

    test("stops asking once the new stamp is current", () => {
        h.state.manifest = stampedManifest();
        h.state.artifactVersions = new Map([["agent.yaml", 2]]);
        const { rerender } = renderHook(() => useAutoFullValidation());
        expect(h.handleSubmit).toHaveBeenCalledTimes(1);

        h.state.manifest = { ...stampedManifest(), full_validation_versions: { "agent.yaml": 2 } } as BuildManifest;
        rerender();
        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS * 3));
        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
    });

    test("retryValidation refills the attempt budget after getting stuck", () => {
        h.state.manifest = unstampedManifest();
        const { result } = renderHook(() => useAutoFullValidation());

        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        expect(result.current.validationStuck).toBe(true);
        const sendsBeforeRetry = h.handleSubmit.mock.calls.length;

        act(() => result.current.retryValidation());
        expect(h.handleSubmit).toHaveBeenCalledTimes(sendsBeforeRetry + 1);
        expect(result.current.validationStuck).toBe(false);
    });

    test("clears validationStuck when the stamp lands via a later manifest refresh", () => {
        h.state.manifest = unstampedManifest();
        const { result, rerender } = renderHook(() => useAutoFullValidation());

        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        expect(result.current.validationStuck).toBe(true);
        const sendsBeforeStamp = h.handleSubmit.mock.calls.length;

        h.state.manifest = stampedManifest();
        h.state.artifactVersions = CURRENT_VERSIONS;
        rerender();
        expect(result.current.validationStuck).toBe(false);
        expect(h.handleSubmit).toHaveBeenCalledTimes(sendsBeforeStamp);
    });

    function redesignedDeployedWorkflow(): BuildManifest {
        return {
            name: "webcam",
            components: [{ filename: "wf.yaml", type: "workflow", name: "Webcam", origin: "session", status: "deployed", deployed_version: 3 }],
        } as BuildManifest;
    }

    test("asks again for a deployed component whose config has moved past its deployed_version", () => {
        h.state.manifest = redesignedDeployedWorkflow();
        h.state.artifactVersions = new Map([["wf.yaml", 4]]);

        renderHook(() => useAutoFullValidation());

        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
        expect(h.handleSubmit.mock.calls[0][2]).toBe(FULL_VALIDATION_REQUEST);
    });

    test("stays silent for a deployed component whose config is provably current", () => {
        h.state.manifest = redesignedDeployedWorkflow();
        h.state.artifactVersions = new Map([["wf.yaml", 3]]);

        renderHook(() => useAutoFullValidation());

        expect(h.handleSubmit).not.toHaveBeenCalled();
    });

    test("re-decides once the deferred version lookup lands", () => {
        h.state.manifest = redesignedDeployedWorkflow();
        h.state.artifactVersions = new Map();

        const { rerender } = renderHook(() => useAutoFullValidation());
        expect(h.handleSubmit).not.toHaveBeenCalled();

        h.state.artifactVersions = new Map([["wf.yaml", 4]]);
        rerender();

        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
        expect(h.handleSubmit.mock.calls[0][2]).toBe(FULL_VALIDATION_REQUEST);
    });

    test("does not ask about a stale component no deploy could refresh", () => {
        h.state.serverApplyEnabled = false;
        h.state.manifest = {
            name: "proj",
            components: [{ filename: "agent.yaml", type: "agent", name: "MyAgent", origin: "session", status: "deployed", deployed_version: 3 }],
        } as BuildManifest;
        h.state.artifactVersions = new Map([["agent.yaml", 4]]);

        renderHook(() => useAutoFullValidation());

        expect(h.handleSubmit).not.toHaveBeenCalled();
    });

    test("cancels the pending retry timer when the stamp lands mid-budget", () => {
        h.state.manifest = unstampedManifest();
        const { rerender } = renderHook(() => useAutoFullValidation());

        expect(h.handleSubmit).toHaveBeenCalledTimes(1);

        h.state.manifest = stampedManifest();
        h.state.artifactVersions = CURRENT_VERSIONS;
        rerender();

        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS * 2));
        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
    });

    test("a session change resets the attempt budget", () => {
        h.state.manifest = unstampedManifest();
        const { result, rerender } = renderHook(() => useAutoFullValidation());

        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        expect(result.current.validationStuck).toBe(true);
        const sendsBeforeSwitch = h.handleSubmit.mock.calls.length;

        h.state.sessionId = "session-2";
        h.state.manifest = unstampedManifest();
        rerender();
        expect(h.handleSubmit).toHaveBeenCalledTimes(sendsBeforeSwitch + 1);
        expect(result.current.validationStuck).toBe(false);
    });
});

describe("fullValidationUnreachable", () => {
    test("says nothing about a build still in progress", () => {
        expect(fullValidationUnreachable([{ name: "A", origin: "session", status: "draft" }])).toBeNull();
        expect(fullValidationUnreachable([{ name: "A", origin: "session", status: "validated" }])).toBeNull();
        expect(fullValidationUnreachable([{ name: "A", origin: "session", status: "error" }])).toBeNull();
    });

    test("reports an empty component list", () => {
        expect(fullValidationUnreachable([])?.message).toContain("no components");
    });

    test("reports a status the gate will never accept, naming it", () => {
        const why = fullValidationUnreachable([{ name: "A", origin: "session", status: "complete" }]);
        expect(why?.message).toContain("'A'");
        expect(why?.message).toContain("complete");
    });

    test("reports an origin outside the set the gate partitions on", () => {
        const why = fullValidationUnreachable([{ name: "A", origin: "platfrm", status: "deployed" }]);
        expect(why?.message).toContain("platfrm");
        expect(why?.key).toContain("origin:");
    });

    test("accepts an absent or empty status", () => {
        expect(fullValidationUnreachable([{ name: "A", origin: "session" }])).toBeNull();
        expect(fullValidationUnreachable([{ name: "A", origin: "session", status: "" }])).toBeNull();
    });

    test("accepts an absent or empty origin", () => {
        expect(fullValidationUnreachable([{ name: "A", status: "draft" }])).toBeNull();
        expect(fullValidationUnreachable([{ name: "A", origin: "", status: "draft" }])).toBeNull();
    });

    test("ignores a platform component's status", () => {
        expect(fullValidationUnreachable([{ name: "A", origin: "platform", status: "whatever" }])).toBeNull();
    });

    test("keys the complaint on the component, not the manifest around it", () => {
        const alone = fullValidationUnreachable([{ name: "A", origin: "session", status: "complete" }]);
        const surrounded = fullValidationUnreachable([
            { name: "A", origin: "session", status: "complete" },
            { name: "B", origin: "session", status: "validated" },
        ]);
        expect(alone?.key).toBe(surrounded?.key);
    });
});

describe("useAutoFullValidation: manifests the gate can never be satisfied by", () => {
    beforeEach(() => {
        vi.useFakeTimers();
        h.state.isResponding = false;
        h.state.sessionId = "session-1";
        h.state.manifest = null;
        h.state.artifactVersions = new Map();
        h.state.serverApplyEnabled = false;
        h.handleSubmit.mockReset();
        h.handleSubmit.mockResolvedValue(undefined);
    });

    afterEach(() => {
        vi.runOnlyPendingTimers();
        vi.useRealTimers();
    });

    test("stays silent while components are still draft", () => {
        h.state.manifest = { name: "proj", components: [{ filename: "a.yaml", type: "agent", name: "A", origin: "session", status: "draft" }] } as BuildManifest;
        renderHook(() => useAutoFullValidation());
        expect(h.handleSubmit).not.toHaveBeenCalled();
    });

    test("stays silent when only some components are validated", () => {
        h.state.manifest = {
            name: "proj",
            components: [
                { filename: "a.yaml", type: "agent", name: "A", origin: "session", status: "validated" },
                { filename: "b.yaml", type: "agent", name: "B", origin: "session", status: "draft" },
            ],
        } as BuildManifest;
        renderHook(() => useAutoFullValidation());
        expect(h.handleSubmit).not.toHaveBeenCalled();
    });

    test("stays silent for a manifest of only platform components", () => {
        h.state.manifest = { name: "proj", components: [{ filename: "", type: "agent", name: "A", origin: "platform", status: "deployed" }] } as BuildManifest;
        renderHook(() => useAutoFullValidation());
        expect(h.handleSubmit).not.toHaveBeenCalled();
    });

    test("asks about an origin the gate cannot partition on", () => {
        h.state.manifest = { name: "proj", components: [{ filename: "a.yaml", type: "agent", name: "A", origin: "platfrm", status: "deployed" }] } as BuildManifest;
        renderHook(() => useAutoFullValidation());
        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
        expect(String(h.handleSubmit.mock.calls[0][2])).toContain("platfrm");
    });

    test("asks about a component whose status the gate does not recognise", () => {
        h.state.manifest = { name: "proj", components: [{ filename: "a.yaml", type: "agent", name: "A", origin: "session", status: "complete" }] } as BuildManifest;
        renderHook(() => useAutoFullValidation());
        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
        expect(String(h.handleSubmit.mock.calls[0][2])).toContain("complete");
    });

    test("asks about a manifest with no components only when a turn just ended", () => {
        h.state.manifest = { name: "proj", components: [] } as unknown as BuildManifest;
        const { rerender } = renderHook(() => useAutoFullValidation());
        expect(h.handleSubmit).not.toHaveBeenCalled();

        h.state.isResponding = true;
        rerender();
        h.state.isResponding = false;
        rerender();
        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
        expect(String(h.handleSubmit.mock.calls[0][2])).toContain("no components");
    });

    test("asks for the cross-component check when every component is validated", () => {
        h.state.manifest = unstampedManifest();
        renderHook(() => useAutoFullValidation());
        expect(h.handleSubmit).toHaveBeenCalledWith(expect.anything(), [], FULL_VALIDATION_REQUEST, null, null, null, null, null, { hidden: true });
    });

    test("says nothing once the stamp is on the manifest", () => {
        h.state.manifest = stampedManifest();
        h.state.artifactVersions = CURRENT_VERSIONS;
        renderHook(() => useAutoFullValidation());
        expect(h.handleSubmit).not.toHaveBeenCalled();
    });

    test("bounds the unreachable request by the same attempt budget", () => {
        h.state.manifest = { name: "proj", components: [{ filename: "a.yaml", type: "agent", name: "A", origin: "session", status: "complete" }] } as BuildManifest;
        const { result } = renderHook(() => useAutoFullValidation());

        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        expect(h.handleSubmit).toHaveBeenCalledTimes(MAX_AUTO_FULL_VALIDATION_ATTEMPTS);
        expect(result.current.validationStuck).toBe(true);
    });

    test("does not refill the budget as the rest of the build advances", () => {
        const withB = (status: string): BuildManifest =>
            ({
                name: "proj",
                components: [
                    { filename: "a.yaml", type: "agent", name: "A", origin: "session", status: "complete" },
                    { filename: "b.yaml", type: "agent", name: "B", origin: "session", status },
                ],
            }) as BuildManifest;

        h.state.manifest = withB("draft");
        const { rerender } = renderHook(() => useAutoFullValidation());

        for (const status of ["error", "deployed", "validated"]) {
            h.state.isResponding = true;
            rerender();
            h.state.manifest = withB(status);
            h.state.isResponding = false;
            rerender();
        }

        expect(h.handleSubmit.mock.calls.length).toBeLessThanOrEqual(MAX_AUTO_FULL_VALIDATION_ATTEMPTS);
    });

    test("drops the armed retry once the agent has fixed the component", () => {
        h.state.manifest = { name: "proj", components: [{ filename: "a.yaml", type: "agent", name: "A", origin: "session", status: "complete" }] } as BuildManifest;
        const { result, rerender } = renderHook(() => useAutoFullValidation());
        expect(h.handleSubmit).toHaveBeenCalledTimes(1);

        h.state.isResponding = true;
        rerender();
        h.state.manifest = { name: "proj", components: [{ filename: "a.yaml", type: "agent", name: "A", origin: "session", status: "draft" }] } as BuildManifest;
        h.state.isResponding = false;
        rerender();

        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
        expect(result.current.validationStuck).toBe(false);
    });

    test("the manual retry re-asks the unreachable question, not the ready one", () => {
        h.state.manifest = { name: "proj", components: [{ filename: "a.yaml", type: "agent", name: "A", origin: "session", status: "complete" }] } as BuildManifest;
        const { result } = renderHook(() => useAutoFullValidation());

        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        act(() => vi.advanceTimersByTime(AUTO_FULL_VALIDATION_RETRY_MS));
        expect(result.current.validationStuck).toBe(true);

        h.handleSubmit.mockClear();
        act(() => result.current.retryValidation());
        expect(h.handleSubmit).toHaveBeenCalledTimes(1);
        expect(String(h.handleSubmit.mock.calls[0][2])).toContain("complete");
    });
});

describe("decideValidationAsk — a deployed claim with no evidence behind it", () => {
    const ask = (components: Parameters<typeof decideValidationAsk>[0]) => decideValidationAsk(components, "fp", true);

    test("asks when every session component is validated", () => {
        expect(ask([{ name: "A", origin: "session", status: "validated" }]).kind).toBe("ask");
    });

    test("asks when a component claims deployed but carries no deployed_version", () => {
        expect(
            ask([
                { name: "A", origin: "session", status: "validated" },
                { name: "B", origin: "session", status: "deployed" },
            ]).kind
        ).toBe("ask");
    });

    test("retires on a genuinely deployed build, so a finished build is not re-asked", () => {
        expect(
            ask([
                { name: "A", origin: "session", status: "deployed", deployed_version: 2 },
                { name: "B", origin: "session", status: "deployed", deployed_version: 2 },
            ]).kind
        ).toBe("retire");
    });

    test("a deployed sibling does not veto the ask for a component that still needs validating", () => {
        expect(
            ask([
                { name: "A", origin: "session", status: "validated" },
                { name: "B", origin: "session", status: "deployed", deployed_version: 2 },
            ]).kind
        ).toBe("ask");
    });

    test("still retires while any component is being worked on", () => {
        expect(
            ask([
                { name: "A", origin: "session", status: "validated" },
                { name: "B", origin: "session", status: "deployed", deployed_version: 2 },
                { name: "C", origin: "session", status: "draft" },
            ]).kind
        ).toBe("retire");
    });

    test("a platform component never blocks the ask — it reports no session status", () => {
        expect(
            ask([
                { name: "A", origin: "session", status: "validated" },
                { name: "P", origin: "platform", status: "deployed" },
            ]).kind
        ).toBe("ask");
    });
});

describe("decideValidationAsk — a blocked component", () => {
    const blocked = { name: "inventory_api", type: "connector", status: "blocked", origin: "session", filename: "inventory_api.json" };
    const validated = { name: "A", type: "agent", status: "validated", origin: "session", filename: "a.yaml" };

    test("is a recognised status, so it is never reported as one the gate cannot reach", () => {
        expect(fullValidationUnreachable([blocked, validated])).toBeNull();
    });

    test("is not asked about: nothing is still being worked on by the Builder, and validation will not stamp", () => {
        expect(decideValidationAsk([blocked, validated], "fp", true)).toEqual({ kind: "retire" });
    });
});
