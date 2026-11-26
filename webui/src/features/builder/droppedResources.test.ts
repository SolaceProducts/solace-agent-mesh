// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { DroppedDeployment, DroppedResource, RemovalResult } from "@/api/builder/applyTypes";

import { deploymentPhrase, kindLabel, pageLabel, problemsOf, removableResources, targetsOf } from "./droppedResources";

const resource = (over: Partial<DroppedResource> = {}): DroppedResource => ({ kind: "agent", id: "id-1", name: "Refund Agent", createdBySession: true, deployment: "deployed", ...over });

describe("removableResources", () => {
    test("offers only what a build of this session created", () => {
        const created = resource({ id: "created" });
        const onlyUpdated = resource({ id: "updated", createdBySession: false });
        expect(removableResources([created, onlyUpdated])).toEqual([created]);
    });

    test("offers nothing when the session only updated what was dropped", () => {
        expect(removableResources([resource({ createdBySession: false })])).toEqual([]);
    });
});

describe("targetsOf", () => {
    test("names exactly the kind and id of each resource, nothing the server could take as more", () => {
        expect(targetsOf([resource({ id: "a", kind: "agent" }), resource({ id: "b", kind: "workflow", name: "Flow" })])).toEqual([
            { kind: "agent", id: "a" },
            { kind: "workflow", id: "b" },
        ]);
    });
});

describe("deploymentPhrase", () => {
    const states: DroppedDeployment[] = ["deployed", "deploy_failed", "undeploy_failed", "not_deployed"];

    test("says something different for every deployment the server reports", () => {
        expect(new Set(states.map(deploymentPhrase)).size).toBe(states.length);
    });

    test("does not call a failed undeploy not deployed", () => {
        expect(deploymentPhrase("undeploy_failed")).toMatch(/still running/);
        expect(deploymentPhrase("not_deployed")).not.toMatch(/running/);
    });
});

describe("labels", () => {
    test("name the kind and the page it is removed from by hand", () => {
        expect(kindLabel("agent")).toBe("agent");
        expect(kindLabel("workflow")).toBe("workflow");
        expect(pageLabel("agent")).toBe("Agents");
        expect(pageLabel("workflow")).toBe("Workflows");
    });
});

describe("problemsOf", () => {
    const result = (over: Partial<RemovalResult>): RemovalResult => ({ kind: "agent", id: "id-1", status: "removed", ...over });

    test("keeps a failure and a refusal, with the server's reason", () => {
        const problems = problemsOf([
            result({ id: "f", status: "failed", error: { message: "undeploy failed; see server logs for details", errorId: "e1", httpStatus: 502 } }),
            result({ id: "r", status: "refused", error: { message: "not created by this session", errorId: "e2", httpStatus: 403 } }),
        ]);
        expect(problems.get("f")).toContain("undeploy failed");
        expect(problems.get("r")).toContain("not created by this session");
    });

    test("has nothing for a resource that is gone, however it went", () => {
        expect(problemsOf([result({ status: "removed" }), result({ id: "x", status: "not_found" })]).size).toBe(0);
    });
});
