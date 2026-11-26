// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { BuildManifest } from "./useBuilderManifest";
import {
    deployOutcomeErrorMessage,
    FLIP_BLOCKED_MESSAGE,
    FLIP_FAILED_MESSAGE,
    flipFailureReport,
    liveShieldsFromDowngrade,
    ownPlatformStatus,
    platformStatusOfComponent,
    reconcileComponentStatuses,
    shouldDowngradeComponent,
    shouldDowngradeToValidated,
    shouldUpgradeToDeployed,
} from "./useManifestDeployment";

type Comp = NonNullable<BuildManifest["components"]>[number];
const comp = (name: string, status: string, extra: Partial<Comp> = {}): Comp => ({ filename: `${name}.yaml`, type: "agent", name, status, origin: "session", ...extra }) as Comp;

const run = (m: BuildManifest, up: string[], down: string[]) => reconcileComponentStatuses(new Set(up), new Set(down))(m);
const statusOf = (m: BuildManifest, name: string) => m.components?.find(c => c.name === name)?.status;

describe("reconcileComponentStatuses", () => {
    const manifest: BuildManifest = {
        name: "b",
        components: [comp("A", "validated"), comp("B", "deployed"), comp("C", "deployed"), comp("D", "validated"), comp("E", "deployed")],
    };

    test("upgrades validated → deployed for upgrade names", () => {
        expect(statusOf(run(manifest, ["A"], []), "A")).toBe("deployed");
    });

    test("downgrades deployed → validated for downgrade names (component gone from platform)", () => {
        expect(statusOf(run(manifest, [], ["B"]), "B")).toBe("validated");
    });

    test("does not touch components outside the sets", () => {
        const out = run(manifest, ["A"], ["B"]);
        expect(statusOf(out, "C")).toBe("deployed");
    });

    test("only acts on the matching prior status (no cross-apply)", () => {
        const out = run(manifest, ["E"], ["D"]);
        expect(statusOf(out, "D")).toBe("validated");
        expect(statusOf(out, "E")).toBe("deployed");
    });

    test("applies upgrade and downgrade together in one pass", () => {
        const out = run(manifest, ["A"], ["B"]);
        expect(statusOf(out, "A")).toBe("deployed");
        expect(statusOf(out, "B")).toBe("validated");
    });
});

describe("shouldDowngradeToValidated (downgrade safety gates)", () => {
    const base = { status: "deployed", platformStatus: undefined as string | undefined, kindFetched: true, savedThisSession: false };

    test("downgrades a manifest-deployed component the platform no longer has (absent, kind fetched, not this session)", () => {
        expect(shouldDowngradeToValidated({ ...base, platformStatus: undefined })).toBe(true);
    });
    test("downgrades when the platform reports a non-deployed status (undeployed)", () => {
        expect(shouldDowngradeToValidated({ ...base, platformStatus: "not_deployed" })).toBe(true);
    });
    test("does NOT downgrade when the platform still reports deployed", () => {
        expect(shouldDowngradeToValidated({ ...base, platformStatus: "deployed" })).toBe(false);
    });
    test("does NOT downgrade a component deployed in THIS session (post-deploy eventual-consistency race guard)", () => {
        expect(shouldDowngradeToValidated({ ...base, platformStatus: "not_deployed", savedThisSession: true })).toBe(false);
    });
    test("does NOT downgrade when the kind's platform fetch FAILED (absence not authoritative)", () => {
        expect(shouldDowngradeToValidated({ ...base, platformStatus: undefined, kindFetched: false })).toBe(false);
    });
    test("only acts on a currently-deployed manifest status", () => {
        expect(shouldDowngradeToValidated({ ...base, status: "validated", platformStatus: undefined })).toBe(false);
    });
});

describe("shouldDowngradeComponent (live-mesh override)", () => {
    const wouldDowngrade = { status: "deployed", platformStatus: undefined as string | undefined, kindFetched: true, savedThisSession: false };

    test("suppresses the downgrade when the component is still live on the mesh", () => {
        expect(shouldDowngradeComponent({ ...wouldDowngrade, liveOnMesh: true })).toBe(false);
    });

    test("downgrades when the component is NOT live on the mesh (genuinely gone)", () => {
        expect(shouldDowngradeComponent({ ...wouldDowngrade, liveOnMesh: false })).toBe(true);
    });

    test("does not invent a downgrade just because it's not live (defers to the base gates)", () => {
        expect(shouldDowngradeComponent({ ...wouldDowngrade, platformStatus: "deployed", liveOnMesh: false })).toBe(false);
    });
});

describe("deployOutcomeErrorMessage", () => {
    test("null when nothing failed or is missing (success)", () => {
        expect(deployOutcomeErrorMessage([], [], [])).toBeNull();
    });
    test("surfaces a missing-on-platform component (so the bridged banner isn't silent)", () => {
        expect(deployOutcomeErrorMessage(["order_exception_gateway"], [], [])).toContain("Not found on platform: order_exception_gateway");
    });
    test("surfaces a per-component deploy failure", () => {
        expect(deployOutcomeErrorMessage([], [{ name: "gw", error: "internal error" }], [])).toContain("Deploy failed: gw (internal error)");
    });
    test("combines both missing and failed into one message", () => {
        const msg = deployOutcomeErrorMessage(["gwA"], [{ name: "gwB", error: "500" }], []);
        expect(msg).toContain("Deploy failed: gwB (500)");
        expect(msg).toContain("Not found on platform: gwA");
    });
});

describe("flipFailureReport", () => {
    test("says nothing when the flip landed", () => {
        expect(flipFailureReport({ ok: true })).toBeNull();
    });

    test("routes a refusal to the blocked channel, with a remedy that exists", () => {
        expect(flipFailureReport({ ok: false, reason: "unreadable" })).toEqual({ channel: "blocked", message: FLIP_BLOCKED_MESSAGE });
        expect(FLIP_BLOCKED_MESSAGE).not.toMatch(/Retry/);
        expect(FLIP_BLOCKED_MESSAGE).toMatch(/repair the plan/);
    });

    test("says nothing about a write dropped by a session change", () => {
        expect(flipFailureReport({ ok: false, reason: "session_changed" })).toBeNull();
    });

    test("routes a transient failure to the ordinary channel, which does offer Retry", () => {
        expect(flipFailureReport({ ok: false, reason: "failed" })).toEqual({ channel: "error", message: FLIP_FAILED_MESSAGE });
        expect(FLIP_FAILED_MESSAGE).toMatch(/Retry/);
    });
});

describe("shouldUpgradeToDeployed", () => {
    const base = { status: "validated", platformStatus: "deployed", runningStaleConfig: false };

    test("upgrades when the platform confirms a component the manifest still calls validated", () => {
        expect(shouldUpgradeToDeployed(base)).toBe(true);
    });

    test("refuses while the component is provably running a superseded config", () => {
        expect(shouldUpgradeToDeployed({ ...base, runningStaleConfig: true })).toBe(false);
    });

    test("still upgrades when staleness cannot be established", () => {
        expect(shouldUpgradeToDeployed({ ...base, runningStaleConfig: false })).toBe(true);
    });

    test("only upgrades from validated", () => {
        expect(shouldUpgradeToDeployed({ ...base, status: "deployed" })).toBe(false);
        expect(shouldUpgradeToDeployed({ ...base, status: "draft" })).toBe(false);
    });

    test("requires the platform to report deployed", () => {
        expect(shouldUpgradeToDeployed({ ...base, platformStatus: "not_deployed" })).toBe(false);
        expect(shouldUpgradeToDeployed({ ...base, platformStatus: undefined })).toBe(false);
    });
});

describe("which platform record the self-heal reads", () => {
    const byId = new Map([["id-own", "deployed"]]);
    const byName = new Map([
        ["Poet", "deployed"],
        ["poet", "deployed"],
    ]);

    test("a component with a recorded id is judged by that record, never by a same-named one", () => {
        expect(ownPlatformStatus({ id: "id-own" }, byId)).toBe("deployed");
        expect(ownPlatformStatus({ id: "id-gone" }, byId)).toBeUndefined();
        expect(platformStatusOfComponent({ id: "id-gone", name: "Poet" }, byId, byName)).toBeUndefined();
    });

    test("a component with no recorded id has no record of its own to ask about for the upgrade", () => {
        expect(ownPlatformStatus({}, byId)).toBeUndefined();
    });

    test("a legacy component with no id keeps being judged by name for the downgrade, as before", () => {
        expect(platformStatusOfComponent({ name: "Poet" }, byId, byName)).toBe("deployed");
        expect(platformStatusOfComponent({ name: "P-o-e-t" }, byId, byName)).toBe("deployed");
        expect(platformStatusOfComponent({ name: "Other" }, byId, byName)).toBeUndefined();
    });

    test("the live mesh shields a component by its own instance; a legacy one with no id by name as before", () => {
        const own = [{ name: "agent_01a160d0_2474_7426_830a_ae0397fcdc9a" }];
        expect(liveShieldsFromDowngrade({ id: "01a160d0-2474-7426-830a-ae0397fcdc9a", name: "Poet", type: "agent" }, own)).toBe(true);
        expect(liveShieldsFromDowngrade({ id: "01a160ff-0000-7000-8000-000000000001", name: "Poet", type: "agent" }, [{ name: "agent_other", displayName: "Poet" }])).toBe(false);
        expect(liveShieldsFromDowngrade({ name: "Poet", type: "agent" }, [{ name: "agent_other", displayName: "Poet" }])).toBe(true);
    });
});
