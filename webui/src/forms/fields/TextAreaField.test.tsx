// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test } from "vitest";
import { z } from "zod";

import { AppForm, SubmitButton, TextAreaField, useAppForm } from "..";

const MESSAGE = "Description must be at least 10 characters";
const schema = z.object({ description: z.string().min(10, MESSAGE) });

function TestForm() {
    const form = useAppForm(schema, { defaultValues: { description: "" } });
    return (
        <AppForm form={form} onSubmit={() => undefined}>
            <TextAreaField name="description" label="Description" maxLength={1000} />
            <SubmitButton>Save</SubmitButton>
        </AppForm>
    );
}

describe("TextAreaField", () => {
    test("the counter stays visible when an error shows", async () => {
        render(<TestForm />);
        await userEvent.type(screen.getByLabelText("Description"), "abc");
        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        expect(await screen.findByText(MESSAGE)).toBeInTheDocument();
        expect(screen.getByText("3/1000")).toBeInTheDocument();
    });

    test("without an error only the counter shows", () => {
        render(<TestForm />);
        expect(screen.getByText("0/1000")).toBeInTheDocument();
        expect(screen.queryByText(MESSAGE)).not.toBeInTheDocument();
    });
});
