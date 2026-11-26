// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { FormProvider, useForm } from "react-hook-form";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { z } from "zod";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import type { ConnectorSchema } from "@/types";

import { ConnectorEdit } from "./ConnectorEdit";

function Wrapper({ children, defaultValues = {} }: { children: React.ReactNode; defaultValues?: Record<string, unknown> }) {
    const methods = useForm({ defaultValues });
    return <FormProvider {...methods}>{children}</FormProvider>;
}

describe("ConnectorEdit — Event Mesh broker fields", () => {
    const schemaInfo: ConnectorSchema = {
        type: "event_mesh",
        subtype: "solace",
        name: "Solace Event Mesh",
        description: "test",
        schemaValidator: z.object({}),
        fieldSchema: {
            broker_url: {
                type: "string",
                label: "Host URI",
                required: true,
                secret: false,
                order: 1,
            },
        },
    };

    test("renders broker fields as editable inputs without a connection-type radio", () => {
        render(
            <Wrapper defaultValues={{ name: "", description: "", values: { broker_url: "" } }}>
                <ConnectorEdit schemaInfo={schemaInfo} isNew onSubmit={() => {}} />
            </Wrapper>
        );

        expect(screen.getByText("Connector Name")).toBeInTheDocument();
        expect(screen.getByText("Host URI")).toBeInTheDocument();
        expect(screen.queryByRole("radio")).toBeNull();
        expect(screen.queryByText("Configured Event Broker")).toBeNull();
    });

    test("renders the Connector Name field before the Type value", () => {
        render(
            <Wrapper defaultValues={{ name: "", description: "", values: { broker_url: "" } }}>
                <ConnectorEdit schemaInfo={schemaInfo} isNew onSubmit={() => {}} />
            </Wrapper>
        );

        const html = document.body.innerHTML;
        expect(html.indexOf("Connector Name")).toBeGreaterThanOrEqual(0);
        expect(html.indexOf("Connector Name")).toBeLessThan(html.indexOf("Solace Event Mesh"));
    });
});

describe("ConnectorEdit — validation friction", () => {
    let trackSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    const schemaInfo: ConnectorSchema = {
        type: "event_mesh",
        subtype: "solace",
        name: "Solace Event Mesh",
        description: "test",
        schemaValidator: z.object({}),
        fieldSchema: {
            broker_url: { type: "string", label: "Host URI", required: true, secret: false, order: 1 },
        },
    };

    test("a rejected submit reports the fields that blocked it, and no value the user typed", async () => {
        const onSubmit = vi.fn();
        render(
            <Wrapper defaultValues={{ name: "", description: "", values: { broker_url: "" } }}>
                <ConnectorEdit schemaInfo={schemaInfo} isNew onSubmit={onSubmit} />
            </Wrapper>
        );

        fireEvent.submit(document.getElementById("connector-form")!);

        await waitFor(() => expect(trackSpy).toHaveBeenCalledWith("form:validation_error", expect.objectContaining({ form: "connector" })));
        expect(onSubmit).not.toHaveBeenCalled();
        for (const [, properties] of trackSpy.mock.calls) {
            expect(Object.keys(properties as object).sort()).toEqual(["errorType", "field", "form", "path"]);
        }
    });
});
