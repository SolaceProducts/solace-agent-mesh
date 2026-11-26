// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";
import { z } from "zod";

import { AppForm, SelectField, useAppForm } from "..";

const OPTIONS = [
    { value: "a", label: "Alpha" },
    { value: "b", label: "Beta" },
];

function Form({ initial = "" }: { initial?: string }) {
    const form = useAppForm(z.object({ space: z.string() }), { defaultValues: { space: initial } });
    return (
        <AppForm form={form} onSubmit={vi.fn()}>
            <SelectField name="space" label="Space" options={OPTIONS} placeholder="Choose a space" />
            <output data-testid="value">{form.watch("space")}</output>
        </AppForm>
    );
}

describe("SelectField", () => {
    test("labels the select and writes the picked value to the form", async () => {
        render(<Form />);
        await userEvent.click(screen.getByRole("combobox", { name: "Space" }));
        await userEvent.click(await screen.findByRole("option", { name: "Beta" }));
        expect(screen.getByTestId("value").textContent).toBe("b");
    });

    test("shows a stored value that is not among the options", () => {
        render(<Form initial="gone-agent" />);
        expect(screen.getByRole("combobox", { name: "Space" })).toHaveTextContent("gone-agent");
    });

    test("shows the label of a stored value that is among the options", () => {
        render(<Form initial="a" />);
        expect(screen.getByRole("combobox", { name: "Space" })).toHaveTextContent("Alpha");
    });
});
