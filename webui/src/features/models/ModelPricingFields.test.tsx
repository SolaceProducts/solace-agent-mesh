// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { FormProvider, useForm } from "react-hook-form";
import { describe, expect, test, vi } from "vitest";

import { Button } from "@/ui";

import { EMPTY_PRICING_FORM, readPricingFromForm } from "./modelPricing";
import { ModelPricingFields } from "./ModelPricingFields";
import type { ModelFormData } from "./modelProviderUtils";

expect.extend(matchers);

function Harness({ onValid }: { onValid: (pricing: ReturnType<typeof readPricingFromForm>) => void }) {
    const methods = useForm<ModelFormData>({
        mode: "onSubmit",
        reValidateMode: "onChange",
        defaultValues: { pricing: EMPTY_PRICING_FORM } as Partial<ModelFormData>,
    });

    return (
        <FormProvider {...methods}>
            <form onSubmit={methods.handleSubmit(data => onValid(readPricingFromForm(data.pricing)))}>
                <ModelPricingFields />
                <Button type="submit">Save</Button>
            </form>
        </FormProvider>
    );
}

function renderFields() {
    const onValid = vi.fn();
    render(<Harness onValid={onValid} />);
    return { onValid };
}

const save = () => userEvent.click(screen.getByRole("button", { name: "Save" }));
const inputRate = () => screen.getByLabelText(/Input Price/);
const outputRate = () => screen.getByLabelText(/Output Price/);

describe("ModelPricingFields", () => {
    test("requires the output rate only once an input rate is entered", async () => {
        const { onValid } = renderFields();

        await userEvent.type(inputRate(), "3");
        await save();

        expect(onValid).not.toHaveBeenCalled();
        expect(await screen.findByText(/Required once input is priced/)).toBeInTheDocument();
    });

    test("a zero rate is accepted, because a free model is a priced model", async () => {
        const { onValid } = renderFields();

        await userEvent.type(inputRate(), "0");
        await userEvent.type(outputRate(), "0");
        await save();

        expect(onValid).toHaveBeenCalledWith({ input: "0.00", output: "0.00", cacheRead: null, cacheWrite: null });
    });

    test("refuses a rate with more precision than a price holds", async () => {
        const { onValid } = renderFields();

        await userEvent.type(inputRate(), "0.00001");
        await userEvent.type(outputRate(), "15");
        await save();

        expect(onValid).not.toHaveBeenCalled();
        expect(await screen.findByText(/At most 4 decimal places/)).toBeInTheDocument();
    });

    test("leaves a blank field blank when it loses focus", async () => {
        const { onValid } = renderFields();

        await userEvent.click(inputRate());
        await userEvent.tab();

        expect(inputRate()).toHaveValue("");

        await save();
        expect(onValid).toHaveBeenCalledWith({ input: null, output: null, cacheRead: null, cacheWrite: null });
    });

    test("submits the padded value unchanged", async () => {
        const { onValid } = renderFields();

        await userEvent.type(inputRate(), "0.1");
        await userEvent.type(outputRate(), "1000");
        await save();

        expect(onValid).toHaveBeenCalledWith({ input: "0.10", output: "1000.00", cacheRead: null, cacheWrite: null });
    });

    test("the input hint excludes cached tokens only once a cache price is entered", async () => {
        renderFields();
        const including = "Tokens sent to the model, including cached tokens";
        const excluding = "Tokens sent to the model, excluding cached tokens";

        expect(inputRate()).toHaveAccessibleDescription(including);

        await userEvent.click(screen.getByRole("checkbox"));
        expect(inputRate()).toHaveAccessibleDescription(including);

        await userEvent.type(screen.getByLabelText(/Cache Read Price/), "0.3");
        expect(inputRate()).toHaveAccessibleDescription(excluding);

        await userEvent.click(screen.getByRole("checkbox"));
        expect(inputRate()).toHaveAccessibleDescription(including);
    });

    test("collapsing the breakdown drops the cache rates rather than saving them", async () => {
        const { onValid } = renderFields();

        await userEvent.type(inputRate(), "3");
        await userEvent.type(outputRate(), "15");
        await userEvent.click(screen.getByRole("checkbox"));
        await userEvent.type(screen.getByLabelText(/Cache Read Price/), "0.3");
        await userEvent.type(screen.getByLabelText(/Cache Write Price/), "3.75");
        await userEvent.click(screen.getByRole("checkbox"));
        await save();

        expect(onValid).toHaveBeenCalledWith({ input: "3.00", output: "15.00", cacheRead: null, cacheWrite: null });
    });

    test("refuses cache rates while the rates they deviate from are blank", async () => {
        const { onValid } = renderFields();

        await userEvent.click(screen.getByRole("checkbox"));
        await userEvent.type(screen.getByLabelText(/Cache Read Price/), "0.3");
        await userEvent.type(screen.getByLabelText(/Cache Write Price/), "3.75");
        await save();

        expect(onValid).not.toHaveBeenCalled();
        expect(await screen.findAllByText(/Price input and output before adding cache prices/)).not.toHaveLength(0);
    });

    test("accepts the same cache rates once input and output are priced", async () => {
        const { onValid } = renderFields();

        await userEvent.type(inputRate(), "3");
        await userEvent.type(outputRate(), "15");
        await userEvent.click(screen.getByRole("checkbox"));
        await userEvent.type(screen.getByLabelText(/Cache Read Price/), "0.3");
        await userEvent.type(screen.getByLabelText(/Cache Write Price/), "3.75");
        await save();

        expect(onValid).toHaveBeenCalledWith({ input: "3.00", output: "15.00", cacheRead: "0.30", cacheWrite: "3.75" });
    });
});
