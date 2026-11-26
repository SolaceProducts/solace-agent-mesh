// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "@/common/product-analytics/client";
import { publishRoutePattern } from "@/common/product-analytics/routePattern";

import { ErrorDialog } from "./ErrorDialog";

let trackSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
    publishRoutePattern("/connectors");
});

afterEach(() => {
    vi.restoreAllMocks();
    publishRoutePattern("/");
});

function renderDialog(props: Partial<React.ComponentProps<typeof ErrorDialog>> = {}) {
    return render(<ErrorDialog title="Something Failed" error="boom" open onOpenChange={() => {}} {...props} />);
}

describe("ErrorDialog reports the errors the app shows", () => {
    test("reports the bucket, the source and the page when it opens", () => {
        renderDialog({ error: "Request failed with status 429", errorSource: "api" });

        expect(trackSpy).toHaveBeenCalledWith("error:displayed", { errorCategory: "rate_limit", errorSource: "api", page: "/connectors" });
    });

    test("leaves the source out of an opening that does not state one", () => {
        renderDialog({ error: "403 Forbidden: agent:orders-agent:invoke required" });

        const [, properties] = trackSpy.mock.calls[0] as [string, Record<string, unknown>];
        expect(properties).not.toHaveProperty("errorSource");
        expect(properties).toEqual({ errorCategory: "auth_failure", page: "/connectors" });
    });

    test("reports the source the opening states", () => {
        renderDialog({ error: "Connection lost. Please try again.", errorSource: "client" });

        expect(trackSpy).toHaveBeenCalledWith("error:displayed", { errorCategory: "unknown", errorSource: "client", page: "/connectors" });
    });

    test("never forwards the error message or the details", () => {
        renderDialog({ error: "Could not reach https://acme.example.com for user jane@acme.com", errorDetails: "token abc123 rejected" });

        const [, properties] = trackSpy.mock.calls[0] as [string, Record<string, unknown>];
        const serialized = JSON.stringify(properties);
        expect(serialized).not.toContain("acme.example.com");
        expect(serialized).not.toContain("jane@acme.com");
        expect(serialized).not.toContain("abc123");
    });

    test("classifies from the details when the message alone is generic", () => {
        renderDialog({ error: "Save failed", errorDetails: "This model's maximum context length is 8192 tokens" });

        expect(trackSpy).toHaveBeenCalledWith("error:displayed", expect.objectContaining({ errorCategory: "context_overflow" }));
    });

    test("falls back to unknown rather than inventing a bucket", () => {
        renderDialog({ error: "Agent Deletion Failed" });

        expect(trackSpy).toHaveBeenCalledWith("error:displayed", expect.objectContaining({ errorCategory: "unknown" }));
    });

    test("a ReactNode message is still reported, as unknown", () => {
        renderDialog({ error: <span>rendered elsewhere</span> });

        expect(trackSpy).toHaveBeenCalledWith("error:displayed", expect.objectContaining({ errorCategory: "unknown" }));
    });

    test("reports nothing while closed, and once when it opens", () => {
        const { rerender } = render(<ErrorDialog title="t" error="boom" open={false} onOpenChange={() => {}} />);
        expect(trackSpy).not.toHaveBeenCalled();

        rerender(<ErrorDialog title="t" error="boom" open onOpenChange={() => {}} />);
        expect(trackSpy).toHaveBeenCalledTimes(1);

        rerender(<ErrorDialog title="t" error="boom still" open onOpenChange={() => {}} />);
        expect(trackSpy).toHaveBeenCalledTimes(1);
    });

    test("reports again once the dialog is dismissed and a new error opens it", () => {
        const { rerender } = render(<ErrorDialog title="t" error="boom" open onOpenChange={() => {}} />);
        rerender(<ErrorDialog title="t" error="boom" open={false} onOpenChange={() => {}} />);
        rerender(<ErrorDialog title="t" error="429 rate limit" open onOpenChange={() => {}} />);

        expect(trackSpy).toHaveBeenCalledTimes(2);
        expect(trackSpy).toHaveBeenLastCalledWith("error:displayed", expect.objectContaining({ errorCategory: "rate_limit" }));
    });

    test("names the task when the opening is scoped to one", () => {
        renderDialog({ error: "Connection lost. Please try again.", traceId: "0199b2c1-0000-7000-8000-000000000003" });

        expect(trackSpy).toHaveBeenCalledWith("error:displayed", expect.objectContaining({ traceId: "0199b2c1-0000-7000-8000-000000000003" }));
    });

    test("names no task for the two dozen openings that have none", () => {
        renderDialog({ error: "Agent Deployment Error", errorSource: "api" });

        const [, properties] = trackSpy.mock.calls[0] as [string, Record<string, unknown>];
        expect(properties).not.toHaveProperty("traceId");
        expect(Object.keys(properties).sort()).toEqual(["errorCategory", "errorSource", "page"]);
    });

    test("reports the route pattern the router published, never a concrete id", () => {
        publishRoutePattern("/agent-mesh/:id");

        renderDialog();

        expect(trackSpy).toHaveBeenCalledWith("error:displayed", expect.objectContaining({ page: "/agent-mesh/:id" }));
    });
});
