// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { Workflow } from "@/types/workflow";

import type { AgentCardInfo } from "@/types";

import { resolvePlatformWorkflow, resolveWorkflowByName } from "./resolveWorkflow";

const wf = (over: Partial<Workflow>): Workflow => ({
    id: "019f512b-c85e-76b2-bfed-7d01122848b3",
    name: "DocumentProcessor",
    displayName: "DocumentProcessor",
    description: "",
    appConfig: {},
    deploymentStatus: "deployed",
    createdBy: "u",
    updatedBy: "u",
    createdTime: 0,
    updatedTime: 0,
    ...over,
});
const card = (over: Partial<AgentCardInfo>): AgentCardInfo => ({ name: "", displayName: "", ...over }) as unknown as AgentCardInfo;

describe("resolvePlatformWorkflow", () => {
    const workflows = [wf({})];

    test("matches the list-panel card (platform name === card.name)", () => {
        expect(resolvePlatformWorkflow(workflows, card({ name: "DocumentProcessor" }))?.id).toBe("019f512b-c85e-76b2-bfed-7d01122848b3");
    });

    test("matches the diagram-page card by wire-name → UUID even when names differ", () => {
        expect(resolvePlatformWorkflow(workflows, card({ name: "workflow_019f512b_c85e_76b2_bfed_7d01122848b3", displayName: "Some Other Label" }))?.id).toBe("019f512b-c85e-76b2-bfed-7d01122848b3");
    });

    test("an authored name that starts with workflow_ still resolves by name", () => {
        expect(resolvePlatformWorkflow([wf({ name: "workflow_intake" })], card({ name: "workflow_intake" }))?.id).toBe("019f512b-c85e-76b2-bfed-7d01122848b3");
    });

    test("a wire-name card whose id has no platform record does not fall back to its displayName", () => {
        expect(resolvePlatformWorkflow(workflows, card({ name: "workflow_deadbeef", displayName: "DocumentProcessor" }))).toBeUndefined();
    });

    test("a YAML-deployed card sharing a platform workflow's displayName does not resolve to it", () => {
        const platform = [wf({ name: "orders", displayName: "Orders" })];
        expect(resolvePlatformWorkflow(platform, card({ name: "orders-yaml", displayName: "Orders" }))).toBeUndefined();
    });
});

describe("resolveWorkflowByName", () => {
    const workflows = [wf({})];

    test("matches the authored platform name (list/detail-panel URL)", () => {
        expect(resolveWorkflowByName(workflows, "DocumentProcessor")?.id).toBe("019f512b-c85e-76b2-bfed-7d01122848b3");
    });

    test("does not match a displayName, which is not unique", () => {
        expect(resolveWorkflowByName([wf({ name: "orders", displayName: "Orders" })], "Orders")).toBeUndefined();
    });

    test("matches the deployed wire name → UUID (nested-diagram refs)", () => {
        expect(resolveWorkflowByName(workflows, "workflow_019f512b_c85e_76b2_bfed_7d01122848b3")?.id).toBe("019f512b-c85e-76b2-bfed-7d01122848b3");
    });

    test("returns undefined when nothing matches", () => {
        expect(resolveWorkflowByName(workflows, "workflow_deadbeef")).toBeUndefined();
        expect(resolveWorkflowByName(workflows, "Nope")).toBeUndefined();
    });
});
