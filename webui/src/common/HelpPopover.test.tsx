// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test } from "vitest";

import { HelpPopover } from "./HelpPopover";

const HELP_TEXT = "This is the help content";

describe("HelpPopover — keyboard focus does not break the form", () => {
    test("focusing the trigger opens the popover content", async () => {
        const user = userEvent.setup();
        render(<HelpPopover content={HELP_TEXT} />);

        await user.tab();

        await waitFor(() => expect(screen.getByText(HELP_TEXT)).toBeInTheDocument());
    });

    test("focus stays on the trigger when the popover opens", async () => {
        const user = userEvent.setup();
        render(<HelpPopover content={HELP_TEXT} />);

        const trigger = screen.getByRole("button", { name: "Help" });
        await user.tab();

        await waitFor(() => expect(screen.getByText(HELP_TEXT)).toBeInTheDocument());
        expect(document.activeElement).toBe(trigger);
    });

    test("tabbing off the trigger closes the popover and moves focus to the next control", async () => {
        const user = userEvent.setup();
        render(
            <>
                <HelpPopover content={HELP_TEXT} />
                <input data-testid="next" aria-label="next field" />
            </>
        );

        const nextField = screen.getByTestId("next");

        await user.tab();
        await waitFor(() => expect(screen.getByText(HELP_TEXT)).toBeInTheDocument());

        await user.tab();

        expect(document.activeElement).toBe(nextField);
        await waitFor(() => expect(screen.queryByText(HELP_TEXT)).not.toBeInTheDocument());
    });

    test("defaults to a 20px icon when no size is given", () => {
        render(<HelpPopover content={HELP_TEXT} />);

        const icon = screen.getByRole("button", { name: "Help" }).querySelector("svg");
        expect(icon).toHaveAttribute("width", "20");
    });

    test("renders a smaller icon when size is given", () => {
        render(<HelpPopover content={HELP_TEXT} size="16" />);

        const icon = screen.getByRole("button", { name: "Help" }).querySelector("svg");
        expect(icon).toHaveAttribute("width", "16");
    });

    test("mouse hover opens and unhover closes the popover", async () => {
        const user = userEvent.setup();
        render(<HelpPopover content={HELP_TEXT} />);

        const trigger = screen.getByRole("button", { name: "Help" });

        await user.hover(trigger);
        await waitFor(() => expect(screen.getByText(HELP_TEXT)).toBeInTheDocument());

        await user.unhover(trigger);
        await waitFor(() => expect(screen.queryByText(HELP_TEXT)).not.toBeInTheDocument());
    });
});

describe("HelpPopover — a link inside it is reachable from the keyboard", () => {
    const LINK = "Learn more";
    const renderWithLink = () =>
        render(
            <>
                <HelpPopover content={<a href="https://docs.example.com/page">{LINK}</a>} />
                <input aria-label="next field" />
            </>
        );
    const openFromKeyboard = async (user: ReturnType<typeof userEvent.setup>) => {
        await user.tab();
        await waitFor(() => expect(screen.getByRole("link", { name: LINK })).toBeInTheDocument());
    };

    test("Tab from the trigger moves into the popover, which stays open; Tab from its last link moves on past the trigger and closes it", async () => {
        const user = userEvent.setup();
        renderWithLink();
        await openFromKeyboard(user);

        await user.tab();
        expect(document.activeElement).toBe(screen.getByRole("link", { name: LINK }));

        await user.tab();
        expect(document.activeElement).toBe(screen.getByRole("textbox", { name: "next field" }));
        await waitFor(() => expect(screen.queryByRole("link", { name: LINK })).not.toBeInTheDocument());
    });

    test("Tab from the link skips what cannot take focus: a tabindex=-1 control, a hidden input, an inert subtree", async () => {
        const user = userEvent.setup();
        render(
            <>
                <HelpPopover content={<a href="https://docs.example.com/page">{LINK}</a>} />
                <button type="button" tabIndex={-1}>
                    roving item
                </button>
                <input type="hidden" name="token" />
                <div inert>
                    <button type="button">behind a panel</button>
                </div>
                <input aria-label="next field" />
            </>
        );
        await openFromKeyboard(user);
        await user.tab();

        await user.tab();
        expect(document.activeElement).toBe(screen.getByRole("textbox", { name: "next field" }));
    });

    test("Tab from the link falls back to the trigger, not the page body, when the next control will not take focus", async () => {
        const user = userEvent.setup();
        renderWithLink();
        screen.getByRole("textbox", { name: "next field" }).focus = () => {};
        await openFromKeyboard(user);
        await user.tab();

        await user.tab();
        expect(document.activeElement).toBe(screen.getByRole("button", { name: "Help" }));
        await waitFor(() => expect(screen.queryByRole("link", { name: LINK })).not.toBeInTheDocument());
    });

    test("Shift+Tab from the link returns to the trigger and keeps the popover open", async () => {
        const user = userEvent.setup();
        renderWithLink();
        await openFromKeyboard(user);
        await user.tab();

        await user.tab({ shift: true });
        expect(document.activeElement).toBe(screen.getByRole("button", { name: "Help" }));
        expect(screen.getByRole("link", { name: LINK })).toBeInTheDocument();
    });

    test("Escape from the link closes the popover and returns focus to the trigger without reopening it", async () => {
        const user = userEvent.setup();
        renderWithLink();
        await openFromKeyboard(user);
        await user.tab();

        await user.keyboard("{Escape}");
        expect(document.activeElement).toBe(screen.getByRole("button", { name: "Help" }));
        await waitFor(() => expect(screen.queryByRole("link", { name: LINK })).not.toBeInTheDocument());
    });
});
