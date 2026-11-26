// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { reportBuilderAgentCreationStarted } from "./agentCreationAnalytics";
import type { BuildManifest } from "./hooks/useBuilderManifest";

type ManifestComponent = NonNullable<BuildManifest["components"]>[number];

let trackSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    sessionStorage.clear();
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});

afterEach(() => {
    vi.restoreAllMocks();
    sessionStorage.clear();
});

function comp(over: Partial<ManifestComponent>): ManifestComponent {
    return { name: "c", filename: "c.yaml", type: "agent", status: "draft", origin: "session", ...over } as ManifestComponent;
}

function startedCount(): number {
    return (trackSpy.mock.calls as unknown[][]).filter(call => call[0] === "agent:creation_started").length;
}

describe("reportBuilderAgentCreationStarted", () => {
    test("reports one start per agent the approved plan will create", () => {
        reportBuilderAgentCreationStarted([comp({ name: "Router" }), comp({ name: "Validator" })], "session-1");

        expect(startedCount()).toBe(2);
    });

    test("counts a remote agent the same way", () => {
        reportBuilderAgentCreationStarted([comp({ name: "Remote", type: "a2a_agent" })], "session-1");

        expect(startedCount()).toBe(1);
    });

    test("reports nothing for a build that creates no agent", () => {
        reportBuilderAgentCreationStarted([comp({ name: "Orders", type: "workflow" }), comp({ name: "Sql", type: "connector" }), comp({ name: "Webhook", type: "gateway" }), comp({ name: "Tools", type: "skill" })], "session-1");

        expect(startedCount()).toBe(0);
    });

    test("skips an agent the platform already holds", () => {
        reportBuilderAgentCreationStarted([comp({ name: "Existing", origin: "platform" })], "session-1");

        expect(startedCount()).toBe(0);
    });

    test("skips an agent that has already been through a save", () => {
        reportBuilderAgentCreationStarted([comp({ name: "Built", status: "deployed" })], "session-1");

        expect(startedCount()).toBe(0);
    });

    test("reports the event with no properties, so nothing names the agent", () => {
        reportBuilderAgentCreationStarted([comp({ name: "Router" })], "session-1");

        expect(trackSpy).toHaveBeenCalledWith("agent:creation_started", {});
        expect(JSON.stringify(trackSpy.mock.calls)).not.toContain("Router");
    });

    test("does not report the same agent twice for one build session", () => {
        const plan = [comp({ name: "Router" })];

        reportBuilderAgentCreationStarted(plan, "session-1");
        reportBuilderAgentCreationStarted(plan, "session-1");

        expect(startedCount()).toBe(1);
    });

    test("still reports an agent added to the plan after the first approval", () => {
        reportBuilderAgentCreationStarted([comp({ name: "Router" })], "session-1");

        reportBuilderAgentCreationStarted([comp({ name: "Router" }), comp({ name: "Summarizer" })], "session-1");

        expect(startedCount()).toBe(2);
    });

    test("reports again for a different build", () => {
        reportBuilderAgentCreationStarted([comp({ name: "Router" })], "session-1");

        reportBuilderAgentCreationStarted([comp({ name: "Router" })], "session-2");

        expect(startedCount()).toBe(2);
    });

    test("reports without a claim when there is no session to key one by", () => {
        reportBuilderAgentCreationStarted([comp({ name: "Router" })], null);

        expect(startedCount()).toBe(1);
    });

    test("reports nothing for a plan with no components", () => {
        reportBuilderAgentCreationStarted(undefined, "session-1");

        expect(startedCount()).toBe(0);
    });
});
