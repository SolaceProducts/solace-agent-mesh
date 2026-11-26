// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "@/common/product-analytics/client";
import { PRODUCT_ANALYTICS_FORM_FIELDS } from "@/common/product-analytics/formFields";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ModelEdit } from "./ModelEdit";

let trackSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});

afterEach(() => {
    vi.restoreAllMocks();
});

function submitEmptyForm() {
    const view = render(
        <StoryProvider>
            <ModelEdit isNew onSave={vi.fn()} availableProviders={[{ id: "openai", label: "OpenAI" }]} />
        </StoryProvider>
    );
    const form = view.container.querySelector("#model-form");
    expect(form).not.toBeNull();
    fireEvent.submit(form!);
    return view;
}

function validationErrors(): Array<Record<string, unknown>> {
    return (trackSpy.mock.calls as unknown[][]).filter(call => call[0] === "form:validation_error").map(call => call[1] as Record<string, unknown>);
}

describe("the model form reports what stopped a save", () => {
    test("reports one event per field the submit could not satisfy", async () => {
        submitEmptyForm();

        await waitFor(() => expect(validationErrors().length).toBeGreaterThan(0));
        expect(
            validationErrors()
                .map(properties => properties.field)
                .sort()
        ).toEqual(["alias", "description", "provider"]);
    });

    test("reports the form and the rule that failed, not the value", async () => {
        submitEmptyForm();

        await waitFor(() => expect(validationErrors().length).toBeGreaterThan(0));
        for (const properties of validationErrors()) {
            expect(properties.form).toBe("model");
            expect(properties.errorType).toBe("required");
        }
    });

    test("names only fields the vocabulary declares", async () => {
        submitEmptyForm();

        await waitFor(() => expect(validationErrors().length).toBeGreaterThan(0));
        for (const properties of validationErrors()) {
            expect(PRODUCT_ANALYTICS_FORM_FIELDS).toContain(properties.field);
        }
    });
});
