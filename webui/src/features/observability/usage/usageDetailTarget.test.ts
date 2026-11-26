// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { detailTargetFor, parseDetailTarget, serializeDetailTarget } from "./usageDetailTarget";

const A_ID = "01a10c9e-3333-7e77-be6a-3cdc8d0b9301";

describe("detailTargetFor", () => {
    test("opens a subject with an id by id", () => {
        expect(detailTargetFor("agents", { id: A_ID, bookedName: null })).toEqual({ kind: "agents", ref: { id: A_ID } });
    });

    test("opens an agent, workflow or model with no id by its booked name", () => {
        expect(detailTargetFor("models", { id: null, bookedName: "gpt-4.1-mini" })).toEqual({ kind: "models", ref: { bookedName: "gpt-4.1-mini" } });
    });

    test("opens nothing for user spend with no user record", () => {
        expect(detailTargetFor("users", { id: null, bookedName: null })).toBeNull();
    });
});

describe("the ?detail param", () => {
    test.each([
        { kind: "workflows" as const, ref: { id: A_ID } },
        { kind: "models" as const, ref: { bookedName: "openai:gpt-4.1" } },
    ])("round-trips %j, colons in a name included", target => {
        expect(parseDetailTarget(serializeDetailTarget(target))).toEqual(target);
    });

    test.each([null, "", "teams:id:1", "agents:id:", "agents:guess:1", "users:name:google-oauth2|1", "agents:id:.", "users:id:..", "workflows:id:not-a-uuid", "agents:name:"])("opens no panel for %j", raw => {
        expect(parseDetailTarget(raw)).toBeNull();
    });

    test("opens a dot-named subject by name, which a query parameter carries as it is", () => {
        expect(parseDetailTarget("models:name:.")).toEqual({ kind: "models", ref: { bookedName: "." } });
    });
});
