// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import * as modelsHooks from "@/api/models/hooks";

import { ConfigContext, type ConfigContextValue } from "@/providers/contexts/ConfigContext";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { ModelSetupDialog } from "./ModelSetupDialog";

const LAZY_FORM_TIMEOUT = 5000;

interface RenderProps {
    open: boolean;
    hasWritePermissions: boolean;
    onOpenChange?: (open: boolean) => void;
    onSuccess?: () => void;
}

function renderDialog(props: RenderProps) {
    const onOpenChange = props.onOpenChange ?? vi.fn();
    const onSuccess = props.onSuccess ?? vi.fn();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return {
        onOpenChange,
        onSuccess,
        ...render(
            <QueryClientProvider client={client}>
                <ConfigContext.Provider value={{ azureWorkloadIdentityEnabled: false } as ConfigContextValue}>
                    <ModelSetupDialog open={props.open} onOpenChange={onOpenChange} hasWritePermissions={props.hasWritePermissions} onSuccess={onSuccess} />
                </ConfigContext.Provider>
            </QueryClientProvider>
        ),
    };
}

function mockCreate(behavior: (input: { alias: string }) => Promise<{ id: string }> | { id: string }) {
    const mutateAsync = vi.fn(async (input: { alias: string }) => behavior(input));
    vi.spyOn(modelsHooks, "useCreateModel").mockReturnValue({ mutateAsync, isPending: false } as unknown as ReturnType<typeof modelsHooks.useCreateModel>);
    return mutateAsync;
}

function mockModelConfigs(list: Array<{ id: string; alias: string }> = []) {
    vi.spyOn(modelsHooks, "useModelConfigs").mockReturnValue({ data: list, isLoading: false, isError: false } as unknown as ReturnType<typeof modelsHooks.useModelConfigs>);
}

beforeEach(() => {
    mockModelConfigs();
    vi.spyOn(modelsHooks, "useUpdateModel").mockReturnValue({ mutateAsync: vi.fn(), isPending: false } as unknown as ReturnType<typeof modelsHooks.useUpdateModel>);
});

afterEach(() => {
    vi.restoreAllMocks();
});

describe("ModelSetupDialog", () => {
    describe("with write permissions (admin)", () => {
        test("renders admin title, provider field, and footer buttons", async () => {
            mockCreate(({ alias }) => ({ id: `id-${alias}` }));
            renderDialog({ open: true, hasWritePermissions: true });

            expect(screen.getByText("Configure Your AI Models")).toBeInTheDocument();
            expect(await screen.findByText("Model Provider", undefined, { timeout: LAZY_FORM_TIMEOUT })).toBeInTheDocument();
            expect(screen.getByRole("button", { name: /Set Up Default Models/i })).toBeInTheDocument();
            expect(screen.getByRole("button", { name: /^Cancel$/i })).toBeInTheDocument();
        });

        test("hides the model-name fields until a provider is selected", () => {
            mockCreate(({ alias }) => ({ id: `id-${alias}` }));
            renderDialog({ open: true, hasWritePermissions: true });

            expect(screen.queryByText("Default Model")).not.toBeInTheDocument();
        });

        test("does not show the non-admin Close button", () => {
            mockCreate(({ alias }) => ({ id: `id-${alias}` }));
            renderDialog({ open: true, hasWritePermissions: true });

            expect(screen.queryByRole("button", { name: /^Close$/i })).not.toBeInTheDocument();
        });

        test("Cancel calls onOpenChange(false)", async () => {
            mockCreate(({ alias }) => ({ id: `id-${alias}` }));
            const onOpenChange = vi.fn();
            renderDialog({ open: true, hasWritePermissions: true, onOpenChange });

            await userEvent.click(await screen.findByRole("button", { name: /^Cancel$/i }, { timeout: LAZY_FORM_TIMEOUT }));

            expect(onOpenChange).toHaveBeenCalledWith(false);
        });

        test("submitting an empty form surfaces validation and creates nothing", async () => {
            const create = mockCreate(({ alias }) => ({ id: `id-${alias}` }));
            const onSuccess = vi.fn();
            renderDialog({ open: true, hasWritePermissions: true, onSuccess });

            await userEvent.click(await screen.findByRole("button", { name: /Set Up Default Models/i }, { timeout: LAZY_FORM_TIMEOUT }));

            await waitFor(() => {
                expect(screen.getByText(/Provider is required/i)).toBeInTheDocument();
            });
            expect(create).not.toHaveBeenCalled();
            expect(onSuccess).not.toHaveBeenCalled();
        });

        test("submitting an empty form reports what stopped it", async () => {
            const track = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
            mockCreate(({ alias }) => ({ id: `id-${alias}` }));
            renderDialog({ open: true, hasWritePermissions: true });

            await userEvent.click(await screen.findByRole("button", { name: /Set Up Default Models/i }, { timeout: LAZY_FORM_TIMEOUT }));

            await waitFor(() => expect(track.mock.calls.filter(call => call[0] === "form:validation_error").length).toBeGreaterThan(0));
            const reported = track.mock.calls.filter(call => call[0] === "form:validation_error").map(call => call[1] as Record<string, unknown>);
            expect(reported.map(properties => properties.field)).toEqual(["provider"]);
            expect(reported[0]).toMatchObject({ form: "model", errorType: "required" });
        });
    });

    describe("without write permissions (non-admin)", () => {
        test("renders non-admin title and Close button", () => {
            mockCreate(({ alias }) => ({ id: `id-${alias}` }));
            renderDialog({ open: true, hasWritePermissions: false });

            expect(screen.getByText("No Default LLM Models Available")).toBeInTheDocument();
            expect(screen.getByText(/Contact an administrator/)).toBeInTheDocument();
            expect(screen.getByRole("button", { name: /^Close$/i })).toBeInTheDocument();
        });

        test("does not render the admin form or CTA", () => {
            mockCreate(({ alias }) => ({ id: `id-${alias}` }));
            renderDialog({ open: true, hasWritePermissions: false });

            expect(screen.queryByRole("button", { name: /Set Up Default Models/i })).not.toBeInTheDocument();
            expect(screen.queryByText("Default Model")).not.toBeInTheDocument();
            expect(screen.queryByText("Model Provider")).not.toBeInTheDocument();
        });

        test("Close calls onOpenChange(false)", async () => {
            mockCreate(({ alias }) => ({ id: `id-${alias}` }));
            const onOpenChange = vi.fn();
            renderDialog({ open: true, hasWritePermissions: false, onOpenChange });

            await userEvent.click(screen.getByRole("button", { name: /^Close$/i }));

            expect(onOpenChange).toHaveBeenCalledWith(false);
        });
    });

    test("does not render dialog content when closed", () => {
        mockCreate(({ alias }) => ({ id: `id-${alias}` }));
        renderDialog({ open: false, hasWritePermissions: true });

        expect(screen.queryByText("Configure Your AI Models")).not.toBeInTheDocument();
    });
});
