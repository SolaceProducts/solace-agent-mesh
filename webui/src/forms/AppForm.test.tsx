// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useController } from "react-hook-form";
import { describe, expect, test, vi } from "vitest";
import { z } from "zod";

import { ApiV2Error } from "@/api/common/v2";

import { AppForm, SubmitButton, useAppForm } from ".";

const schema = z.object({ name: z.string().min(1, "Name is required") });

function NameInput() {
    const { field, fieldState } = useController<z.input<typeof schema>, "name">({ name: "name" });
    return (
        <>
            <input aria-label="Name" {...field} />
            {fieldState.error && <p>{fieldState.error.message}</p>}
        </>
    );
}

function TestForm({ onSubmit }: { onSubmit: (values: z.output<typeof schema>) => Promise<void> }) {
    const form = useAppForm(schema, { defaultValues: { name: "" } });
    return (
        <AppForm form={form} onSubmit={onSubmit}>
            <NameInput />
            <SubmitButton>Save</SubmitButton>
        </AppForm>
    );
}

describe("AppForm", () => {
    test("submit stays enabled while the form is invalid and shows client errors", async () => {
        const onSubmit = vi.fn();
        render(<TestForm onSubmit={onSubmit} />);

        const save = screen.getByRole("button", { name: "Save" });
        expect(save).toBeEnabled();
        await userEvent.click(save);

        expect(await screen.findByText("Name is required")).toBeInTheDocument();
        expect(onSubmit).not.toHaveBeenCalled();
        expect(save).toBeEnabled();
    });

    test("a thrown API error lands on its field and the rest in a banner with the error id", async () => {
        const onSubmit = vi.fn().mockRejectedValue(new ApiV2Error(422, "Validation failed", { errorId: "e9", validationDetails: { name: ["already taken"], other: ["broken"] } }));
        render(<TestForm onSubmit={onSubmit} />);

        await userEvent.type(screen.getByLabelText("Name"), "bot");
        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        expect(await screen.findByText("already taken")).toBeInTheDocument();
        const banner = await screen.findByRole("alert");
        expect(banner).toHaveTextContent("other: broken");
        expect(banner).toHaveTextContent("Error ID: e9");
        await waitFor(() => expect(screen.getByRole("button", { name: "Save" })).toBeEnabled());
    });

    test("a submit that fails client validation clears the previous server banner", async () => {
        const onSubmit = vi.fn().mockRejectedValue(new ApiV2Error(500, "Server exploded", { errorId: "e1" }));
        render(<TestForm onSubmit={onSubmit} />);

        await userEvent.type(screen.getByLabelText("Name"), "bot");
        await userEvent.click(screen.getByRole("button", { name: "Save" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Server exploded");

        await userEvent.clear(screen.getByLabelText("Name"));
        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        expect(await screen.findByText("Name is required")).toBeInTheDocument();
        expect(screen.queryByText("Server exploded")).not.toBeInTheDocument();
        expect(onSubmit).toHaveBeenCalledTimes(1);
    });

    test("server errors that all land on fields move focus to the first of them", async () => {
        const onSubmit = vi.fn().mockRejectedValue(new ApiV2Error(422, "Validation failed", { validationDetails: { name: ["already taken"] } }));
        render(<TestForm onSubmit={onSubmit} />);

        await userEvent.type(screen.getByLabelText("Name"), "bot");
        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        expect(await screen.findByText("already taken")).toBeInTheDocument();
        await waitFor(() => expect(screen.getByLabelText("Name")).toHaveFocus());
    });

    test("a server error shown in the banner moves focus to the banner", async () => {
        const onSubmit = vi.fn().mockRejectedValue(new ApiV2Error(409, "Unable to create entrypoint", { errorId: "e2" }));
        render(<TestForm onSubmit={onSubmit} />);

        await userEvent.type(screen.getByLabelText("Name"), "bot");
        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        expect(await screen.findByRole("alert")).toHaveTextContent("Unable to create entrypoint");
        await waitFor(() => expect(screen.getByTestId("form-error-banner")).toHaveFocus());
    });
});
