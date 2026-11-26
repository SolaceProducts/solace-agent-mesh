// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { buildWorkflowNavigationUrl, parseWorkflowParentPath } from "./workflowNavigation";

const roundTrip = (parents: string[]) => parseWorkflowParentPath(new URL(buildWorkflowNavigationUrl("child", parents), "http://x").searchParams.get("from"));

describe("workflow parent-path round trip", () => {
    test.each([
        ["plain names", ["parent_one", "parent_two"]],
        ["a space", ["Order Flow", "Invoice Sync"]],
        ["a percent sign", ["50%off"]],
        ["the separator itself", ["a,b", "c"]],
        ["a slash and a question mark", ["a/b", "c?d"]],
        ["an ampersand", ["a&from=evil"]],
    ])("survives %s", (_label, parents) => {
        expect(roundTrip(parents)).toEqual(parents);
    });

    test("no parents means no query string at all", () => {
        expect(buildWorkflowNavigationUrl("child")).toBe("/agent-mesh/workflows/child");
        expect(parseWorkflowParentPath(null)).toEqual([]);
    });

    test("a hand-edited trail that is not valid escaping falls back to the raw segment", () => {
        expect(parseWorkflowParentPath("%")).toEqual(["%"]);
    });

    test("the workflow name itself is escaped into the path", () => {
        expect(buildWorkflowNavigationUrl("a/b")).toBe("/agent-mesh/workflows/a%2Fb");
    });
});
