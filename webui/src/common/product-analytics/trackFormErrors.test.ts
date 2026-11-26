// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { FieldErrors } from "react-hook-form";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "./client";
import { trackFormErrors } from "./trackFormErrors";

let trackSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});

afterEach(() => {
    vi.restoreAllMocks();
});

describe("trackFormErrors", () => {
    test("emits one form:validation_error per errored field with form name, field, and errorType", () => {
        const errors = {
            name: { type: "too_small" },
            values: { type: "invalid_type" },
        } as unknown as FieldErrors;

        trackFormErrors(errors, "agent");

        expect(trackSpy).toHaveBeenCalledTimes(2);
        expect(trackSpy).toHaveBeenCalledWith("form:validation_error", { form: "agent", field: "name", errorType: "too_small", path: "/" });
        expect(trackSpy).toHaveBeenCalledWith("form:validation_error", { form: "agent", field: "values", errorType: "invalid_type", path: "/" });
    });

    test("skips fields whose error entry is nil", () => {
        const errors = {
            name: undefined,
            description: { type: "too_small" },
        } as unknown as FieldErrors;

        trackFormErrors(errors, "connector");

        expect(trackSpy).toHaveBeenCalledOnce();
        expect(trackSpy).toHaveBeenCalledWith("form:validation_error", { form: "connector", field: "description", errorType: "too_small", path: "/" });
    });

    test("falls back to 'unknown' when an error carries no type", () => {
        const errors = { name: {} } as unknown as FieldErrors;

        trackFormErrors(errors, "connector");

        expect(trackSpy).toHaveBeenCalledWith("form:validation_error", { form: "connector", field: "name", errorType: "unknown", path: "/" });
    });

    test("emits nothing for a valid form", () => {
        trackFormErrors({} as FieldErrors, "entrypoint");

        expect(trackSpy).not.toHaveBeenCalled();
    });

    test("names the nested field a connector or entrypoint failure is actually on", () => {
        const errors = {
            values: { auth_oauth_client_secret: { type: "too_small" } },
        } as unknown as FieldErrors;

        trackFormErrors(errors, "connector");

        expect(trackSpy).toHaveBeenCalledOnce();
        const [, properties] = trackSpy.mock.calls[0] as [string, Record<string, unknown>];
        expect(properties).toEqual({ form: "connector", field: "values.auth_oauth_client_secret", errorType: "too_small", path: "/" });
    });

    test("truncates below two segments, so an array index and an operator's own key are never sent", () => {
        const errors = {
            values: { operations: [{ tool_name: { type: "custom", message: "Tool names must be unique" } }] },
        } as unknown as FieldErrors;

        trackFormErrors(errors, "connector");

        expect(trackSpy).toHaveBeenCalledOnce();
        const [, properties] = trackSpy.mock.calls[0] as [string, Record<string, unknown>];
        expect(properties).toEqual({ form: "connector", field: "values.operations", errorType: "custom", path: "/" });
    });

    test("falls back to the container for a path the vocabulary does not know", () => {
        const errors = {
            values: { acme_internal_ledger_token: { type: "too_small" } },
        } as unknown as FieldErrors;

        trackFormErrors(errors, "connector");

        const [, properties] = trackSpy.mock.calls[0] as [string, Record<string, unknown>];
        expect(properties).toEqual({ form: "connector", field: "values", errorType: "too_small", path: "/" });
    });

    test("reports one event per distinct field and type rather than one per bad leaf", () => {
        const errors = {
            values: {
                operations: [{ tool_name: { type: "custom" } }, { tool_name: { type: "custom" } }],
                base_url: { type: "invalid_format" },
            },
        } as unknown as FieldErrors;

        trackFormErrors(errors, "connector");

        const fields = (trackSpy.mock.calls as unknown[][]).map(call => (call[1] as Record<string, unknown>).field);
        expect(fields.sort()).toEqual(["values.base_url", "values.operations"]);
    });

    test("reports the agent editor's own field names", () => {
        const errors = { systemPrompt: { type: "too_small" }, selectedModel: { type: "too_small" } } as unknown as FieldErrors;

        trackFormErrors(errors, "agent");

        const fields = (trackSpy.mock.calls as unknown[][]).map(call => (call[1] as Record<string, unknown>).field);
        expect(fields.sort()).toEqual(["selectedModel", "systemPrompt"]);
    });

    test("never forwards the error message or any field value", () => {
        const errors = {
            values: { type: "custom", message: "must be at least 8 characters", ref: { value: "hunter2" } },
        } as unknown as FieldErrors;

        trackFormErrors(errors, "entrypoint");

        const [, properties] = trackSpy.mock.calls[0] as [string, Record<string, unknown>];
        expect(properties).toEqual({ form: "entrypoint", field: "values", errorType: "custom", path: "/" });
        expect(JSON.stringify(properties)).not.toContain("hunter2");
        expect(JSON.stringify(properties)).not.toContain("at least 8");
    });

    test("does not walk into a nested field's ref, so a DOM value cannot become a path segment", () => {
        const errors = {
            values: { smtp_password: { type: "too_small", ref: { name: "values.smtp_password", value: "hunter2" } } },
        } as unknown as FieldErrors;

        trackFormErrors(errors, "entrypoint");

        expect(trackSpy).toHaveBeenCalledOnce();
        const [, properties] = trackSpy.mock.calls[0] as [string, Record<string, unknown>];
        expect(properties).toEqual({ form: "entrypoint", field: "values.smtp_password", errorType: "too_small", path: "/" });
    });
});
