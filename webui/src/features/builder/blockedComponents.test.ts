// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { BLOCKED_REASON_FALLBACK, blockedSessionComponents } from "./blockedComponents";
import { isBlockedSessionComponent, isBuildCompleteCtaShowing, isBuildReadyToTest } from "./builderUtils";
import { BuildManifestSchema } from "./hooks/useBuilderManifest";

const REASON = "the http(s) URL of the Inventory API's OpenAPI document";

const connector = { name: "inventory_api", type: "connector", filename: "inventory_api.json", origin: "session", status: "blocked", blocked_reason: REASON };
const checker = { name: "InventoryChecker", type: "agent", filename: "checker.yaml", origin: "session", status: "deployed", deployed_version: 0, connectors: ["inventory_api"], depends_on: ["inventory_api"] };
const intake = { name: "OrderIntake", type: "agent", filename: "intake.yaml", origin: "session", status: "deployed", deployed_version: 0, connectors: [] as string[] };
const workflow = { name: "OrderFulfillment", type: "workflow", filename: "wf.yaml", origin: "session", status: "deployed", deployed_version: 0, depends_on: ["InventoryChecker", "OrderIntake"], nodes: [{ agent_name: "InventoryChecker" }] };
const entrypoint = { name: "OrdersEntrypoint", type: "gateway", filename: "gw.yaml", origin: "session", status: "deployed", deployed_version: 0, depends_on: ["OrderFulfillment"] };

const edits = {
    versions: new Map([
        ["intake.yaml", 0],
        ["checker.yaml", 0],
        ["wf.yaml", 0],
        ["gw.yaml", 0],
    ]),
    serverApplyEnabled: false,
};

describe("blockedSessionComponents", () => {
    test("names what the user must provide and what is held back, through a workflow and its entrypoint", () => {
        const [blocked, ...rest] = blockedSessionComponents([connector, intake, checker, workflow, entrypoint]);
        expect(rest).toEqual([]);
        expect(blocked.name).toBe("inventory_api");
        expect(blocked.reason).toBe(REASON);
        expect(blocked.heldBack.toSorted()).toEqual(["InventoryChecker", "OrderFulfillment", "OrdersEntrypoint"]);
    });

    test("the held-back set does not depend on the order the components are listed in", () => {
        const [blocked] = blockedSessionComponents([entrypoint, workflow, checker, intake, connector]);
        expect(blocked.heldBack.toSorted()).toEqual(["InventoryChecker", "OrderFulfillment", "OrdersEntrypoint"]);
    });

    test("a workflow whose tool node binds the connector uses it", () => {
        const stockFlow = { name: "StockFlow", type: "workflow", filename: "stock.yaml", origin: "session", nodes: [{ connector: "inventory_api" }] };
        expect(blockedSessionComponents([stockFlow, connector])[0].heldBack).toEqual(["StockFlow"]);
    });

    test("a component that does not use the blocked one is not held back", () => {
        const [blocked] = blockedSessionComponents([connector, intake, checker]);
        expect(blocked.heldBack).not.toContain("OrderIntake");
    });

    test("names match across casing and punctuation, the way the deploy side matches them", () => {
        const [blocked] = blockedSessionComponents([connector, { ...checker, connectors: ["Inventory Api"], depends_on: [] }]);
        expect(blocked.heldBack).toEqual(["InventoryChecker"]);
    });

    test("a platform component never counts as blocked or held back", () => {
        const existing = { ...checker, origin: "platform", filename: "" };
        expect(blockedSessionComponents([{ ...connector, origin: "platform" }])).toEqual([]);
        expect(blockedSessionComponents([connector, existing])[0].heldBack).toEqual([]);
    });

    test("a reason missing or blank still says something, and a trailing full stop is not doubled by the card", () => {
        expect(blockedSessionComponents([{ ...connector, blocked_reason: undefined }])[0].reason).toBe(BLOCKED_REASON_FALLBACK);
        expect(blockedSessionComponents([{ ...connector, blocked_reason: "   " }])[0].reason).toBe(BLOCKED_REASON_FALLBACK);
        expect(blockedSessionComponents([{ ...connector, blocked_reason: "the spec URL." }])[0].reason).toBe("the spec URL");
    });

    test("a build with nothing blocked reads empty", () => {
        expect(blockedSessionComponents([intake, checker])).toEqual([]);
        expect(blockedSessionComponents(undefined)).toEqual([]);
    });
});

describe("a blocked component keeps the build from reading as finished", () => {
    const build = [connector, intake, checker, workflow, entrypoint];

    test("every deployable component deployed is still not ready to test while one is blocked", () => {
        expect(
            isBuildReadyToTest(
                build.filter(c => c.name !== "inventory_api"),
                edits
            )
        ).toBe(true);
        expect(isBuildReadyToTest(build, edits)).toBe(false);
    });

    test("the blocked card owns the slot where the plan card would offer Build & Activate", () => {
        expect(isBuildCompleteCtaShowing(build, edits)).toBe(true);
        expect(isBuildCompleteCtaShowing([connector, { ...checker, status: "validated" }], edits)).toBe(true);
    });

    test("only a session component can be blocked", () => {
        expect(isBlockedSessionComponent({ origin: "session", status: "blocked" })).toBe(true);
        expect(isBlockedSessionComponent({ status: "blocked" })).toBe(true);
        expect(isBlockedSessionComponent({ origin: "platform", status: "blocked" })).toBe(false);
        expect(isBlockedSessionComponent({ origin: "session", status: "validated" })).toBe(false);
    });
});

describe("the manifest schema keeps a valid text value and drops only a wrong-typed one", () => {
    const parsed = () =>
        BuildManifestSchema.parse({
            name: "b",
            components: [
                { filename: "c.json", type: "connector", name: "inventory_api", status: "blocked", blocked_reason: "the spec URL" },
                { filename: "wf.yaml", type: "workflow", name: "StockFlow", nodes: [{ id: "fetch", type: "tool", connector: "inventory_api" }] },
            ],
        });

    test("a text blocked_reason and a text tool-node connector come through the parse unchanged", () => {
        const components = parsed().components!;
        expect(components[0].blocked_reason).toBe("the spec URL");
        expect(components[1].nodes![0].connector).toBe("inventory_api");
    });

    test("the card's reason and the held-back set come from them", () => {
        const [blocked] = blockedSessionComponents(parsed().components);
        expect(blocked.reason).toBe("the spec URL");
        expect(blocked.heldBack).toEqual(["StockFlow"]);
    });
});

describe("the manifest schema and blocked_reason", () => {
    const manifestWith = (reason: unknown) => ({ name: "b", components: [{ filename: "c.json", type: "connector", name: "c", status: "validated", blocked_reason: reason }] });

    test.each([undefined, null, "the spec URL", 42, ["a", "b"], { a: 1 }])("a manifest whose blocked_reason is %s is readable", reason => {
        expect(BuildManifestSchema.safeParse(manifestWith(reason)).success).toBe(true);
    });
});

describe("what the held-back reading does with odd entries in the lists it reads", () => {
    const oddEntries: [string, unknown][] = [
        ["null (a bare list dash)", null],
        ["a number", 42],
        ["a map with a non-text name", { name: 42 }],
        ["a map with a text name", { name: "inventory_api" }],
        ["an empty map", {}],
        ["a nested list", ["inventory_api"]],
        ["an empty string", ""],
    ];
    const user = (field: "skills" | "connectors" | "depends_on", entries: unknown[]) => ({ name: "StockFlow", type: "agent", filename: "s.yaml", origin: "session", [field]: entries });

    describe.each(["skills", "connectors", "depends_on"] as const)("%s", field => {
        test.each(oddEntries)("an entry that is %s is skipped without throwing", (_label, entry) => {
            expect(blockedSessionComponents([connector, user(field, [entry])])[0].heldBack).toEqual([]);
        });
        test("a text entry naming the connector holds the component back", () => {
            expect(blockedSessionComponents([connector, user(field, [null, "inventory_api"])])[0].heldBack).toEqual(["StockFlow"]);
        });
    });

    test.each([
        ["not a list", "inventory_api"],
        ["a list of null and numbers", [null, 42]],
        ["a node that is a list", [["inventory_api"]]],
        ["a node whose names are not text", [{ agent_name: 42, workflow_name: {}, connector: ["inventory_api"] }]],
    ])("workflow nodes that are %s are skipped without throwing", (_label, nodes) => {
        const workflowWith = { name: "StockFlow", type: "workflow", filename: "s.yaml", origin: "session", nodes };
        expect(blockedSessionComponents([connector, workflowWith])[0].heldBack).toEqual([]);
    });
});
