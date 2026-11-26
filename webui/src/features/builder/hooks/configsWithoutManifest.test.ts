// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { configsWithoutManifest, finishBuildRequest, manifestWarning, rewriteManifestRequest } from "./configsWithoutManifest";
import type { BuildManifest } from "./useBuilderManifest";

const aManifest = { name: "build", components: [] } as unknown as BuildManifest;

const stuck = { configCount: 1, manifest: null, manifestSettled: true, manifestUnreadable: false, isResponding: false };

describe("configsWithoutManifest", () => {
    test("reports the silent direction: configs written, no manifest", () => {
        expect(configsWithoutManifest(stuck)).toBe(true);
    });

    test("stays quiet until the manifest lookup has settled", () => {
        expect(configsWithoutManifest({ ...stuck, manifestSettled: false })).toBe(false);
    });

    test("stays quiet when the manifest was read but would not parse", () => {
        expect(configsWithoutManifest({ ...stuck, manifestUnreadable: true })).toBe(false);
    });

    test("stays quiet while the agent is mid-turn", () => {
        expect(configsWithoutManifest({ ...stuck, isResponding: true })).toBe(false);
    });

    test("stays quiet on a session with a manifest", () => {
        expect(configsWithoutManifest({ ...stuck, manifest: aManifest })).toBe(false);
    });

    test("stays quiet on a session with nothing in it", () => {
        expect(configsWithoutManifest({ ...stuck, configCount: 0 })).toBe(false);
    });
});

describe("finishBuildRequest", () => {
    test("names the artifact the save actually looks for, in both roles", () => {
        const msg = finishBuildRequest(2);
        expect(msg).toMatch(/no `build_manifest\.yaml`/);
        expect(msg).toMatch(/Write `build_manifest\.yaml`/);
    });

    test("counts the configs it can see", () => {
        expect(finishBuildRequest(1)).toContain("1 component config but");
        expect(finishBuildRequest(3)).toContain("3 component configs but");
    });
});

describe("manifestWarning", () => {
    test("an absent manifest says the canvas is empty and the build cannot be saved", () => {
        expect(manifestWarning(false, false)).toContain("No build manifest");
        expect(manifestWarning(false, false)).toContain("stays empty");
    });

    test("an unreadable first manifest says the same, because it is also true there", () => {
        expect(manifestWarning(true, false)).toContain("couldn't be read");
        expect(manifestWarning(true, false)).toContain("stays empty");
    });

    test("an unreadable later manifest names staleness, not absence", () => {
        const msg = manifestWarning(true, true);
        expect(msg).toContain("couldn't be read");
        expect(msg).not.toContain("stays empty");
        expect(msg).not.toContain("can't be saved");
        expect(msg).toMatch(/last version|may be missing/);
    });
});

describe("rewriteManifestRequest", () => {
    test("does not claim an empty canvas when a manifest is on screen", () => {
        const msg = rewriteManifestRequest(true);
        expect(msg).not.toContain("canvas is empty");
        expect(msg).toContain("older version");
        expect(msg).toContain("repair the invalid YAML");
    });

    test("still names the empty canvas when nothing is on screen", () => {
        expect(rewriteManifestRequest(false)).toContain("canvas is empty");
    });
});
