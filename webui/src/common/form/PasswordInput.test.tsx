// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { FormProvider, useForm } from "react-hook-form";
import { describe, expect, test } from "vitest";

import { PasswordInput } from "./PasswordInput";

const PasswordInputWrapper = ({ hasStoredValue = false, disabled = false }: { hasStoredValue?: boolean; disabled?: boolean }) => {
    const methods = useForm({ defaultValues: { password: "" } });
    return (
        <FormProvider {...methods}>
            <PasswordInput name="password" control={methods.control} hasStoredValue={hasStoredValue} disabled={disabled} />
        </FormProvider>
    );
};

describe("PasswordInput", () => {
    test("renders input field with password type by default", () => {
        render(<PasswordInputWrapper />);
        const input = screen.getByRole("textbox") as HTMLInputElement;
        expect(input).toHaveAttribute("type", "password");
    });

    test("toggles to text type when show button is clicked", async () => {
        const user = userEvent.setup();
        render(<PasswordInputWrapper />);
        const input = screen.getByRole("textbox") as HTMLInputElement;
        const button = screen.getByRole("button");

        expect(input).toHaveAttribute("type", "password");
        await user.click(button);
        expect(input).toHaveAttribute("type", "text");
    });

    test("renders toggle button with show icon when password is hidden", () => {
        render(<PasswordInputWrapper />);
        const button = screen.getByRole("button");
        expect(button).toHaveAttribute("aria-label", "Show password");
    });

    test("toggles aria-label when clicked", async () => {
        const user = userEvent.setup();
        render(<PasswordInputWrapper />);
        const button = screen.getByRole("button");

        expect(button).toHaveAttribute("aria-label", "Show password");
        await user.click(button);
        expect(button).toHaveAttribute("aria-label", "Hide password");
    });

    test("disables input and button when disabled prop is true", () => {
        render(<PasswordInputWrapper disabled={true} />);
        const input = screen.getByRole("textbox");
        const button = screen.getByRole("button");
        expect(input).toBeDisabled();
        expect(button).toBeDisabled();
    });

    test("sets autocomplete to new-password", () => {
        render(<PasswordInputWrapper />);
        const input = screen.getByRole("textbox");
        expect(input).toHaveAttribute("autoComplete", "new-password");
    });

    test("shows bullet placeholder when hasStoredValue is true and field is empty", () => {
        render(<PasswordInputWrapper hasStoredValue={true} />);
        const input = screen.getByRole("textbox") as HTMLInputElement;
        expect(input).toHaveAttribute("placeholder", "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022");
    });

    test("eye toggle is hidden when hasStoredValue and no user input", () => {
        render(<PasswordInputWrapper hasStoredValue={true} />);
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });
});
