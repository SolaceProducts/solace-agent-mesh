// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { mockGatewaySchemas } from "@/testing/data/gatewaySchemas";

import { matchesConditionalDisplay, valuesWithSchemaDefaults } from "./conditionalDisplay";

describe("matchesConditionalDisplay", () => {
    test("undefined rule always matches", () => {
        expect(matchesConditionalDisplay(undefined, { mode: "auto" })).toBe(true);
        expect(matchesConditionalDisplay(undefined, {})).toBe(true);
        expect(matchesConditionalDisplay(undefined, undefined)).toBe(true);
    });

    test("empty rule object matches (every() over [] is true)", () => {
        expect(matchesConditionalDisplay({}, { mode: "auto" })).toBe(true);
    });

    test("string rule: matches on exact equality", () => {
        const rule = { auth_mode: "oauth2" };
        expect(matchesConditionalDisplay(rule, { auth_mode: "oauth2" })).toBe(true);
        expect(matchesConditionalDisplay(rule, { auth_mode: "basic" })).toBe(false);
    });

    test("string rule: missing form value does not match", () => {
        const rule = { auth_mode: "oauth2" };
        expect(matchesConditionalDisplay(rule, {})).toBe(false);
        expect(matchesConditionalDisplay(rule, undefined)).toBe(false);
    });

    test("array rule: matches when value is any of the listed entries", () => {
        const rule = { delivery_mode: ["auto", "poll"] };
        expect(matchesConditionalDisplay(rule, { delivery_mode: "auto" })).toBe(true);
        expect(matchesConditionalDisplay(rule, { delivery_mode: "poll" })).toBe(true);
    });

    test("array rule: does not match values outside the list", () => {
        const rule = { delivery_mode: ["auto", "poll"] };
        expect(matchesConditionalDisplay(rule, { delivery_mode: "idle" })).toBe(false);
    });

    test("array rule: missing form value does not match", () => {
        const rule = { delivery_mode: ["auto", "poll"] };
        expect(matchesConditionalDisplay(rule, {})).toBe(false);
    });

    test("array rule: empty list never matches", () => {
        const rule = { delivery_mode: [] };
        expect(matchesConditionalDisplay(rule, { delivery_mode: "auto" })).toBe(false);
    });

    test("multi-key rule: AND-combined (all entries must match)", () => {
        const rule = { auth_mode: "oauth2", region: "us" };
        expect(matchesConditionalDisplay(rule, { auth_mode: "oauth2", region: "us" })).toBe(true);
        expect(matchesConditionalDisplay(rule, { auth_mode: "oauth2", region: "eu" })).toBe(false);
        expect(matchesConditionalDisplay(rule, { auth_mode: "basic", region: "us" })).toBe(false);
    });

    test("multi-key rule: mix of string and array entries", () => {
        const rule = { auth_mode: "oauth2", delivery_mode: ["auto", "poll"] };
        expect(matchesConditionalDisplay(rule, { auth_mode: "oauth2", delivery_mode: "poll" })).toBe(true);
        expect(matchesConditionalDisplay(rule, { auth_mode: "oauth2", delivery_mode: "idle" })).toBe(false);
    });
});

describe("valuesWithSchemaDefaults", () => {
    const schema = {
        identity_lookup_key: { default: "phone" },
        contact_request_template: {},
    };

    test("fills a default for an absent key so its condition still matches", () => {
        const resolved = valuesWithSchemaDefaults({ contact_request_template: "request_contact_info_1" }, schema);
        expect(resolved.identity_lookup_key).toBe("phone");
        expect(matchesConditionalDisplay({ identity_lookup_key: "phone" }, resolved)).toBe(true);
    });

    test("treats an explicit blank as absent", () => {
        const resolved = valuesWithSchemaDefaults({ identity_lookup_key: "" }, schema);
        expect(resolved.identity_lookup_key).toBe("phone");
    });

    test("never overrides a value that was actually set", () => {
        const resolved = valuesWithSchemaDefaults({ identity_lookup_key: "wa_username" }, schema);
        expect(resolved.identity_lookup_key).toBe("wa_username");
        expect(matchesConditionalDisplay({ identity_lookup_key: "phone" }, resolved)).toBe(false);
    });

    test("leaves fields with no declared default alone", () => {
        const resolved = valuesWithSchemaDefaults({}, schema);
        expect("contact_request_template" in resolved).toBe(false);
    });

    test("tolerates missing values and schema", () => {
        expect(valuesWithSchemaDefaults(undefined, undefined)).toEqual({});
        expect(valuesWithSchemaDefaults(null, schema).identity_lookup_key).toBe("phone");
    });
});

describe("whatsapp sender identity modes", () => {
    const whatsapp = mockGatewaySchemas.find(s => s.type === "whatsapp")!;
    const field = (name: string) => whatsapp.schema[name];

    const shown = (values: Record<string, unknown>) =>
        Object.entries(whatsapp.schema)
            .filter(([, cfg]) => matchesConditionalDisplay(cfg.conditionalDisplay, values))
            .map(([name]) => name);

    test("the system-user mode shows run_as and none of the lookup fields", () => {
        const visible = shown(valuesWithSchemaDefaults({ sender_identity_mode: "system_user", run_as: "ops" }, whatsapp.schema));
        expect(visible).toContain("run_as");
        for (const hidden of ["identity_lookup_key", "identity_claim_key", "contact_request_template", "contact_request_template_language"]) {
            expect(visible).not.toContain(hidden);
        }
    });

    test("the lookup mode shows the lookup fields and not run_as", () => {
        const visible = shown(valuesWithSchemaDefaults({ sender_identity_mode: "idp_lookup" }, whatsapp.schema));
        expect(visible).toContain("identity_lookup_key");
        expect(visible).toContain("identity_claim_key");
        expect(visible).not.toContain("run_as");
    });

    test("a bag with no mode resolves to the lookup mode through the schema default, so live fields stay visible", () => {
        const visible = shown(valuesWithSchemaDefaults({ identity_lookup_key: "phone" }, whatsapp.schema));
        expect(visible).toContain("identity_lookup_key");
        expect(visible).toContain("contact_request_template");
    });

    test("a username lookup hides the contact-share fields even in the lookup mode", () => {
        const visible = shown(valuesWithSchemaDefaults({ sender_identity_mode: "idp_lookup", identity_lookup_key: "wa_username" }, whatsapp.schema));
        expect(visible).toContain("identity_claim_key");
        expect(visible).not.toContain("contact_request_template");
    });

    test("run_as is required only where it is shown", () => {
        expect(field("run_as").required).toBe(true);
        expect(field("run_as").clearWhenHidden).toBe(true);
        expect(field("sender_identity_mode").default).toBe("idp_lookup");
    });

    test("a stamped legacy run_as row keeps its run_as visible", () => {
        const visible = shown(valuesWithSchemaDefaults({ sender_identity_mode: "system_user", run_as: "ops" }, whatsapp.schema));
        expect(visible).toContain("run_as");
        expect(visible).not.toContain("identity_lookup_key");
    });

    test("an unstamped run_as row would lose its run_as", () => {
        const visible = shown(valuesWithSchemaDefaults({ run_as: "ops" }, whatsapp.schema));
        expect(visible).not.toContain("run_as");
    });

    test("identity_claim_key does not pre-fill a value", () => {
        expect(field("identity_claim_key").default).toBeUndefined();
    });
});
