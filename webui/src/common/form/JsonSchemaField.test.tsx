// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Controller, useForm } from "react-hook-form";
import { describe, expect, test } from "vitest";

import { JsonSchemaField } from "./JsonSchemaField";

const JsonSchemaFieldWrapper = () => {
    const { control } = useForm<{ schema?: unknown }>({ defaultValues: {} });
    return (
        <>
            <Controller name="schema" control={control} render={({ field }) => <JsonSchemaField label="Input Schema (JSON)" field={field} />} />
            <button type="button">Elsewhere</button>
        </>
    );
};

describe("JsonSchemaField", () => {
    test("keeps invalid JSON text with quotes unchanged each time the field is blurred", async () => {
        const user = userEvent.setup();
        render(<JsonSchemaFieldWrapper />);
        const textbox = screen.getByRole("textbox");
        const typed = '{"type": "object", "properties": }';

        await user.click(textbox);
        await user.paste(typed);
        await user.click(screen.getByRole("button", { name: "Elsewhere" }));
        expect(textbox).toHaveValue(typed);

        await user.click(textbox);
        await user.click(screen.getByRole("button", { name: "Elsewhere" }));
        expect(textbox).toHaveValue(typed);
        expect(screen.getByText("Invalid JSON")).toBeInTheDocument();
    });

    test("keeps a non-object JSON value unchanged on blur and reports it", async () => {
        const user = userEvent.setup();
        render(<JsonSchemaFieldWrapper />);
        const textbox = screen.getByRole("textbox");
        const typed = '"a string"';

        await user.click(textbox);
        await user.paste(typed);
        await user.click(screen.getByRole("button", { name: "Elsewhere" }));
        await user.click(textbox);
        await user.click(screen.getByRole("button", { name: "Elsewhere" }));

        expect(textbox).toHaveValue(typed);
        expect(screen.getByText("Schema must be a JSON object")).toBeInTheDocument();
    });

    test("leaves a valid JSON object as typed on blur and shows no error", async () => {
        const user = userEvent.setup();
        render(<JsonSchemaFieldWrapper />);
        const textbox = screen.getByRole("textbox");

        await user.click(textbox);
        await user.paste('{"type":"object"}');
        await user.click(screen.getByRole("button", { name: "Elsewhere" }));

        expect(textbox).toHaveValue('{"type":"object"}');
        expect(screen.queryByText("Invalid JSON")).not.toBeInTheDocument();
    });
});
