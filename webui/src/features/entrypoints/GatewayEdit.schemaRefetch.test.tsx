// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import type { GatewaySchema } from "@/types";

import { GatewayEdit } from "./GatewayEdit";

const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

function withProviders(ui: ReactNode) {
    return (
        <OpenFeatureTestProvider flagValueMap={{}}>
            <MemoryRouter>
                <QueryClientProvider client={client}>{ui}</QueryClientProvider>
            </MemoryRouter>
        </OpenFeatureTestProvider>
    );
}

function webhookSchema(): GatewaySchema {
    return {
        type: "webhook",
        name: "Webhook",
        description: "test",
        schema: {
            endpoint_path: { type: "string", label: "Endpoint Path", required: true, order: 1 },
        },
    } as unknown as GatewaySchema;
}

describe("GatewayEdit — schema refetch", () => {
    test("a create form keeps the typed name when the schemas are refetched", () => {
        const { container, rerender } = render(withProviders(<GatewayEdit schemaInfo={webhookSchema()} isNew onSubmit={vi.fn()} />));
        const nameInput = container.querySelector<HTMLInputElement>('input[name="name"]')!;

        fireEvent.change(nameInput, { target: { value: "My hook" } });
        rerender(withProviders(<GatewayEdit schemaInfo={webhookSchema()} isNew onSubmit={vi.fn()} />));

        expect(screen.getByDisplayValue("My hook")).toBe(container.querySelector('input[name="name"]'));
    });
});
