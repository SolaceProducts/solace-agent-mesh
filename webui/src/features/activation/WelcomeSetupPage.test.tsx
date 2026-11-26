// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { ModelSetupRequestContext } from "./ModelSetupRequest";
import { WelcomeSetupPage } from "./WelcomeSetupPage";

vi.mock("@/common/dialogs/ModelSetupForm", () => ({
    default: ({ onSuccess, hideCancel, submitLabel }: { onSuccess: () => void; hideCancel?: boolean; submitLabel?: string }) => (
        <div>
            {!hideCancel && <button type="button">Cancel</button>}
            <button type="button" onClick={onSuccess}>
                {submitLabel}
            </button>
        </div>
    ),
}));

describe("WelcomeSetupPage", () => {
    test("closes from its own button, with no cancel in the form", async () => {
        const onDone = vi.fn();
        render(<WelcomeSetupPage onDone={onDone} />);

        expect(await screen.findByRole("button", { name: "Complete Setup" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Close" }));

        expect(onDone).toHaveBeenCalledTimes(1);
    });

    test("tells the gate it is on screen once its form is", async () => {
        const shown = vi.fn();
        render(
            <ModelSetupRequestContext value={{ requested: true, shown, appShown: vi.fn(), clear: vi.fn() }}>
                <WelcomeSetupPage onDone={vi.fn()} />
            </ModelSetupRequestContext>
        );

        await screen.findByRole("button", { name: "Complete Setup" });
        expect(shown).toHaveBeenCalled();
    });

    test("is done once the model is set up", async () => {
        const onDone = vi.fn();
        render(<WelcomeSetupPage onDone={onDone} />);

        await userEvent.click(await screen.findByRole("button", { name: "Complete Setup" }));

        expect(onDone).toHaveBeenCalledTimes(1);
    });
});
