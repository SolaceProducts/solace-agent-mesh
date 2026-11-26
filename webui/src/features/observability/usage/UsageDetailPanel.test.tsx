// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { usageKeys, useUsageDetail } from "@/api/usage";

import { usageAgentRows, usageDetailFor } from "@/testing/data/usage";

import { UsageDetailPanel } from "./UsageDetailPanel";

vi.mock("@/api/usage", async importOriginal => {
    const actual = await importOriginal<typeof import("@/api/usage")>();
    return { ...actual, useUsageDetail: vi.fn(actual.useUsageDetail) };
});

const WINDOW = { period: "this_month" } as const;
const agent = usageAgentRows.find(row => row.name === "Code Assistant")!;
const TARGET = { kind: "agents" as const, ref: { id: agent.id! } };

function setup() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    client.setQueryData(usageKeys.detail("agents", TARGET.ref, WINDOW), usageDetailFor("agents", agent));
    const onClose = vi.fn();
    const wrapper = ({ children }: { children: ReactNode }) => (
        <OpenFeatureTestProvider flagValueMap={{}}>
            <QueryClientProvider client={client}>{children}</QueryClientProvider>
        </OpenFeatureTestProvider>
    );
    const view = (target: typeof TARGET | null) => (
        <>
            <button type="button">Code Assistant link</button>
            <UsageDetailPanel target={target} windowParams={WINDOW} periodLabel="This month" today="2026-10-16" onClose={onClose} />
        </>
    );
    return { onClose, view, wrapper };
}

describe("UsageDetailPanel on a desktop-width screen, over the page", () => {
    test("opens over the page as a labelled panel with a backdrop, not a dialog, with its title focused", async () => {
        const { view, wrapper } = setup();
        render(view(TARGET), { wrapper });
        const panel = await screen.findByTestId("usageDetailPanel");
        expect(panel.tagName).toBe("ASIDE");
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
        expect(screen.getByRole("complementary", { name: "Code Assistant" })).toBe(panel);
        expect(screen.getByRole("heading", { name: "Code Assistant" })).toHaveFocus();
        expect(screen.getByTestId("sidePanelBackdrop")).toHaveAttribute("aria-hidden", "true");
    });

    test("Escape, the close button and a click on the backdrop each close it", async () => {
        const { onClose, view, wrapper } = setup();
        render(view(TARGET), { wrapper });
        await screen.findByTestId("usageDetailPanel");
        await userEvent.keyboard("{Escape}");
        await userEvent.click(screen.getByRole("button", { name: "Close usage details" }));
        await userEvent.click(screen.getByTestId("sidePanelBackdrop"));
        expect(onClose).toHaveBeenCalledTimes(3);
    });

    test("closing returns focus to the name it was opened from", async () => {
        const { view, wrapper } = setup();
        const { rerender } = render(view(null), { wrapper });
        const opener = screen.getByRole("button", { name: "Code Assistant link" });
        act(() => opener.focus());
        rerender(view(TARGET));
        await screen.findByTestId("usageDetailPanel");
        rerender(view(null));
        expect(opener).toHaveFocus();
    });
});

describe("UsageDetailPanel when the detail fails to load", () => {
    test("says so with the dashboard's slow-read hint, and Retry reads it again", async () => {
        const refetch = vi.fn();
        const slowRead = Object.assign(new Error("Usage figures took too long to read"), { status: 503 });
        vi.mocked(useUsageDetail).mockReturnValue({ data: undefined, error: slowRead, refetch } as unknown as ReturnType<typeof useUsageDetail>);
        const { wrapper } = setup();
        render(<UsageDetailPanel target={TARGET} windowParams={WINDOW} periodLabel="This month" today="2026-10-16" onClose={vi.fn()} />, { wrapper });
        expect(screen.getByRole("alert")).toHaveTextContent("Couldn't load usage. The figures took too long to read. Select Refresh in a moment, or choose a shorter period.");
        await userEvent.click(screen.getByRole("button", { name: "Retry" }));
        expect(refetch).toHaveBeenCalled();
        vi.mocked(useUsageDetail).mockRestore();
    });
});
