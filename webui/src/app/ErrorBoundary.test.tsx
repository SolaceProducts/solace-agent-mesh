// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import ErrorBoundary from "./ErrorBoundary";

function Boom(): never {
    throw new Error("boom");
}

describe("ErrorBoundary", () => {
    beforeEach(() => {
        vi.spyOn(console, "error").mockImplementation(() => {});
    });
    afterEach(() => {
        vi.restoreAllMocks();
    });

    it("renders children when there is no error", () => {
        render(
            <ErrorBoundary>
                <div>healthy</div>
            </ErrorBoundary>
        );
        expect(screen.getByText("healthy")).toBeInTheDocument();
    });

    it("renders a recoverable fallback when a child throws", () => {
        render(
            <ErrorBoundary>
                <Boom />
            </ErrorBoundary>
        );
        expect(screen.getByRole("alert")).toBeInTheDocument();
        expect(screen.getByText("Something Went Wrong")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "Return To Login" })).toBeInTheDocument();
    });

    it("says why sign-in did not start when an embedded frame cannot open a popup", async () => {
        const originalTop = window.top;
        Object.defineProperty(window, "top", {
            value: {
                location: {
                    get origin(): string {
                        throw new Error("cross-origin");
                    },
                    set href(_v: string) {},
                },
            },
            writable: true,
            configurable: true,
        });
        const open = vi.spyOn(window, "open").mockReturnValue(null);
        vi.spyOn(console, "warn").mockImplementation(() => {});

        try {
            render(
                <ErrorBoundary>
                    <Boom />
                </ErrorBoundary>
            );
            await userEvent.click(screen.getByRole("link", { name: "Return To Login" }));

            expect(open).toHaveBeenCalled();
            expect(screen.getByText(/Allow pop-ups for this site/i)).toBeInTheDocument();
        } finally {
            Object.defineProperty(window, "top", { value: originalTop, writable: true, configurable: true });
        }
    });
});
