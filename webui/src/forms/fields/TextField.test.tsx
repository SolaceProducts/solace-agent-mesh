// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";
import { z } from "zod";

import { AppForm, TextField, useAppForm } from "..";

function TestForm({ initial = "", copyable, prefix, onSubmit = () => undefined }: { initial?: string; copyable?: boolean; prefix?: string; onSubmit?: (values: { name: string }) => void }) {
    const form = useAppForm(z.object({ name: z.string() }), { defaultValues: { name: initial } });
    return (
        <AppForm form={form} onSubmit={onSubmit}>
            <TextField name="name" label="Name" copyable={copyable} prefix={prefix} />
            <button type="submit">Save</button>
        </AppForm>
    );
}

describe("TextField copy button", () => {
    afterEach(() => vi.unstubAllGlobals());

    test("copies the field's value", async () => {
        const writeText = vi.fn().mockResolvedValue(undefined);
        vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
        render(<TestForm initial="service-1@gcp-sa-gsuiteaddons.iam.gserviceaccount.com" copyable />);

        await userEvent.click(screen.getByRole("button", { name: "Copy Name" }));

        await waitFor(() => expect(writeText).toHaveBeenCalledWith("service-1@gcp-sa-gsuiteaddons.iam.gserviceaccount.com"));
    });

    test("is disabled while the field is empty", () => {
        render(<TestForm copyable />);
        expect(screen.getByRole("button", { name: "Copy Name" })).toBeDisabled();
    });

    test("is absent unless asked for", () => {
        render(<TestForm initial="x" />);
        expect(screen.queryByRole("button", { name: "Copy Name" })).not.toBeInTheDocument();
    });
});

describe("TextField prefix", () => {
    test("shows the prefix beside the box and leaves it out of the value", async () => {
        const onSubmit = vi.fn();
        render(<TestForm prefix="https://host/gw/" onSubmit={onSubmit} />);
        expect(screen.getByText("https://host/gw/")).toBeInTheDocument();

        await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "abc");
        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({ name: "abc" }));
    });
});
