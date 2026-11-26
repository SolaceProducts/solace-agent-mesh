// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import * as modelsApi from "@/api/models";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ModelWarningBanner } from "./ModelWarningBanner";

const mockModelConfigStatus = vi.fn<() => { data: { configured: boolean } | undefined }>();

beforeEach(() => {
    vi.spyOn(modelsApi, "useModelConfigStatus").mockImplementation((() => mockModelConfigStatus()) as unknown as typeof modelsApi.useModelConfigStatus);
});

describe("ModelWarningBanner", () => {
    test("renders nothing when models are already configured", async () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: true } });
        renderWithProviders(<ModelWarningBanner />, {
            chatContextValues: { hasModelConfigWrite: true },
        });
        expect(screen.queryByText(/No default model has been set up/)).not.toBeInTheDocument();
    });

    test("renders warning text when models not configured", async () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: false } });
        renderWithProviders(<ModelWarningBanner />, {
            chatContextValues: { hasModelConfigWrite: false },
        });
        expect(screen.getByText(/No default model has been set up/)).toBeInTheDocument();
    });

    test("shows Set Up Default Models button when hasModelConfigWrite is true", async () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: false } });
        renderWithProviders(<ModelWarningBanner />, {
            chatContextValues: { hasModelConfigWrite: true },
        });
        expect(screen.getByRole("button", { name: /Set Up Default Models/i })).toBeInTheDocument();
        expect(screen.queryByText(/Ask your administrator/)).not.toBeInTheDocument();
    });

    test("shows admin contact text when hasModelConfigWrite is false", async () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: false } });
        renderWithProviders(<ModelWarningBanner />, {
            chatContextValues: { hasModelConfigWrite: false },
        });
        expect(screen.getByText(/Ask your administrator/)).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Set Up Default Models/i })).not.toBeInTheDocument();
    });
});
