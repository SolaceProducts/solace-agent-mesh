// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { useModelById, useModelConfigs, useSupportedModels } from "@/api/models";
import type { ModelConfig } from "@/api/models/types";

import { useFreeModel } from "@/features/activation";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ModelDetailsPage } from "./ModelDetailsPage";
import { ModelEditPage } from "./ModelEditPage";
import { ModelsView } from "./ModelsView";

vi.mock("@/features/activation", async importOriginal => ({
    ...(await importOriginal<typeof import("@/features/activation")>()),
    useFreeModel: vi.fn(),
    FreeCreditsBanner: () => <p>Free credits banner</p>,
    FreeModelManagedNotice: () => <p>Managed by Solace Agent Mesh</p>,
}));

vi.mock("@/api/models", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/models")>()),
    useModelConfigs: vi.fn(),
    useModelById: vi.fn(),
    useSupportedModels: vi.fn(() => ({ data: [], isLoading: false })),
    useDeleteModel: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

const freeGeneral = {
    id: "general-id",
    alias: "general",
    provider: "openai",
    modelName: "solace-free",
    apiBase: "https://llm.example.test/v1",
    authType: "oauth2_delegated",
    authConfig: { type: "oauth2_delegated", grant_id: "grant-1" },
    modelParams: {},
    pricing: { input: null, output: null, cacheRead: null, cacheWrite: null },
    modelType: "chat",
} as unknown as ModelConfig;
const ownModel = { ...freeGeneral, id: "mine-id", alias: "mine", provider: "anthropic", modelName: "claude", authType: "apikey", authConfig: { type: "apikey" } } as unknown as ModelConfig;

const allCapabilities = { userHasCapability: () => true };

beforeEach(() => {
    vi.mocked(useFreeModel).mockReturnValue({
        chosen: true,
        inUse: true,
        known: true,
        isFreeModel: model => model.id === freeGeneral.id,
        freeModelLabel: model => (model.id === freeGeneral.id ? { modelName: "Sonnet 5.5", provider: "anthropic", providerName: "Anthropic" } : undefined),
    });
    vi.mocked(useModelConfigs).mockReturnValue({ data: [freeGeneral, ownModel], isLoading: false, error: null } as unknown as ReturnType<typeof useModelConfigs>);
    vi.mocked(useModelById).mockReturnValue({ data: freeGeneral, isLoading: false, error: null } as unknown as ReturnType<typeof useModelById>);
});

describe("the models list", () => {
    test("shows the free credits, and the free model as Anthropic's Sonnet 5.5 without its stored provider", () => {
        renderWithProviders(<ModelsView />, { userContextValues: allCapabilities });

        expect(screen.getByText("Free credits banner")).toBeInTheDocument();
        const row = screen.getByTestId("modelRow-general");
        expect(within(row).getByText("Anthropic")).toBeInTheDocument();
        expect(within(row).getByText("Sonnet 5.5")).toBeInTheDocument();
        expect(row).toHaveTextContent("Powered byClaude");
        expect(row).not.toHaveTextContent("OpenAI");
        expect(row).not.toHaveTextContent("solace-free");
    });

    test("offers no edit or delete for the free model, only its details", async () => {
        renderWithProviders(<ModelsView />, { userContextValues: allCapabilities });

        const [freeRowMenu] = screen.getAllByRole("button", { name: "Actions" });
        await userEvent.click(freeRowMenu);

        expect(screen.getByRole("menuitem", { name: "Open Details" })).toBeInTheDocument();
        expect(screen.queryByRole("menuitem", { name: "Edit" })).not.toBeInTheDocument();
        expect(screen.queryByRole("menuitem", { name: "Delete" })).not.toBeInTheDocument();
    });

    test("still offers edit and delete for the person's own models", async () => {
        renderWithProviders(<ModelsView />, { userContextValues: allCapabilities });

        await userEvent.click(screen.getAllByRole("button", { name: "Actions" })[1]);

        expect(screen.getByRole("menuitem", { name: "Edit" })).toBeInTheDocument();
        expect(screen.getByRole("menuitem", { name: "Delete" })).toBeInTheDocument();
    });
});

describe("the free model's details", () => {
    test("show its credits, and offer no edit or delete", () => {
        renderWithProviders(
            <Routes>
                <Route path="/models/:id" element={<ModelDetailsPage />} />
            </Routes>,
            { initialPath: "/models/general-id", userContextValues: allCapabilities }
        );

        expect(screen.getByText("Free credits banner")).toBeInTheDocument();
        expect(screen.getByText("Anthropic")).toBeInTheDocument();
        expect(screen.getByText("Sonnet 5.5")).toBeInTheDocument();
        expect(screen.getByText("Claude")).toBeInTheDocument();
        expect(screen.queryByText("solace-free")).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
        expect(screen.queryByTestId("modelMoreButton")).not.toBeInTheDocument();
    });

    test("show no model prefix for the free model, whatever provider and name it is stored with", () => {
        const freeAsCustom = { ...freeGeneral, provider: "custom", modelName: "openai/solace-free" } as unknown as ModelConfig;
        vi.mocked(useModelConfigs).mockReturnValue({ data: [freeAsCustom, ownModel], isLoading: false, error: null } as unknown as ReturnType<typeof useModelConfigs>);
        renderWithProviders(
            <Routes>
                <Route path="/models/:id" element={<ModelDetailsPage />} />
            </Routes>,
            { initialPath: "/models/general-id", userContextValues: allCapabilities }
        );

        expect(screen.getByText("Sonnet 5.5")).toBeInTheDocument();
        expect(screen.queryByText("Model Prefix")).not.toBeInTheDocument();
    });

    test("show the model prefix of the person's own custom model", () => {
        const ownCustom = { ...ownModel, provider: "custom", modelName: "openai/my-model" } as unknown as ModelConfig;
        vi.mocked(useModelConfigs).mockReturnValue({ data: [freeGeneral, ownCustom], isLoading: false, error: null } as unknown as ReturnType<typeof useModelConfigs>);
        renderWithProviders(
            <Routes>
                <Route path="/models/:id" element={<ModelDetailsPage />} />
            </Routes>,
            { initialPath: "/models/mine-id", userContextValues: allCapabilities }
        );

        expect(screen.getByText("Model Prefix")).toBeInTheDocument();
        expect(screen.getByText("openai/")).toBeInTheDocument();
    });

    test("of another model keep their edit", () => {
        renderWithProviders(
            <Routes>
                <Route path="/models/:id" element={<ModelDetailsPage />} />
            </Routes>,
            { initialPath: "/models/mine-id", userContextValues: allCapabilities }
        );

        expect(screen.queryByText("Free credits banner")).not.toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
    });
});

test("the free model's edit page, reached by URL, shows who manages it instead of the form, and probes nothing", async () => {
    renderWithProviders(
        <Routes>
            <Route path="/models/:id/edit" element={<ModelEditPage />} />
            <Route path="/models" element={<p>Models list</p>} />
        </Routes>,
        { initialPath: "/models/general-id/edit", userContextValues: allCapabilities }
    );

    expect(screen.getByText("Managed by Solace Agent Mesh")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save Model" })).not.toBeInTheDocument();
    expect(vi.mocked(useSupportedModels).mock.calls.every(([request]) => request === null)).toBe(true);

    await userEvent.click(screen.getByRole("button", { name: "Back to Models" }));
    expect(screen.getByText("Models list")).toBeInTheDocument();
});
