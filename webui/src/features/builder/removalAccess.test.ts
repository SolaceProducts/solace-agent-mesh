// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

const sources = import.meta.glob<string>("/src/**/*.{ts,tsx}", { query: "?raw", import: "default", eager: true });

const isTest = (path: string) => /\.(test|spec|stories)\.(ts|tsx)$/.test(path) || path.includes("/testing/");
const production = Object.entries(sources).filter(([path]) => !isTest(path));

function filesMatching(pattern: RegExp): string[] {
    return production
        .filter(([, source]) => pattern.test(source))
        .map(([path]) => path.replace("/src/", ""))
        .sort();
}

describe("who can reach a removal", () => {
    test("the route is named in one file", () => {
        expect(filesMatching(/builder\/removals/)).toEqual(["api/builder/service.ts"]);
    });

    test("the service function that deletes is called only by its mutation", () => {
        expect(filesMatching(/\bremoveDropped\(/)).toEqual(["api/builder/service.ts", "features/builder/hooks/useDroppedResources.ts"]);
        expect(filesMatching(/builderService\.removeDropped/)).toEqual(["api/builder/hooks.ts"]);
    });

    test("no file reaches the removal function under another name: the identifier appears only where it is passed down", () => {
        expect(filesMatching(/\bremoveDropped\b/)).toEqual(["api/builder/hooks.ts", "api/builder/service.ts", "features/builder/BuilderPage.tsx", "features/builder/hooks/useDroppedResources.ts"]);
    });

    test("the removal mutation is used by one hook, and that hook by one page", () => {
        expect(filesMatching(/useRemoveDroppedMutation/)).toEqual(["api/builder/hooks.ts", "features/builder/hooks/useDroppedResources.ts"]);
        expect(filesMatching(/useDroppedResources\b/)).toEqual(["features/builder/BuilderPage.tsx", "features/builder/hooks/useDroppedResources.ts"]);
    });

    test("only the confirmation dialog calls the removal it is handed", () => {
        const callers = production.filter(([, source]) => /DroppedResource/.test(source) && /\bonRemove(Dropped)?\??\.?\(/.test(source)).map(([path]) => path.replace("/src/", ""));
        expect(callers).toEqual(["features/builder/RemoveDroppedDialog.tsx"]);
    });

    test("nothing in the Builder deletes an agent or a workflow by any other route", () => {
        const builderFiles = production.filter(([path]) => path.startsWith("/src/features/builder/") && !path.endsWith("RemoveDroppedDialog.tsx"));
        const offenders = builderFiles.filter(([, source]) => /useDelete(Agent|Workflow|RemoteAgent)|delete(Agent|Workflow)\(|platform\.delete\(|webui\.delete\(/.test(source)).map(([path]) => path);
        expect(offenders.filter(path => !path.endsWith("hooks/useTestSessionLifecycle.ts"))).toEqual([]);
    });

    test("the save, deploy, retry and trigger code has no reference to removal", () => {
        const paths = ["hooks/useSaveOrchestration.tsx", "hooks/useManifestDeployment.ts", "hooks/useChatDeployTrigger.ts", "hooks/useBuildActivateBridge.ts", "BuilderActions.tsx", "EditCompleteCard.tsx"];
        for (const path of paths) {
            const source = sources[`/src/features/builder/${path}`];
            expect(source, path).toBeDefined();
            expect(source, path).not.toMatch(/removeDropped|useRemoveDropped|RemoveDropped|builder\/removals|DroppedResource/);
        }
    });

    test("the dialog reads no preference that would let the user skip it", () => {
        const dialog = sources["/src/features/builder/RemoveDroppedDialog.tsx"];
        const notice = sources["/src/features/builder/DroppedResourcesNotice.tsx"];
        for (const source of [dialog, notice]) {
            expect(source).not.toMatch(/localStorage|sessionStorage|overwriteConfirmPreference|shouldSkip|SKIP_CONFIRM|persistSkip/);
        }
    });
});
