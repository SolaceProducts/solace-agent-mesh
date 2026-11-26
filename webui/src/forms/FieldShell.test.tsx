// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test } from "vitest";
import { z } from "zod";

import { AppForm, CheckboxField, SubmitButton, TextAreaField, TextField, useAppForm } from ".";

const schema = z.object({
    name: z.string().min(1, "Name is required"),
    notes: z.string().min(1, "Notes are required"),
    agree: z.boolean().refine(value => value, "Agreement is required"),
});

function TestForm() {
    const form = useAppForm(schema, { defaultValues: { name: "", notes: "", agree: false } });
    return (
        <AppForm form={form} onSubmit={() => undefined}>
            <TextField name="name" label="Name" description="Your name" docsHref="https://example.com/name" />
            <TextAreaField name="notes" label="Notes" maxLength={100} docsHref="https://example.com/notes" />
            <CheckboxField name="agree" label="Agree" docsHref="https://example.com/agree" />
            <SubmitButton>Save</SubmitButton>
        </AppForm>
    );
}

const CASES = [
    { label: "Name", error: "Name is required", role: "textbox" },
    { label: "Notes", error: "Notes are required", role: "textbox" },
    { label: "Agree", error: "Agreement is required", role: "checkbox" },
] as const;

describe("FieldShell accessibility", () => {
    test.each(CASES)("$label: label names the focusable control, error is announced, docs link stays", async ({ label, error, role }) => {
        render(<TestForm />);
        const control = () => screen.getByRole(role, { name: label });
        expect(control()).not.toHaveAttribute("aria-invalid", "true");

        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(control()).toHaveAttribute("aria-invalid", "true"));
        expect(control()).toHaveAccessibleDescription(expect.stringContaining(error));
        const field = screen.getByTestId(`field-${label.toLowerCase()}`);
        expect(within(field).getByRole("link", { name: /Learn more/ })).toBeInTheDocument();
    });
});

function RequiredForm() {
    const form = useAppForm(schema, { defaultValues: { name: "", notes: "", agree: false } });
    return (
        <AppForm form={form} onSubmit={() => undefined}>
            <TextField name="name" label="Name" description="Your name" required />
            <CheckboxField name="agree" label="Agree" />
        </AppForm>
    );
}

describe("FieldShell anatomy matches the app's other forms", () => {
    test("a required field's star is decoration: the control is named without it and marked required", () => {
        render(<RequiredForm />);
        const input = screen.getByRole("textbox", { name: "Name" });
        expect(input).toHaveAttribute("aria-required", "true");
    });
});
