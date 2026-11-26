// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement } from "react";
import { act, render } from "@testing-library/react";
import { Controller, useForm, type UseFormReturn } from "react-hook-form";
import { describe, expect, test } from "vitest";

import { ApiV2Error } from "@/api/common/v2";

import { applyServerErrors } from "./applyServerErrors";

interface Fields {
    name: string;
    values: { allowed_email_domains: string; override: string };
}

type Shown = Partial<Record<"name" | "domains", boolean>>;

const NOT_A_DOMAIN = 'entry "gmail" is not a domain';

function renderForm(initial: Shown = { name: true, domains: true }) {
    const ref: { form: UseFormReturn<Fields> | null } = { form: null };
    function Harness({ shown }: { shown: Shown }) {
        const form = useForm<Fields>({ defaultValues: { name: "", values: { allowed_email_domains: "", override: "fallback" } } });
        ref.form = form;
        return createElement(
            "form",
            null,
            shown.name && createElement(Controller<Fields, "name">, { control: form.control, name: "name", render: ({ field }) => createElement("input", { "aria-label": "name", ...field }) }),
            shown.domains &&
                createElement(Controller<Fields, "values.allowed_email_domains">, {
                    control: form.control,
                    name: "values.allowed_email_domains",
                    render: ({ field }) => createElement("input", { "aria-label": "domains", ...field }),
                })
        );
    }
    const view = render(createElement(Harness, { shown: initial }));
    return {
        form: () => ref.form!,
        show: (shown: Shown) => view.rerender(createElement(Harness, { shown })),
    };
}

function apply(form: UseFormReturn<Fields>, error: unknown) {
    let rest: ReturnType<typeof applyServerErrors> = null;
    act(() => {
        rest = applyServerErrors(form, error);
    });
    return rest as ReturnType<typeof applyServerErrors>;
}

describe("applyServerErrors", () => {
    test("maps bare top-level keys and joins multiple messages", () => {
        const { form } = renderForm();

        const rest = apply(form(), new ApiV2Error(422, "Validation failed", { validationDetails: { name: ["is required", "is too short"] } }));

        expect(form().getFieldState("name").error).toMatchObject({ type: "server", message: "is required is too short" });
        expect(rest).toBeNull();
    });

    test("returns unmapped keys as banner with errorId", () => {
        const { form } = renderForm();
        const error = new ApiV2Error(422, "Validation failed: values.unknown_field: not allowed", {
            errorId: "e1",
            validationDetails: { "values.unknown_field": ["not allowed"], "values.allowed_email_domains": [NOT_A_DOMAIN] },
        });

        const rest = apply(form(), error);

        expect(rest).toMatchObject({ errorId: "e1" });
        expect(rest!.message).toContain("values.unknown_field");
        expect(rest!.message).toContain("not allowed");
        expect(rest!.message).not.toContain(NOT_A_DOMAIN);
        expect(form().getFieldState("values.allowed_email_domains").error?.message).toBe(NOT_A_DOMAIN);
    });

    test("a field with a default value but no rendered input goes to the banner", () => {
        const { form } = renderForm();

        const rest = apply(form(), new ApiV2Error(422, "Validation failed", { errorId: "e4", validationDetails: { "values.override": ["must be set"] } }));

        expect(rest).not.toBeNull();
        expect(rest!.message).toContain("values.override: must be set");
    });

    test("a field rendered and then unmounted goes to the banner", () => {
        const { form, show } = renderForm({ name: true, domains: true });
        show({ name: true, domains: false });

        const rest = apply(form(), new ApiV2Error(422, "Validation failed", { validationDetails: { "values.allowed_email_domains": [NOT_A_DOMAIN] } }));

        expect(rest).not.toBeNull();
        expect(rest!.message).toContain(NOT_A_DOMAIN);
    });

    test("when the server names a path that is a group of fields, not an input, the banner shows it", () => {
        const { form } = renderForm();

        const rest = apply(form(), new ApiV2Error(422, "Validation failed", { validationDetails: { values: ["is malformed"] } }));

        expect(rest).not.toBeNull();
        expect(rest!.message).toContain("values: is malformed");
    });

    test("an API error without details becomes the banner", () => {
        const { form } = renderForm();

        expect(applyServerErrors(form(), new ApiV2Error(500, "Server exploded", { errorId: "e3" }))).toEqual({ message: "Server exploded", errorId: "e3" });
    });

    test("non-API error returns its message", () => {
        const { form } = renderForm();

        expect(applyServerErrors(form(), new Error("Network down"))).toEqual({ message: "Network down" });
    });
});
