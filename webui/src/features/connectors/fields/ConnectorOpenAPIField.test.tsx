// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { type FieldValues, FormProvider, useForm, type UseFormReturn } from "react-hook-form";
import { describe, expect, it, vi } from "vitest";

import { ConnectorOpenAPIField } from "./ConnectorOpenAPIField";

vi.mock("@/common/product-analytics/errorCategory", () => ({ classifyErrorCategory: () => "unknown" }));

const VALID_YAML = 'openapi: "3.0.3"\ninfo:\n  title: Pets\n  description: The pet store\npaths:\n  /pets:\n    get:\n      operationId: listPets\n';

function specFile(text: string, type: string): FileList {
    const file = new File([text], "spec", { type });
    Object.defineProperty(file, "text", { value: () => Promise.resolve(text) });
    return Object.assign([file], { item: (index: number) => (index === 0 ? file : null) }) as unknown as FileList;
}

let form: UseFormReturn<FieldValues>;

function Harness({ children, resources }: { children: ReactNode; resources: FileList }) {
    form = useForm<FieldValues>({ defaultValues: { name: "", description: "", resources, values: {} } });
    return <FormProvider {...form}>{children}</FormProvider>;
}

function renderField(resources: FileList) {
    return render(
        <Harness resources={resources}>
            <ConnectorOpenAPIField isNew={true} />
        </Harness>
    );
}

describe("ConnectorOpenAPIField", () => {
    it("fills the form from a valid specification", async () => {
        renderField(specFile(VALID_YAML, "application/yaml"));

        await waitFor(() => expect(form.getValues("tools")).toHaveLength(1));
        expect(form.getValues("name")).toBe("Pets");
        expect(form.getValues("description")).toBe("The pet store");
        expect(screen.queryByText("OpenAPI Parsing Error")).toBeNull();
    });

    it.each([
        ["an unquoted version", "openapi: 3.1\ninfo:\n  title: Pets\npaths: {}\n", "application/yaml"],
        ["an external parameter reference", JSON.stringify({ openapi: "3.0.0", info: { title: "Pets" }, paths: { "/pets": { get: { parameters: [{ $ref: "https://example.test/p.yaml#/limit" }] } } } }), "application/json"],
        ["a null path item", JSON.stringify({ openapi: "3.0.0", info: { title: "Pets" }, paths: { "/pets": null } }), "application/json"],
        ["parameters that are not a list", JSON.stringify({ openapi: "3.0.0", info: { title: "Pets" }, paths: { "/pets": { get: { parameters: "limit" } } } }), "application/json"],
        ["a broken file", "{not json", "application/json"],
    ])("opens the OpenAPI Parsing Error dialog and clears the file for %s", async (_label, text, type) => {
        renderField(specFile(text, type));

        expect(await screen.findByText("OpenAPI Parsing Error")).toBeInTheDocument();
        await waitFor(() => expect(form.getValues("resources")).toBeNull());
        expect(form.getValues("openAPISpec")).toBeNull();
        expect(form.getValues("tools")).toEqual([]);
    });

    it("keeps the unsupported-version dialog for a Swagger 2.0 file", async () => {
        renderField(specFile(JSON.stringify({ swagger: "2.0", info: { title: "Old" }, paths: {} }), "application/json"));

        expect(await screen.findByText("Unsupported OpenAPI Version")).toBeInTheDocument();
        await waitFor(() => expect(form.getValues("resources")).toBeNull());
    });

    it("does not crash on a title or description that is not text", async () => {
        renderField(specFile(JSON.stringify({ openapi: "3.0.0", info: { title: 42, description: { text: "x" } }, paths: {} }), "application/json"));

        await waitFor(() => expect(form.getValues("openAPISpec")).not.toBeNull());
        expect(form.getValues("name")).toBe("");
        expect(screen.queryByText("OpenAPI Parsing Error")).toBeNull();
    });
});
