// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { useModelConfigs } from "@/api/models";
import type { ModelConfig } from "@/api/models/types";

import { anthropicModelConfig } from "@/testing/data/models";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ModelsView } from "./ModelsView";

vi.mock("@/api/models", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/models")>()),
    useModelConfigs: vi.fn(),
    useDeleteModel: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

const customModel = (alias: string, modelName: string) => ({ ...anthropicModelConfig, id: `${alias}-id`, alias, provider: "custom", modelName }) as unknown as ModelConfig;

beforeEach(() => {
    vi.mocked(useModelConfigs).mockReturnValue({
        data: [customModel("routed", "openai/gpt-4o"), customModel("namespaced", "meta-llama/Llama-3-70b"), customModel("gateway", "hosted_vllm/llama")],
        isLoading: false,
        error: null,
    } as unknown as ReturnType<typeof useModelConfigs>);
});

describe("the Model column", () => {
    test("shows the name without a prefix the runtime routes by", () => {
        renderWithProviders(<ModelsView />, { userContextValues: { userHasCapability: () => true } });

        expect(within(screen.getByTestId("modelRow-routed")).getByText("gpt-4o")).toBeInTheDocument();
    });

    test("shows a name whose first segment is not a prefix whole, since the segment is part of the model's id", () => {
        renderWithProviders(<ModelsView />, { userContextValues: { userHasCapability: () => true } });

        expect(within(screen.getByTestId("modelRow-namespaced")).getByText("meta-llama/Llama-3-70b")).toBeInTheDocument();
        expect(within(screen.getByTestId("modelRow-gateway")).getByText("hosted_vllm/llama")).toBeInTheDocument();
    });
});
