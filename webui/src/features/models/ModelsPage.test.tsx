// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { useFreeModelStatus } from "@/api/activation";
import { getFreeModel } from "@/api/activation/service";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ModelsPage } from "./ModelsPage";

vi.mock("./ModelsView", () => ({ ModelsView: () => null }));
vi.mock("@/api/activation/service", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/activation/service")>()),
    getFreeModel: vi.fn(async () => ({ status: "available", remainingPercent: 80, models: [] })),
}));

function FreeCreditsReader() {
    useFreeModelStatus(true);
    return null;
}

describe("ModelsPage", () => {
    test("Refresh reads the free credits again", async () => {
        renderWithProviders(
            <>
                <ModelsPage />
                <FreeCreditsReader />
            </>
        );
        await waitFor(() => expect(getFreeModel).toHaveBeenCalledTimes(1));

        await userEvent.click(screen.getByRole("button", { name: "Refresh" }));

        await waitFor(() => expect(getFreeModel).toHaveBeenCalledTimes(2));
    });
});
