// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { completeOrderWorkflow, defaultAgentCard } from "@/testing/data";

import { workflowDefinitionsOf } from "./workflowDefinitions";

describe("workflowDefinitionsOf", () => {
    test("maps each workflow card's broker name to its nodes and leaves out plain agents", () => {
        const definitions = workflowDefinitionsOf([completeOrderWorkflow, defaultAgentCard]);

        expect(Object.keys(definitions)).toEqual(["CompleteOrderWorkflow"]);
        expect(definitions.CompleteOrderWorkflow.map(node => node.id)).toContain("validate_order");
    });

    test("is empty before the agent cards load", () => {
        expect(workflowDefinitionsOf(undefined)).toEqual({});
    });
});
