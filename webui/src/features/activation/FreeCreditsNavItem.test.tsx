// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useLocation } from "react-router-dom";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { type FreeCreditsSummary, useFreeCreditsSummary } from "./freeCredits";
import { FreeCreditsNavItem } from "./FreeCreditsNavItem";

vi.mock("./freeCredits", async importOriginal => ({ ...(await importOriginal<typeof import("./freeCredits")>()), useFreeCreditsSummary: vi.fn() }));

function CurrentPath() {
    return <p data-testid="path">{useLocation().pathname}</p>;
}

function renderNavItem(collapsed = false) {
    return renderWithProviders(
        <>
            <FreeCreditsNavItem collapsed={collapsed} />
            <CurrentPath />
        </>,
        { initialPath: "/chat" }
    );
}

const twentyUsed: FreeCreditsSummary = { used: 20, tone: "ok", label: "20% of free credits used" };
const signInAgain: FreeCreditsSummary = { used: 0, tone: "low", label: "Sign in again" };
const usedUp: FreeCreditsSummary = { used: 100, tone: "out", label: "No free credits left" };
const paused: FreeCreditsSummary = { used: 0, tone: "out", label: "The free model is paused" };

describe("FreeCreditsNavItem", () => {
    beforeEach(() => {
        vi.mocked(useFreeCreditsSummary).mockReturnValue(twentyUsed);
    });

    test("shows nothing when the default models are not on the free model", () => {
        vi.mocked(useFreeCreditsSummary).mockReturnValue(null);
        renderNavItem();
        expect(screen.queryByText(/free credits/i)).not.toBeInTheDocument();
    });

    test("shows the share of free credits used in one line and opens Models", async () => {
        renderNavItem();
        await userEvent.click(screen.getByRole("link", { name: "20% of free credits used" }));
        expect(screen.getByTestId("path")).toHaveTextContent("/models");
    });

    test("asks the person to sign in again once the free model's sign-in has expired", async () => {
        vi.mocked(useFreeCreditsSummary).mockReturnValue(signInAgain);
        renderNavItem();
        await userEvent.click(screen.getByRole("link", { name: "Sign in again" }));
        expect(screen.getByTestId("path")).toHaveTextContent("/models");
    });

    test("keeps the way onto the person's own model off the rail while credits remain", () => {
        renderNavItem();
        expect(screen.queryByRole("button", { name: "Replace With My Own Model" })).not.toBeInTheDocument();
    });

    test.each([
        ["used up", usedUp],
        ["paused", paused],
    ])("once the free model is %s, offers to replace it from the rail", async (_state, summary) => {
        vi.mocked(useFreeCreditsSummary).mockReturnValue(summary);
        renderNavItem();
        expect(screen.getByRole("link", { name: summary.label })).toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Replace With My Own Model" }));
        expect(screen.getByRole("dialog")).toBeInTheDocument();
        expect(screen.getByTestId("path")).toHaveTextContent("/chat");
    });

    test("collapsed, leaves the replace button to the expanded rail", () => {
        vi.mocked(useFreeCreditsSummary).mockReturnValue(usedUp);
        renderNavItem(true);
        expect(screen.queryByRole("button", { name: "Replace With My Own Model" })).not.toBeInTheDocument();
    });

    test("collapsed, names the credits used on its ring and opens Models", async () => {
        renderNavItem(true);
        expect(screen.queryByText("20% of free credits used")).not.toBeInTheDocument();
        await userEvent.click(screen.getByRole("link", { name: "20% of free credits used" }));
        expect(screen.getByTestId("path")).toHaveTextContent("/models");
    });
});
