// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import type { GatewaySchema } from "@/types";

import { GatewayEdit } from "./GatewayEdit";

function withProviders(ui: ReactNode) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return (
        <OpenFeatureTestProvider flagValueMap={{}}>
            <MemoryRouter>
                <QueryClientProvider client={client}>{ui}</QueryClientProvider>
            </MemoryRouter>
        </OpenFeatureTestProvider>
    );
}

const schemaInfo = {
    type: "webhook",
    name: "Webhook",
    description: "test",
    schema: {
        endpoint_path: { type: "string", label: "Endpoint Path", required: true, order: 1 },
    },
} as unknown as GatewaySchema;

let trackSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});

afterEach(() => {
    vi.restoreAllMocks();
});

describe("GatewayEdit — validation friction", () => {
    test("a rejected submit reports the fields that blocked it, and no value the user typed", async () => {
        const onSubmit = vi.fn();
        render(withProviders(<GatewayEdit schemaInfo={schemaInfo} isNew onSubmit={onSubmit} />));

        fireEvent.submit(document.getElementById("gateway-form")!);

        await waitFor(() => expect(trackSpy).toHaveBeenCalledWith("form:validation_error", expect.objectContaining({ form: "entrypoint" })));
        expect(onSubmit).not.toHaveBeenCalled();
        for (const [, properties] of trackSpy.mock.calls) {
            expect(Object.keys(properties as object).sort()).toEqual(["errorType", "field", "form", "path"]);
        }
    });

    test("a valid submit reports nothing", async () => {
        const onSubmit = vi.fn();
        const gateway = { name: "My hook", description: "does things", type: "webhook", values: { endpoint_path: "/hook" } } as never;
        render(withProviders(<GatewayEdit schemaInfo={schemaInfo} initialGatewayToEdit={gateway} onSubmit={onSubmit} />));

        fireEvent.submit(document.getElementById("gateway-form")!);

        await waitFor(() => expect(onSubmit).toHaveBeenCalled());
        expect(trackSpy).not.toHaveBeenCalledWith("form:validation_error", expect.anything());
    });
});
