// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { GatewayFieldSchema } from "@/types/models";

import { sortFieldsByOrder } from "./sortFieldsByOrder";

const field = (label: string, order?: number): GatewayFieldSchema => ({ label, type: "string", required: false, order }) as GatewayFieldSchema;

const keys = (fields: Record<string, GatewayFieldSchema>) => sortFieldsByOrder(fields).map(([key]) => key);

describe("sortFieldsByOrder", () => {
    test("puts fields in ascending order, whatever order the schema lists them in", () => {
        expect(keys({ app_token: field("App token", 3), bot_token: field("Bot token", 2), default_agent_name: field("Default Agent", 1) })).toEqual(["default_agent_name", "bot_token", "app_token"]);
    });

    test("orders fractional positions between whole ones", () => {
        expect(keys({ credentials: field("Credentials", 2), identity: field("Identity", 1.5), agent: field("Agent", 1) })).toEqual(["agent", "identity", "credentials"]);
    });

    test("keeps fields without an order after the ordered ones, in the schema's own order", () => {
        expect(keys({ zeta: field("Zeta"), alpha: field("Alpha"), ordered: field("Ordered", 5) })).toEqual(["ordered", "zeta", "alpha"]);
    });

    test("keeps the schema's order between fields with the same position", () => {
        expect(keys({ b: field("B", 1), a: field("A", 1) })).toEqual(["b", "a"]);
    });
});
