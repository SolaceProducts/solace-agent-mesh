// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";
import { z } from "zod";

import { AppForm, ChipListField, SubmitButton, useAppForm } from "..";

const MESSAGE = "Enter the whole domain, for example gmail.com";
const hasDot = (entry: string) => entry.includes(".");
const parseDomainEntry = (raw: string) => {
    const value = raw.trim().toLowerCase();
    if (value === "") return null;
    return hasDot(value) ? { value } : { value: raw.trim(), problem: MESSAGE };
};
const schema = z.object({ domains: z.array(z.string()).refine(entries => entries.every(hasDot), MESSAGE) });

function TestForm({ onSubmit }: { onSubmit: (values: z.output<typeof schema>) => void }) {
    const form = useAppForm(schema, { defaultValues: { domains: [] } });
    return (
        <AppForm form={form} onSubmit={onSubmit}>
            <ChipListField name="domains" label="Domains" parseEntry={parseDomainEntry} />
            <output data-testid="value">{JSON.stringify(form.watch("domains"))}</output>
            <SubmitButton>Save</SubmitButton>
        </AppForm>
    );
}

const value = () => screen.getByTestId("value").textContent;

describe("ChipListField", () => {
    test("an invalid entry shows the schema message as soon as it becomes a chip, and stays until removed", async () => {
        const onSubmit = vi.fn();
        render(<TestForm onSubmit={onSubmit} />);
        await userEvent.type(screen.getByLabelText("Domains"), "gmail{Enter}");

        expect(await screen.findByText(MESSAGE, { selector: "p" })).toBeInTheDocument();
        expect(screen.getByRole("listitem")).toHaveTextContent(`gmail${MESSAGE}`);
        await userEvent.click(screen.getByRole("button", { name: "Save" }));
        expect(onSubmit).not.toHaveBeenCalled();
        expect(screen.getByRole("listitem")).toBeInTheDocument();
    });

    test("removing the invalid chip with Backspace clears the message at once", async () => {
        render(<TestForm onSubmit={vi.fn()} />);
        await userEvent.type(screen.getByLabelText("Domains"), "a.com{Enter}gmail{Enter}");
        expect(await screen.findByText(MESSAGE, { selector: "p" })).toBeInTheDocument();

        await userEvent.keyboard("{Backspace}");
        expect(screen.queryByText(MESSAGE, { selector: "p" })).not.toBeInTheDocument();
    });

    test("commits a typed draft when the input loses focus", async () => {
        render(<TestForm onSubmit={vi.fn()} />);
        await userEvent.type(screen.getByLabelText("Domains"), "example.com");
        await userEvent.tab();
        expect(value()).toBe('["example.com"]');
    });

    test("removing the invalid chip lets the form submit", async () => {
        const onSubmit = vi.fn();
        render(<TestForm onSubmit={onSubmit} />);
        await userEvent.type(screen.getByLabelText("Domains"), "a.com, gmail{Enter}");
        await userEvent.click(screen.getByRole("button", { name: "Remove gmail" }));
        await userEvent.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({ domains: ["a.com"] }));
    });

    test("removing a chip with its button keeps focus in the field", async () => {
        render(<TestForm onSubmit={vi.fn()} />);
        await userEvent.type(screen.getByLabelText("Domains"), "a.com{Enter}b.com{Enter}");
        await userEvent.click(screen.getByRole("button", { name: "Remove a.com" }));
        expect(screen.getByLabelText("Domains")).toHaveFocus();
    });

    test("a paste after a finished entry adds a second entry", async () => {
        render(<TestForm onSubmit={vi.fn()} />);
        await userEvent.type(screen.getByLabelText("Domains"), "a.com");
        await userEvent.paste("b.org");
        expect(value()).toBe('["a.com","b.org"]');
    });

    test("Shift+Tab is not trapped by a draft", async () => {
        render(<TestForm onSubmit={vi.fn()} />);
        const input = screen.getByLabelText("Domains");
        await userEvent.type(input, "example.com");
        await userEvent.keyboard("{Shift>}{Tab}{/Shift}");
        expect(input).not.toHaveFocus();
    });

    test("a click on the box's empty space focuses the input", async () => {
        render(<TestForm onSubmit={vi.fn()} />);
        const input = screen.getByLabelText("Domains");
        await userEvent.type(input, "example.com{Enter}");

        input.blur();
        await userEvent.pointer({ keys: "[MouseLeft]", target: screen.getByRole("list") });
        expect(input).toHaveFocus();
    });

    test("Backspace in an empty input removes the last chip", async () => {
        render(<TestForm onSubmit={vi.fn()} />);
        await userEvent.type(screen.getByLabelText("Domains"), "a.com{Enter}b.org{Enter}");

        await userEvent.keyboard("{Backspace}");

        expect(screen.getByRole("listitem")).toHaveTextContent("a.com");
        expect(screen.queryByText("b.org")).not.toBeInTheDocument();
    });
});
