// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { Header } from "./Header";

const setMobile = (mobile: boolean) => {
    Object.defineProperty(window, "innerWidth", { value: mobile ? 375 : 1280, configurable: true });
    window.matchMedia = vi.fn().mockImplementation((q: string) => ({
        matches: mobile && q.includes("max-width"),
        media: q,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
    }));
};

beforeEach(() => {
    setMobile(false);
});

describe("Header", () => {
    test("renders the full breadcrumb chain on desktop", () => {
        const breadcrumbs = [{ label: "Projects", onClick: vi.fn() }, { label: "My Project" }];
        render(<Header title="My Project" breadcrumbs={breadcrumbs} />);

        expect(screen.getByText("Projects")).toBeInTheDocument();
        const occurrences = screen.getAllByText("My Project");
        expect(occurrences).toHaveLength(2);
    });

    test("collapses breadcrumbs to '… > current' on mobile", async () => {
        setMobile(true);
        const parentClick = vi.fn();
        const breadcrumbs = [{ label: "Projects", onClick: parentClick }, { label: "My Project" }];
        render(<Header title="My Project" breadcrumbs={breadcrumbs} />);

        expect(await screen.findByRole("button", { name: /back to projects/i })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /^projects$/i })).not.toBeInTheDocument();
    });

    test("mobile ellipsis click goes back to parent breadcrumb", async () => {
        setMobile(true);
        const user = userEvent.setup();
        const parentClick = vi.fn();
        render(<Header title="Detail" breadcrumbs={[{ label: "Parent", onClick: parentClick }, { label: "Detail" }]} />);

        await user.click(await screen.findByRole("button", { name: /back to parent/i }));
        expect(parentClick).toHaveBeenCalledOnce();
    });

    test("mobile renders the hamburger menu trigger; desktop does not", async () => {
        setMobile(true);
        const { unmount } = render(<Header title="Page" />);
        expect(await screen.findByRole("button", { name: /^menu$/i })).toBeInTheDocument();
        unmount();

        setMobile(false);
        render(<Header title="Page" />);
        expect(screen.queryByRole("button", { name: /^menu$/i })).not.toBeInTheDocument();
    });

    test("clicking hamburger dispatches the open-mobile-nav event", async () => {
        setMobile(true);
        const user = userEvent.setup();
        const listener = vi.fn();
        window.addEventListener("open-mobile-nav", listener);

        render(<Header title="Page" />);
        await user.click(await screen.findByRole("button", { name: /^menu$/i }));

        expect(listener).toHaveBeenCalledOnce();
        window.removeEventListener("open-mobile-nav", listener);
    });

    test("breadcrumb without onClick renders as plain text (mobile + desktop)", () => {
        setMobile(true);
        const { unmount } = render(<Header title="Detail" breadcrumbs={[{ label: "Parent" }, { label: "Detail" }]} />);
        expect(screen.queryByRole("button", { name: /back to parent/i })).not.toBeInTheDocument();
        unmount();

        setMobile(false);
        render(<Header title="Detail" breadcrumbs={[{ label: "Parent" }, { label: "Detail" }]} />);
        expect(screen.queryByRole("button", { name: /^parent$/i })).not.toBeInTheDocument();
        expect(screen.getByText("Parent")).toBeInTheDocument();
    });

    test("current breadcrumb on desktop is not capped at 150px and may span 50 characters up to half the screen", () => {
        const longName = "A".repeat(50);
        render(<Header title="Page" breadcrumbs={[{ label: "Projects", onClick: vi.fn() }, { label: longName }]} />);

        const current = screen.getByText(longName);
        expect(current).not.toHaveClass("max-w-[150px]");
        expect(current).toHaveClass("max-w-[min(50ch,50vw)]", "truncate");
    });

    test("renders nothing for breadcrumbs when array is empty", () => {
        render(<Header title="Page" breadcrumbs={[]} />);
        expect(screen.queryByText("…")).not.toBeInTheDocument();
    });
});
