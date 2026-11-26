// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { inferConnectorIconFromName, lookupConnectorIconByKey } from "./connectorIconInference";

describe("lookupConnectorIconByKey", () => {
    test("returns a resolved URL for known service keys", () => {
        expect(lookupConnectorIconByKey("jira")).toBeTruthy();
        expect(lookupConnectorIconByKey("postgresql")).toBeTruthy();
        expect(lookupConnectorIconByKey("aws")).toBeTruthy();
    });

    test("treats gmail and google as the same icon", () => {
        expect(lookupConnectorIconByKey("gmail")).toBe(lookupConnectorIconByKey("google"));
    });

    test("returns undefined for unknown keys", () => {
        expect(lookupConnectorIconByKey("other")).toBeUndefined();
        expect(lookupConnectorIconByKey("")).toBeUndefined();
        expect(lookupConnectorIconByKey("totally-fake")).toBeUndefined();
    });
});

describe("inferConnectorIconFromName", () => {
    test("matches snake_case component names", () => {
        expect(inferConnectorIconFromName("jira_connector")).toBe(lookupConnectorIconByKey("jira"));
    });

    test("matches kebab-case component names", () => {
        expect(inferConnectorIconFromName("slack-connector")).toBe(lookupConnectorIconByKey("slack"));
    });

    test("matches case-insensitively", () => {
        expect(inferConnectorIconFromName("GitHub Integration")).toBe(lookupConnectorIconByKey("github"));
    });

    test("matches multi-word service keys like microsoft_graph", () => {
        expect(inferConnectorIconFromName("microsoft graph connector")).toBe(lookupConnectorIconByKey("microsoft_graph"));
    });

    test("matches web search connector names to the generic web search icon", () => {
        expect(lookupConnectorIconByKey("web_search")).toBeTruthy();
        expect(inferConnectorIconFromName("web-search")).toBe(lookupConnectorIconByKey("web_search"));
        expect(inferConnectorIconFromName("team_web_search")).toBe(lookupConnectorIconByKey("web_search"));
    });

    test("returns undefined when no known service is referenced", () => {
        expect(inferConnectorIconFromName("my_custom_thing")).toBeUndefined();
        expect(inferConnectorIconFromName("")).toBeUndefined();
    });
});
