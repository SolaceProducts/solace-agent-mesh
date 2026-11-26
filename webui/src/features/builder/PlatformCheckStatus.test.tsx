// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { PlatformCheck } from "./platformCheck";
import { PlatformCheckStatus } from "./PlatformCheckStatus";

function renderStatus(check: PlatformCheck, { canAskBuilder = false, turnRunning = false } = {}) {
    const onCheckAgain = vi.fn();
    const onAskBuilder = vi.fn();
    const view = render(<PlatformCheckStatus view={{ check, canAskBuilder, turnRunning }} onCheckAgain={onCheckAgain} onAskBuilder={onAskBuilder} />);
    return { ...view, onCheckAgain, onAskBuilder };
}

const problem = { kind: "agent", name: "TestAgent", message: "model alias is unknown" };

describe("PlatformCheckStatus", () => {
    test("idle shows nothing", () => {
        const { container } = renderStatus({ kind: "idle" });
        expect(container).toBeEmptyDOMElement();
    });

    test("checking says the platform is being asked", () => {
        renderStatus({ kind: "checking" });
        expect(screen.getByText("Checking with the platform…")).toBeInTheDocument();
    });

    test("passed says Ready, with no list when there are no warnings", () => {
        renderStatus({ kind: "passed", warnings: [] });
        expect(screen.getByText("Ready")).toBeInTheDocument();
        expect(screen.queryByRole("list")).toBeNull();
    });

    test("passed lists the warnings under Ready", () => {
        renderStatus({ kind: "passed", warnings: ["Connector X will be reused", "Skill Y is unchanged"] });
        expect(screen.getByText("Ready")).toBeInTheDocument();
        expect(screen.getAllByRole("listitem").map(li => li.textContent)).toEqual(["Connector X will be reused", "Skill Y is unchanged"]);
    });

    test("refused lists each problem as kind name: message and offers Check again", () => {
        const { onCheckAgain } = renderStatus({ kind: "refused", problems: [problem, { kind: "workflow", name: "Flow", message: "unknown node" }], forbidden: [] });
        expect(screen.getByText("agent TestAgent: model alias is unknown")).toBeInTheDocument();
        expect(screen.getByText("workflow Flow: unknown node")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Check again" }));
        expect(onCheckAgain).toHaveBeenCalledTimes(1);
    });

    test("a refusal of the whole request names no component", () => {
        renderStatus({ kind: "refused", problems: [{ kind: null, name: null, message: "build name is not allowed" }], forbidden: [] });
        expect(screen.getByText("build name is not allowed")).toBeInTheDocument();
    });

    test("refused offers the Builder the fix only when it can take it", () => {
        const check: PlatformCheck = { kind: "refused", problems: [problem], forbidden: [] };
        const without = renderStatus(check);
        expect(screen.queryByRole("button", { name: "Ask the Builder to fix these" })).toBeNull();
        without.unmount();

        const { onAskBuilder } = renderStatus(check, { canAskBuilder: true });
        fireEvent.click(screen.getByRole("button", { name: "Ask the Builder to fix these" }));
        expect(onAskBuilder).toHaveBeenCalledTimes(1);
    });

    test("refused also shows what the user is not allowed to do", () => {
        renderStatus({ kind: "refused", problems: [problem], forbidden: [{ kind: "connector", name: "Slack", message: "not permitted" }] });
        expect(screen.getByText("connector Slack: not permitted")).toBeInTheDocument();
    });

    test("notAllowed shows the permission problems and Check again, but no hand-off to the Builder", () => {
        const { onCheckAgain } = renderStatus({ kind: "notAllowed", problems: [{ kind: "agent", name: "TestAgent", message: "you may not create agents" }] }, { canAskBuilder: true });
        expect(screen.getByText("agent TestAgent: you may not create agents")).toBeInTheDocument();
        expect(screen.getAllByRole("button").map(button => button.textContent)).toEqual(["Check again"]);
        fireEvent.click(screen.getByRole("button", { name: "Check again" }));
        expect(onCheckAgain).toHaveBeenCalledTimes(1);
    });

    test("with no check at all it renders nothing", () => {
        const { container } = render(<PlatformCheckStatus />);
        expect(container).toBeEmptyDOMElement();
    });

    test("checking and Ready are announced politely", () => {
        const checking = renderStatus({ kind: "checking" });
        expect(screen.getByRole("status")).toHaveAttribute("aria-live", "polite");
        checking.unmount();

        renderStatus({ kind: "passed", warnings: [] });
        expect(screen.getByRole("status")).toHaveTextContent("Ready");
        expect(screen.getByRole("status")).toHaveAttribute("aria-live", "polite");
    });

    test("unavailable says the platform could not be asked, in visible text with the reason, and Retry checks again", () => {
        const { onCheckAgain } = renderStatus({ kind: "unavailable", message: "platform down" });
        expect(screen.getByText("Couldn't check with the platform")).toBeInTheDocument();
        expect(screen.getByText("platform down")).toBeVisible();
        fireEvent.click(screen.getByRole("button", { name: "Retry" }));
        expect(onCheckAgain).toHaveBeenCalledTimes(1);
    });

    test.each<[string, PlatformCheck]>([
        ["refused", { kind: "refused", problems: [problem], forbidden: [] }],
        ["notAllowed", { kind: "notAllowed", problems: [problem] }],
        ["unavailable", { kind: "unavailable", message: "platform down" }],
    ])("%s disables Check again and Ask the Builder while a turn runs, and enables them after", (_label, check) => {
        const running = renderStatus(check, { canAskBuilder: true, turnRunning: true });
        for (const button of screen.getAllByRole("button")) expect(button).toBeDisabled();
        running.unmount();

        renderStatus(check, { canAskBuilder: true });
        for (const button of screen.getAllByRole("button")) expect(button).toBeEnabled();
    });

    test("one live region stays mounted while the check goes from checking to passed", () => {
        const view = render(<PlatformCheckStatus view={{ check: { kind: "checking" }, canAskBuilder: false }} />);
        const region = screen.getByRole("status");
        expect(region).toHaveAttribute("aria-live", "polite");

        view.rerender(<PlatformCheckStatus view={{ check: { kind: "passed", warnings: [] }, canAskBuilder: false }} />);

        expect(screen.getByRole("status")).toBe(region);
        expect(region).toHaveTextContent("Ready");
    });

    test("the live region also holds a refusal, so every verdict is announced from the same place", () => {
        const view = render(<PlatformCheckStatus view={{ check: { kind: "checking" }, canAskBuilder: false }} />);
        const region = screen.getByRole("status");

        view.rerender(<PlatformCheckStatus view={{ check: { kind: "refused", problems: [problem], forbidden: [] }, canAskBuilder: false }} />);

        expect(screen.getByRole("status")).toBe(region);
        expect(region).toHaveTextContent("agent TestAgent: model alias is unknown");
    });

    test("decorative icons are hidden from assistive technology", () => {
        const checking = renderStatus({ kind: "checking" });
        expect(checking.container.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
        checking.unmount();

        const passed = renderStatus({ kind: "passed", warnings: [] });
        expect(passed.container.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
    });
});
