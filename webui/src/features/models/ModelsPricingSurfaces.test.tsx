// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { useModelConfigs } from "@/api/models";
import type { ModelConfig, ModelPricing } from "@/api/models/types";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { mockModelConfigs, typesafeDecisionModelConfig } from "@/testing/data/models";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ModelDetailsPage } from "./ModelDetailsPage";
import { ModelsView } from "./ModelsView";

expect.extend(matchers);

vi.mock("@/api/models", async importOriginal => {
    const actual = await importOriginal<typeof import("@/api/models")>();
    return {
        ...actual,
        useModelConfigs: vi.fn(),
        useDeleteModel: () => ({ mutateAsync: vi.fn(), isPending: false }),
    };
});

vi.mock("react-router-dom", async importOriginal => {
    const actual = await importOriginal<typeof import("react-router-dom")>();
    return { ...actual, useParams: () => ({ id: "priced-model" }) };
});

function modelWith(pricing: ModelPricing | null): ModelConfig {
    return { ...mockModelConfigs[0], id: "priced-model", alias: "general", pricing };
}

function seed(model: ModelConfig) {
    vi.mocked(useModelConfigs).mockReturnValue({ data: [model], isLoading: false, error: null } as unknown as ReturnType<typeof useModelConfigs>);
}

const ALL_CAPABILITIES = { userHasCapability: () => true, CAPABILITIES };

beforeEach(() => {
    vi.clearAllMocks();
});

describe("models list pricing columns", () => {
    test("shows input and output columns, with the cache rates in the input hint", () => {
        seed(modelWith({ input: "3", output: "15", cacheRead: "0.3", cacheWrite: "3.75" }));

        renderWithProviders(<ModelsView />, { userContextValues: ALL_CAPABILITIES });

        for (const header of ["Input", "Output"]) {
            expect(screen.getByRole("columnheader", { name: header })).toBeInTheDocument();
        }
        for (const header of ["Cache Read", "Cache Write"]) {
            expect(screen.queryByRole("columnheader", { name: header })).not.toBeInTheDocument();
        }
        expect(screen.getByText("Prices in USD / 1M tokens")).toBeInTheDocument();
    });

    test("shows Last Updated alongside the rates", () => {
        seed(modelWith({ input: "3", output: "15", cacheRead: null, cacheWrite: null }));

        renderWithProviders(<ModelsView />, { userContextValues: ALL_CAPABILITIES });

        expect(screen.getByRole("columnheader", { name: "Last Updated" })).toBeInTheDocument();
    });

    test("a decision model's input price carries no cache-rate hint", () => {
        seed({ ...typesafeDecisionModelConfig, pricing: { input: "2", output: "8", cacheRead: null, cacheWrite: null } });

        renderWithProviders(<ModelsView />, { userContextValues: ALL_CAPABILITIES });

        const row = screen.getByTestId(`modelRow-${typesafeDecisionModelConfig.alias}`);
        expect(within(row).getByText("$2.00")).toBeInTheDocument();
        expect(within(row).queryByText(/Cache read/)).not.toBeInTheDocument();
    });
});

describe("model details pricing section", () => {
    test("lists every rate, marking the ones that follow the input rate", () => {
        seed(modelWith({ input: "3", output: "15", cacheRead: null, cacheWrite: null }));

        renderWithProviders(<ModelDetailsPage />, { userContextValues: ALL_CAPABILITIES });

        const section = screen.getByTestId("model-pricing-details");
        expect(within(section).getByText("$15.00")).toBeInTheDocument();
        expect(within(section).getAllByText("$3.00")).toHaveLength(3);
        expect(within(section).getByRole("button", { name: "Cache Read price help" })).toBeInTheDocument();
        expect(within(section).getByRole("button", { name: "Cache Write price help" })).toBeInTheDocument();
        expect(within(section).getAllByText(/defaults to the input price/)).toHaveLength(2);
        const labels = ["Input Price", "Cache Read Price", "Cache Write Price", "Output Price"].map(label => within(section).getByText(label));
        for (const [earlier, later] of labels.slice(0, -1).map((label, index) => [label, labels[index + 1]])) {
            expect(earlier.compareDocumentPosition(later) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        }
    });

    test("a decision model lists its input and output rates and no cache rates", () => {
        seed({ ...typesafeDecisionModelConfig, id: "priced-model", pricing: { input: "2", output: "8", cacheRead: null, cacheWrite: null } });

        renderWithProviders(<ModelDetailsPage />, { userContextValues: ALL_CAPABILITIES });

        const section = screen.getByTestId("model-pricing-details");
        expect(within(section).getByText("$2.00")).toBeInTheDocument();
        expect(within(section).getByText("$8.00")).toBeInTheDocument();
        expect(within(section).queryByText("Cache Read Price")).not.toBeInTheDocument();
    });

    test("an unpriced model reads as not set rather than as free", () => {
        seed(modelWith(null));

        renderWithProviders(<ModelDetailsPage />, { userContextValues: ALL_CAPABILITIES });

        const section = screen.getByTestId("model-pricing-details");
        expect(within(section).getAllByText("Not set")).toHaveLength(4);
        expect(within(section).queryByText("$0.00")).not.toBeInTheDocument();
    });
});
