// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { OnboardingBanner } from "./OnboardingBanner";

afterEach(() => vi.restoreAllMocks());

describe("OnboardingBanner", () => {
    test("clicking the learn-more link opens the URL in a new tab", async () => {
        const openSpy = vi.spyOn(window, "open").mockImplementation(() => null);
        renderWithProviders(<OnboardingBanner storageKey="test-onboarding-banner" header="Header" description="Some description." learnMoreText="Learn about evaluators and see examples" learnMoreUrl="https://example.com/docs" />);
        await userEvent.click(screen.getByRole("button", { name: /learn about evaluators/i }));
        expect(openSpy).toHaveBeenCalledWith("https://example.com/docs", "_blank");
    });
});
