// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { AlreadySignedInHandoff, SIGN_IN_HANDOFF_CLOSE_DELAY_MS, SignedInHandoff } from "./SignedInHandoff";
import { pendingSignInSource, rememberSignInSource, settleSignInSource } from "./signInSource";

describe("pendingSignInSource", () => {
    afterEach(() => sessionStorage.clear());

    test("names a known chat app from the address", () => {
        expect(pendingSignInSource("?signInFrom=whatsapp")).toEqual({ key: "whatsapp", label: "WhatsApp" });
    });

    test("ignores an absent or unknown source, including prototype keys", () => {
        expect(pendingSignInSource("")).toBeNull();
        expect(pendingSignInSource("?signInFrom=")).toBeNull();
        expect(pendingSignInSource("?signInFrom=somewhere")).toBeNull();
        expect(pendingSignInSource("?signInFrom=constructor")).toBeNull();
    });

    test("falls back to a source remembered across a sign-out", () => {
        rememberSignInSource("whatsapp");
        expect(pendingSignInSource("")).toEqual({ key: "whatsapp", label: "WhatsApp" });
    });
});

describe("settleSignInSource", () => {
    test("drops the source from the address and from storage, keeping the rest of the address", () => {
        rememberSignInSource("whatsapp");
        globalThis.history.replaceState(null, "", "/?signInFrom=whatsapp&agent=hr#/chat");
        settleSignInSource();
        expect(globalThis.location.search).toBe("?agent=hr");
        expect(globalThis.location.hash).toBe("#/chat");
        expect(pendingSignInSource()).toBeNull();
    });
});

describe("SignedInHandoff", () => {
    let close: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        vi.useFakeTimers();
        close = vi.spyOn(globalThis, "close").mockImplementation(() => {});
    });

    afterEach(() => {
        vi.useRealTimers();
        vi.restoreAllMocks();
        vi.unstubAllGlobals();
    });

    test("a closable tab confirms the sign-in and closes after the delay", () => {
        vi.spyOn(globalThis.history, "length", "get").mockReturnValue(1);
        vi.stubGlobal("closed", true);
        render(<SignedInHandoff sourceLabel="WhatsApp" onContinue={vi.fn()} />);
        expect(screen.getByRole("status")).toHaveTextContent("This window will close shortly. Go back to WhatsApp to continue.");

        act(() => vi.advanceTimersByTime(SIGN_IN_HANDOFF_CLOSE_DELAY_MS - 1));
        expect(close).not.toHaveBeenCalled();
        act(() => vi.advanceTimersByTime(1));
        expect(close).toHaveBeenCalledTimes(1);
        expect(screen.queryByRole("button", { name: "Continue to Agent Mesh" })).not.toBeInTheDocument();
    });

    test("a closable tab whose close is refused falls back to asking the user", () => {
        vi.spyOn(globalThis.history, "length", "get").mockReturnValue(1);
        vi.stubGlobal("closed", false);
        render(<SignedInHandoff sourceLabel="WhatsApp" onContinue={vi.fn()} />);

        act(() => vi.advanceTimersByTime(SIGN_IN_HANDOFF_CLOSE_DELAY_MS));
        expect(close).toHaveBeenCalledTimes(1);
        expect(screen.getByRole("status")).toHaveTextContent(/^Close this window to return to WhatsApp, or select the button below to open Agent Mesh\.$/);
        expect(screen.getByRole("button", { name: "Continue to Agent Mesh" })).toBeInTheDocument();
    });

    test("a window a script opened is closable whatever its history", () => {
        vi.spyOn(globalThis.history, "length", "get").mockReturnValue(4);
        vi.stubGlobal("opener", {});
        vi.stubGlobal("closed", true);
        render(<SignedInHandoff sourceLabel="WhatsApp" onContinue={vi.fn()} />);

        act(() => vi.advanceTimersByTime(SIGN_IN_HANDOFF_CLOSE_DELAY_MS));
        expect(close).toHaveBeenCalledTimes(1);
    });

    test("a tab the browser will not let us close asks the user straight away and never tries", () => {
        vi.spyOn(globalThis.history, "length", "get").mockReturnValue(4);
        const onContinue = vi.fn();
        render(<SignedInHandoff sourceLabel="WhatsApp" onContinue={onContinue} />);
        expect(screen.getByText("Close this window to return to WhatsApp, or select the button below to open Agent Mesh.")).toBeInTheDocument();

        act(() => vi.advanceTimersByTime(SIGN_IN_HANDOFF_CLOSE_DELAY_MS));
        expect(close).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole("button", { name: "Continue to Agent Mesh" }));
        expect(onContinue).toHaveBeenCalledTimes(1);
    });
});

describe("AlreadySignedInHandoff", () => {
    test("names the account and offers to switch or continue", () => {
        const onContinue = vi.fn();
        const onUseDifferentAccount = vi.fn();
        render(<AlreadySignedInHandoff sourceLabel="WhatsApp" account="ada@example.com" onContinue={onContinue} onUseDifferentAccount={onUseDifferentAccount} />);
        expect(screen.getByText("You're signed in as ada@example.com. If WhatsApp still asks you to sign in, this may not be the account your administrator registered.")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Use a different account" }));
        expect(onUseDifferentAccount).toHaveBeenCalledTimes(1);
        fireEvent.click(screen.getByRole("button", { name: "Continue to Agent Mesh" }));
        expect(onContinue).toHaveBeenCalledTimes(1);
    });

    test("reads cleanly when the account is unknown", () => {
        render(<AlreadySignedInHandoff sourceLabel="WhatsApp" account={null} onContinue={vi.fn()} onUseDifferentAccount={vi.fn()} />);
        expect(screen.getByText(/^If WhatsApp still asks you to sign in/)).toBeInTheDocument();
        expect(screen.queryByText(/signed in as/)).not.toBeInTheDocument();
    });
});
