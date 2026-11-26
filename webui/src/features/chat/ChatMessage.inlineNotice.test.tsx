// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { buildErrorMessage } from "@/providers/chat";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ChatMessage } from "./ChatMessage";

vi.mock("@/features/activation", () => ({ FreeModelErrorActions: ({ message }: { message: string }) => <p>Free model actions for: {message}</p> }));

const TASK_ID = "11111111-1111-1111-1111-111111111111";

describe("ChatMessage inline notices", () => {
    it("renders a warning message as a polite status notice", () => {
        renderWithProviders(<ChatMessage message={buildErrorMessage("Your usage budget is used up.", true, TASK_ID)} isLastWithTaskId />);

        const notice = screen.getByTestId("inlineNotice-warning");
        expect(screen.getAllByRole("status")).toContain(notice);
        expect(notice).toHaveTextContent("Your usage budget is used up.");
        expect(screen.queryByTestId("inlineNotice-error")).not.toBeInTheDocument();
    });

    it("renders an error message as an alert notice", () => {
        renderWithProviders(<ChatMessage message={buildErrorMessage("Your usage budget is used up.", false, TASK_ID)} isLastWithTaskId />);

        const notice = screen.getByTestId("inlineNotice-error");
        expect(notice).toHaveAttribute("role", "alert");
        expect(notice).toHaveTextContent("Your usage budget is used up.");
        expect(screen.queryByTestId("inlineNotice-warning")).not.toBeInTheDocument();
    });

    it("hands an error's text to the free model's actions, which decide whether to offer anything", () => {
        renderWithProviders(<ChatMessage message={buildErrorMessage("Error: Terms not accepted. Accept the free model's terms to continue.", false, TASK_ID)} isLastWithTaskId />);

        expect(screen.getByTestId("inlineNotice-error")).toHaveTextContent("Accept the free model's terms");
        expect(screen.getByText(/Free model actions for: .*Accept the free model's terms/)).toBeInTheDocument();
    });

    it("offers no free model actions under a warning", () => {
        renderWithProviders(<ChatMessage message={buildErrorMessage("Your usage budget is used up.", true, TASK_ID)} isLastWithTaskId />);

        expect(screen.queryByText(/Free model actions for/)).not.toBeInTheDocument();
    });
});
