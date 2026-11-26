// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AlertCircle, CircleCheckBig, Clock, FileText, Loader2, type LucideIcon, MessageCircleQuestion, Pause } from "lucide-react";
import { describe, expect, test, vi } from "vitest";

import type { ComponentGroup, GroupStatus } from "./canvasGroups";
import { CanvasTabs } from "./CanvasTabs";

const ARIA_SELECTED = "aria-selected";
const ARROW_RIGHT = "{ArrowRight}";

const GROUP_LABELS = ["Alpha", "Beta", "Gamma", "Delta"];

function makeGroup(label: string, type: ComponentGroup["type"] = "agent"): ComponentGroup {
    return { id: `grp:${label}`, label, type, members: new Set([label]), anchor: label };
}

const GROUPS = GROUP_LABELS.map(label => makeGroup(label));

type TabsOptions = { activeId?: string; statusFor?: (group: ComponentGroup) => GroupStatus };

function renderTabs(options: TabsOptions = {}) {
    const onActiveChange = vi.fn();
    const user = userEvent.setup();
    const ui = ({ activeId = GROUPS[0].id, statusFor = (): GroupStatus => "ready" }: TabsOptions) => (
        <>
            <button>before</button>
            <CanvasTabs groups={GROUPS} activeId={activeId} onActiveChange={onActiveChange} statusFor={statusFor} />
            <button>after</button>
        </>
    );
    const { rerender } = render(ui(options));
    return { user, onActiveChange, tabs: screen.getAllByRole("tab"), rerender: (next: TabsOptions) => rerender(ui(next)) };
}

const STATUS_ICON: Record<GroupStatus, LucideIcon> = {
    creating: Loader2,
    ready: CircleCheckBig,
    queued: Clock,
    paused: Pause,
    draft: FileText,
    waiting: MessageCircleQuestion,
    error: AlertCircle,
};

const STATUS_WORD: Record<GroupStatus, string> = {
    creating: "Creating",
    ready: "Ready",
    queued: "Queued",
    paused: "Paused",
    draft: "Draft",
    waiting: "Waiting for input",
    error: "Error",
};

describe("CanvasTabs status as text", () => {
    test.each(Object.entries(STATUS_WORD) as [GroupStatus, string][])("a %s group's tab is named with the label and '%s'", (status, word) => {
        renderTabs({ statusFor: () => status });

        expect(screen.getByRole("tab", { name: `Alpha, ${word}` })).toBeInTheDocument();
        expect(screen.getAllByRole("tab")).toHaveLength(GROUPS.length);
    });

    test("each tab carries its own group's status", () => {
        const byLabel: Record<string, GroupStatus> = { Alpha: "ready", Beta: "paused", Gamma: "waiting", Delta: "error" };
        renderTabs({ statusFor: g => byLabel[g.label] });

        expect(screen.getAllByRole("tab").map(t => t.getAttribute("aria-label"))).toEqual(["Alpha, Ready", "Beta, Paused", "Gamma, Waiting for input", "Delta, Error"]);
    });

    test.each(Object.entries(STATUS_ICON) as [GroupStatus, LucideIcon][])("a %s group's tab shows its own status icon", (status, Icon) => {
        renderTabs({ statusFor: () => status });
        const shown = screen.getAllByRole("tab")[0].querySelector("[data-slot=status-icon]");
        const { container } = render(<Icon />);

        expect(shown?.innerHTML).toBe(container.querySelector("svg")?.innerHTML);
    });

    test("every status icon is different, so waiting, paused and draft cannot be confused", () => {
        expect(new Set(Object.values(STATUS_ICON).map(Icon => render(<Icon />).container.innerHTML)).size).toBe(Object.keys(STATUS_ICON).length);
    });
});

describe("CanvasTabs tablist semantics", () => {
    test("the tablist is named and exactly the active tab is selected", () => {
        const { tabs } = renderTabs({ activeId: GROUPS[2].id });

        expect(screen.getByRole("tablist", { name: "Component groups" })).toBeInTheDocument();
        expect(tabs.map(t => t.getAttribute(ARIA_SELECTED))).toEqual(["false", "false", "true", "false"]);
    });

    test("tabs do not point at a panel that is not rendered", () => {
        const { tabs } = renderTabs();

        for (const tab of tabs) expect(tab).not.toHaveAttribute("aria-controls");
    });

    test("roving tabindex: once focus is in the strip only the focused tab is in the tab order", async () => {
        const { user, tabs } = renderTabs({ activeId: GROUPS[1].id });

        await user.tab();
        await user.tab();
        expect(tabs.map(t => t.tabIndex)).toEqual([-1, 0, -1, -1]);

        await user.keyboard(ARROW_RIGHT);
        expect(tabs.map(t => t.tabIndex)).toEqual([-1, -1, 0, -1]);
    });

    test("the strip is a single tab stop that lands on the active tab", async () => {
        const { user, tabs } = renderTabs({ activeId: GROUPS[2].id });

        await user.tab();
        expect(screen.getByRole("button", { name: "before" })).toHaveFocus();
        await user.tab();
        expect(tabs[2]).toHaveFocus();
        await user.tab();
        expect(screen.getByRole("button", { name: "after" })).toHaveFocus();
    });
});

describe("CanvasTabs keyboard navigation", () => {
    const LAST = GROUPS.length - 1;

    test.each([
        { key: "ArrowRight", from: 0, to: 1 },
        { key: "ArrowRight", from: 2, to: 3 },
        { key: "ArrowRight", from: LAST, to: 0 },
        { key: "ArrowLeft", from: 3, to: 2 },
        { key: "ArrowLeft", from: 1, to: 0 },
        { key: "ArrowLeft", from: 0, to: LAST },
        { key: "Home", from: 2, to: 0 },
        { key: "Home", from: 0, to: 0 },
        { key: "End", from: 1, to: LAST },
        { key: "End", from: LAST, to: LAST },
    ])("$key moves focus from tab $from to tab $to without selecting it", async ({ key, from, to }) => {
        const { user, tabs, onActiveChange } = renderTabs({ activeId: GROUPS[from].id });
        tabs[from].focus();

        await user.keyboard(`{${key}}`);

        expect(tabs[to]).toHaveFocus();
        expect(onActiveChange).not.toHaveBeenCalled();
        expect(tabs[from]).toHaveAttribute(ARIA_SELECTED, "true");
    });

    test("tab order follows focus after an arrow key, so Shift+Tab returns to the focused tab", async () => {
        const { user, tabs } = renderTabs();
        tabs[0].focus();

        await user.keyboard(ARROW_RIGHT);
        await user.tab();
        await user.tab({ shift: true });

        expect(tabs[1]).toHaveFocus();
    });

    test.each(["{Enter}", " "])("activating the focused tab with %j selects its group", async keys => {
        const { user, tabs, onActiveChange } = renderTabs();
        tabs[0].focus();
        await user.keyboard(`${ARROW_RIGHT}${ARROW_RIGHT}`);

        await user.keyboard(keys);

        expect(onActiveChange).toHaveBeenCalledExactlyOnceWith(GROUPS[2].id);
    });

    test("clicking a tab selects its group", async () => {
        const { user, tabs, onActiveChange } = renderTabs();

        await user.click(tabs[3]);

        expect(onActiveChange).toHaveBeenCalledExactlyOnceWith(GROUPS[3].id);
    });

    test.each([
        { how: "clicking", act: (user: ReturnType<typeof userEvent.setup>, tab: HTMLElement) => user.click(tab) },
        { how: "pressing Enter on", act: (user: ReturnType<typeof userEvent.setup>) => user.keyboard("{Enter}") },
        { how: "pressing Space on", act: (user: ReturnType<typeof userEvent.setup>) => user.keyboard(" ") },
    ])("$how the already-active tab still reports it, so the view is pinned against auto-follow", async ({ act }) => {
        const { user, tabs, onActiveChange } = renderTabs();
        tabs[0].focus();

        await act(user, tabs[0]);

        expect(onActiveChange).toHaveBeenCalledExactlyOnceWith(GROUPS[0].id);
    });
});

describe("CanvasTabs while the parent changes under it", () => {
    test("focus and the tab stop stay on the focused tab when the active group and statuses change", async () => {
        const { user, tabs, rerender } = renderTabs();
        tabs[0].focus();
        await user.keyboard(ARROW_RIGHT);

        rerender({ activeId: GROUPS[3].id, statusFor: () => "creating" });

        expect(tabs[1]).toHaveFocus();
        expect(tabs.map(t => t.tabIndex)).toEqual([-1, 0, -1, -1]);
        expect(tabs.map(t => t.getAttribute(ARIA_SELECTED))).toEqual(["false", "false", "false", "true"]);
        expect(tabs[1]).toHaveAccessibleName("Beta, Creating");
    });

    test("an active id that matches no group leaves the strip reachable", async () => {
        const { user, tabs } = renderTabs({ activeId: "grp:missing" });

        await user.tab();
        await user.tab();

        expect(tabs[0]).toHaveFocus();
        expect(tabs.map(t => t.getAttribute(ARIA_SELECTED))).toEqual(["false", "false", "false", "false"]);
    });

    test("Shift+Tab leaves the strip for the preceding control", async () => {
        const { user, tabs } = renderTabs();
        tabs[0].focus();

        await user.tab({ shift: true });

        expect(screen.getByRole("button", { name: "before" })).toHaveFocus();
    });

    test("a controlled parent follows the keyboard: ArrowRight then Enter moves the selection", async () => {
        const user = userEvent.setup();
        function Controlled() {
            const [activeId, setActiveId] = useState(GROUPS[0].id);
            return <CanvasTabs groups={GROUPS} activeId={activeId} onActiveChange={setActiveId} statusFor={() => "ready"} />;
        }
        render(<Controlled />);
        const tabs = screen.getAllByRole("tab");
        tabs[0].focus();

        await user.keyboard(ARROW_RIGHT);
        expect(tabs[0]).toHaveAttribute(ARIA_SELECTED, "true");
        await user.keyboard("{Enter}");

        expect(tabs.map(t => t.getAttribute(ARIA_SELECTED))).toEqual(["false", "true", "false", "false"]);
        expect(tabs[1]).toHaveFocus();
    });
});
