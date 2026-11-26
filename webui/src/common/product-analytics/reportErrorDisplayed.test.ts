// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "./client";
import { reportErrorDisplayed } from "./reportErrorDisplayed";

let trackSpy: ReturnType<typeof vi.spyOn>;

function reportedProperties(): Record<string, unknown> {
    const call = (trackSpy.mock.calls as unknown[][]).find(call => call[0] === "error:displayed");
    expect(call, "no error:displayed was reported").toBeDefined();
    return call![1] as Record<string, unknown>;
}

beforeEach(() => {
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});

afterEach(() => {
    vi.restoreAllMocks();
});

describe("reportErrorDisplayed", () => {
    test("reports the bucket, the source and the route for a failure shown as a toast", () => {
        reportErrorDisplayed('Deploy failed for "orders-agent": 401 Unauthorized', "api");

        expect(reportedProperties()).toEqual({ errorCategory: "auth_failure", errorSource: "api", page: "/" });
    });

    test("buckets a message it cannot classify rather than dropping the event", () => {
        reportErrorDisplayed("Failed to create toolset", "api");

        expect(reportedProperties()).toEqual({ errorCategory: "unknown", errorSource: "api", page: "/" });
    });

    test("separates our own refusal from a provider's, which the category alone cannot", () => {
        reportErrorDisplayed("403 Forbidden: agent:orders-agent:invoke required", "api");
        const ours = reportedProperties();

        trackSpy.mockClear();
        reportErrorDisplayed("Anthropic API error: 401 invalid x-api-key", "agent_task");
        const providers = reportedProperties();

        expect(ours).toEqual({ errorCategory: "auth_failure", errorSource: "api", page: "/" });
        expect(providers).toEqual({ errorCategory: "auth_failure", errorSource: "agent_task", page: "/" });
        expect(ours.errorCategory).toEqual(providers.errorCategory);
        expect(ours.errorSource).not.toEqual(providers.errorSource);
    });

    test("never forwards the message, so an operator name or a path cannot leave the browser", () => {
        reportErrorDisplayed("Could not save the diagnostics zip: /Users/jane/Acme Financial/prod-secrets.env is not writable", "client");

        const properties = reportedProperties();
        expect(Object.keys(properties).sort()).toEqual(["errorCategory", "errorSource", "page"]);
        expect(JSON.stringify(properties)).not.toContain("Acme");
        expect(JSON.stringify(properties)).not.toContain("prod-secrets");
        expect(JSON.stringify(properties)).not.toContain("jane");
    });

    test("reports nothing but error:displayed", () => {
        reportErrorDisplayed("Run Now failed: already running", "api");

        expect((trackSpy.mock.calls as unknown[][]).map(call => call[0])).toEqual(["error:displayed"]);
    });

    test("carries traceId when the caller has one, joining the report to the agent run", () => {
        reportErrorDisplayed("Anthropic API error: 401 invalid x-api-key", "agent_task", "trace-123");

        expect(reportedProperties()).toEqual({ errorCategory: "auth_failure", errorSource: "agent_task", page: "/", traceId: "trace-123" });
    });
});
