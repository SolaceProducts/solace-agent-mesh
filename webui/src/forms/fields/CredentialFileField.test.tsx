// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";
import { z } from "zod";

import { AppForm, CredentialFileField, SubmitButton, useAppForm } from "..";

const REQUIRED = "Upload the key file";
const NOT_A_KEY = "Not a key file";

const keyProblem = (text: string) => (text.includes('"client_email"') ? null : NOT_A_KEY);
const schema = z.object({
    key: z
        .string()
        .min(1, REQUIRED)
        .refine(text => keyProblem(text) === null, NOT_A_KEY),
});

function TestForm({ hasStoredValue = false }: { hasStoredValue?: boolean }) {
    const form = useAppForm(schema, { defaultValues: { key: "" } });
    return (
        <AppForm form={form} onSubmit={() => undefined}>
            <CredentialFileField name="key" label="Key" validate={keyProblem} accept=".json,application/json" hasStoredValue={hasStoredValue} />
            <output data-testid="value">{form.watch("key").length}</output>
            <button type="button" onClick={() => form.reset({ key: "" })}>
                Reset form
            </button>
            <SubmitButton>Save</SubmitButton>
        </AppForm>
    );
}

const VALID = '{"type":"service_account","client_email":"a@p.iam.gserviceaccount.com"}';

const dropFile = (contents: string, name = "my-key.json") => fireEvent.drop(screen.getByTestId("credential-drop-zone"), { dataTransfer: { files: [new File([contents], name, { type: "application/json" })] } });

describe("CredentialFileField", () => {
    test("a valid key file shows a card with its file name, and Remove clears it", async () => {
        render(<TestForm />);
        dropFile(VALID);

        expect(await screen.findByText("my-key.json")).toBeInTheDocument();
        expect(screen.getByTestId("value")).toHaveTextContent(String(VALID.length));
        await userEvent.click(screen.getByRole("button", { name: "Remove Key" }));
        expect(screen.getByTestId("value")).toHaveTextContent("0");
        expect(screen.getByRole("button", { name: "Upload File for Key" })).toHaveFocus();
    });

    test("an invalid key file shows the check's message and no card", async () => {
        render(<TestForm />);
        dropFile('{"type":"user"}', "wrong.json");

        expect(await screen.findByText(NOT_A_KEY)).toBeInTheDocument();
        expect(screen.queryByText("wrong.json")).not.toBeInTheDocument();
        expect(screen.getByTestId("credential-drop-zone")).toBeInTheDocument();
    });

    test("removing a valid key before dropping an invalid one leaves no key behind", async () => {
        render(<TestForm />);
        dropFile(VALID);
        await userEvent.click(await screen.findByRole("button", { name: "Remove Key" }));
        dropFile('{"type":"user"}', "wrong.json");

        expect(await screen.findByText(NOT_A_KEY)).toBeInTheDocument();
        expect(screen.queryByText("my-key.json")).not.toBeInTheDocument();
    });

    test("a saved key can be replaced, and kept again until a new file is chosen", async () => {
        render(<TestForm hasStoredValue />);
        expect(screen.getByText("A key is saved")).toBeInTheDocument();

        await userEvent.click(screen.getByRole("button", { name: "Replace Key" }));
        await userEvent.click(screen.getByRole("button", { name: "Keep the saved key" }));
        expect(screen.getByText("A key is saved")).toBeInTheDocument();
        expect(screen.getByTestId("value")).toHaveTextContent("0");

        await userEvent.click(screen.getByRole("button", { name: "Replace Key" }));
        dropFile(VALID);
        await screen.findByText("my-key.json");
        expect(screen.queryByRole("button", { name: "Keep the saved key" })).not.toBeInTheDocument();
    });

    test("after Replace, a bad file can still be undone by keeping the saved key", async () => {
        render(<TestForm hasStoredValue />);
        await userEvent.click(screen.getByRole("button", { name: "Replace Key" }));
        dropFile('{"type":"user"}', "wrong.json");
        expect(await screen.findByText(NOT_A_KEY)).toBeInTheDocument();

        await userEvent.click(screen.getByRole("button", { name: "Keep the saved key" }));
        expect(screen.getByText("A key is saved")).toBeInTheDocument();
        expect(screen.getByTestId("value")).toHaveTextContent("0");
        expect(screen.queryByText(NOT_A_KEY)).not.toBeInTheDocument();
    });

    test("a key file that cannot be read shows its message", async () => {
        render(<TestForm />);
        const file = new File([VALID], "key.json", { type: "application/json" });
        vi.spyOn(file, "text").mockRejectedValue(new DOMException("The file could not be read.", "NotReadableError"));

        fireEvent.drop(screen.getByTestId("credential-drop-zone"), { dataTransfer: { files: [file] } });

        expect(await screen.findByRole("alert")).toHaveTextContent("That file could not be read. Try again.");
    });

    test("the saved card returns after the form is reset following a replacement", async () => {
        render(<TestForm hasStoredValue />);
        await userEvent.click(screen.getByRole("button", { name: "Replace Key" }));
        dropFile(VALID);
        await screen.findByText("my-key.json");

        await userEvent.click(screen.getByRole("button", { name: "Reset form" }));
        expect(screen.getByText("A key is saved")).toBeInTheDocument();
    });
});
