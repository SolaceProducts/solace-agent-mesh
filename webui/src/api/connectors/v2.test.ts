// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { Connector } from "@/types";

import {
    connectorResourcePathV2,
    createConnectorFromFormV2,
    createConnectorV2,
    deleteConnectorV2,
    detectConnectorAuthV2,
    diffConnectorSecretsV2,
    getAllConnectorsV2,
    getConnectorSchemasV2,
    getConnectorSpecificationV2,
    getConnectorsV2,
    getConnectorV2,
    testConnectorConnectionV2,
    toConnectorUIModel,
    updateConnectorV2,
    uploadConnectorResourceV2,
} from "./v2";

const connectorV2 = (overrides: Record<string, unknown> = {}) => ({
    id: "a",
    type: "connector",
    connectorType: "api",
    connectorSubtype: "openapi",
    name: "Orders API",
    description: "The orders service",
    values: { base_url: "https://api.example.com" },
    agentCount: 0,
    hasResource: false,
    contentHash: null,
    warnings: [],
    agentRegistrations: [],
    createdBy: "u1",
    updatedBy: null,
    createdTime: "2026-10-19T12:00:00.000Z",
    updatedTime: null,
    ...overrides,
});

const page = (data: unknown[], nextPage: number | null = null) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage, totalPages: 1 } } });

describe("connectors on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const respond = (status: number, body?: unknown) => fetchMock.mockResolvedValueOnce(new Response(body === undefined ? null : JSON.stringify(body), { status }));
    const call = (n: number) => ({ url: fetchMock.mock.calls[n][0] as string, method: fetchMock.mock.calls[n][1]?.method, body: fetchMock.mock.calls[n][1]?.body });
    const jsonBody = (n: number) => JSON.parse(call(n).body as string);

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    describe("reads", () => {
        test("getAllConnectorsV2 reads every page, not just the first", async () => {
            respond(200, page([connectorV2({ id: "a", name: "A" })], 2));
            respond(200, page([connectorV2({ id: "b", name: "B" })]));

            const all = await getAllConnectorsV2();

            expect(all.map(c => c.id)).toEqual(["a", "b"]);
            expect(fetchMock).toHaveBeenCalledTimes(2);
            expect(call(1).url).toContain("/api/v2/platform/connectors?pageNumber=2&pageSize=100");
        });

        test("getAllConnectorsV2 sorts by name like v1", async () => {
            respond(200, page([connectorV2({ id: "z", name: "Zeta" }), connectorV2({ id: "a", name: "Alpha" })]));
            expect((await getAllConnectorsV2()).map(c => c.name)).toEqual(["Alpha", "Zeta"]);
        });

        test("getConnectorsV2 sends the page and the trimmed name filter and keeps the page meta", async () => {
            respond(200, page([connectorV2()]));

            const resp = await getConnectorsV2({ pageNumber: 3, pageSize: 10, name: " ord " });

            expect(call(0).url).toBe("/api/v2/platform/connectors?pageNumber=3&pageSize=10&name=ord");
            expect(resp.meta.pagination.nextPage).toBeNull();
            expect(resp.data[0].id).toBe("a");
        });

        test("getConnectorsV2 defaults to page 1 of 100", async () => {
            respond(200, page([]));
            await getConnectorsV2();
            expect(call(0).url).toBe("/api/v2/platform/connectors?pageNumber=1&pageSize=100");
        });

        test("getConnectorV2 maps the v2 shape back onto the v1 UI model", async () => {
            respond(200, { data: connectorV2({ connectorType: "api", connectorSubtype: "openapi", agentCount: 2 }) });

            const got = await getConnectorV2("a");

            expect(call(0).url).toBe("/api/v2/platform/connectors/a");
            expect(got).toMatchObject({ id: "a", type: "api", subtype: "openapi", name: "Orders API", agentCount: 2, createdBy: "u1", createdAt: "2026-10-19T12:00:00.000Z" });
        });

        const withSpec = connectorV2({ values: { specification_url: "/api/v2/platform/connectors/a/resource" } });
        test.each([
            ["getConnectorV2", () => respond(200, { data: withSpec }), async () => getConnectorV2("a")],
            ["getConnectorsV2", () => respond(200, page([withSpec])), async () => (await getConnectorsV2()).data[0]],
            ["getAllConnectorsV2", () => respond(200, page([withSpec])), async () => (await getAllConnectorsV2())[0]],
        ])("%s absolutizes a relative specification_url like v1", async (_name, reply, read) => {
            reply();
            const got = await read();
            expect(got.values.specification_url).toBe(`${globalThis.location.origin}/api/v2/platform/connectors/a/resource`);
        });

        test("a redacted value reads through untouched", async () => {
            respond(200, { data: connectorV2({ values: { password: "<REDACTED>" } }) });
            expect((await getConnectorV2("a")).values.password).toBe("<REDACTED>");
        });
    });

    describe("toConnectorUIModel", () => {
        test("maps a null description to an empty string, as v1 always sent a string", () => {
            expect(toConnectorUIModel(connectorV2({ description: null }) as never).description).toBe("");
        });

        test("drops null members and the members the v1 model does not carry", () => {
            const model = toConnectorUIModel(connectorV2() as never);

            expect(model).not.toHaveProperty("updatedBy");
            expect(model).not.toHaveProperty("connectorType");
            expect(model).not.toHaveProperty("contentHash");
            expect(model).not.toHaveProperty("warnings");
            expect(model.updatedAt).toBe("");
        });
    });

    describe("writes", () => {
        test("createConnectorV2 sends the renamed kind fields and nothing else", async () => {
            respond(201, { data: connectorV2({ id: "new" }) });

            const created = await createConnectorV2({ name: "Orders API", description: "The orders service", type: "api", subtype: "openapi", values: { base_url: "x" }, extra: "ignored" });

            expect(call(0)).toMatchObject({ url: "/api/v2/platform/connectors", method: "POST" });
            expect(jsonBody(0)).toEqual({ name: "Orders API", description: "The orders service", connectorType: "api", connectorSubtype: "openapi", values: { base_url: "x" } });
            expect(created?.id).toBe("new");
        });

        test("createConnectorFromFormV2 posts the form's connector and returns it as a v1 Connector", async () => {
            respond(201, { data: connectorV2({ id: "new" }) });
            const form = { id: "", name: "Orders API", description: "The orders service", type: "api", subtype: "openapi", values: { base_url: "x" }, agentCount: 4 } as Connector;

            const created = await createConnectorFromFormV2(form);

            expect(call(0)).toMatchObject({ url: "/api/v2/platform/connectors", method: "POST" });
            expect(jsonBody(0)).toEqual({ name: "Orders API", description: "The orders service", connectorType: "api", connectorSubtype: "openapi", values: { base_url: "x" } });
            expect(created).toMatchObject({ id: "new", type: "api", subtype: "openapi" });
        });

        test("updateConnectorV2 PATCHes name, description and values", async () => {
            respond(200, { data: connectorV2() });

            const updated = await updateConnectorV2("a", { name: "New name", description: "A new description", values: { base_url: "y" } });

            expect(call(0)).toMatchObject({ url: "/api/v2/platform/connectors/a", method: "PATCH" });
            expect(jsonBody(0)).toEqual({ name: "New name", description: "A new description", values: { base_url: "y" } });
            expect(updated.type).toBe("api");
        });

        test("updateConnectorV2 leaves out an empty name or description, which v1 read as unchanged and v2 rejects", async () => {
            respond(200, { data: connectorV2() });

            await updateConnectorV2("a", { name: "", description: "" });

            expect(jsonBody(0)).toEqual({});
        });

        test("deleteConnectorV2 is a DELETE", async () => {
            respond(204);
            await deleteConnectorV2("a");
            expect(call(0)).toMatchObject({ url: "/api/v2/platform/connectors/a", method: "DELETE" });
        });

        test("uploadConnectorResourceV2 PUTs the multipart form and returns the connector", async () => {
            respond(200, { data: connectorV2({ hasResource: true }) });
            const form = new FormData();
            form.append("file", new File(["{}"], "spec.json"));

            const updated = await uploadConnectorResourceV2("a", form);

            expect(call(0)).toMatchObject({ url: "/api/v2/platform/connectors/a/resource", method: "PUT" });
            expect(call(0).body).toBe(form);
            expect(updated.subtype).toBe("openapi");
        });

        test("a write keeps the platform's warnings for the edit form to show", async () => {
            const warning = 'approval gate on "delete_pet" was removed: the connector no longer provides that tool';
            respond(200, { data: connectorV2({ warnings: [warning] }) });

            const updated = await updateConnectorV2("a", { name: "New name", description: "" });

            expect(updated.warnings).toEqual([warning]);
        });

        test("createConnectorFromFormV2 keeps the platform's warnings, so a gate it could not check is shown", async () => {
            const warning = 'approval gates on "delete_pet" could not be checked: the connector has no uploaded specification';
            respond(201, { data: connectorV2({ id: "new", warnings: [warning] }) });
            const form = { id: "", name: "Orders API", description: "The orders service", type: "api", subtype: "openapi", values: { base_url: "x" } } as Connector;

            const created = await createConnectorFromFormV2(form);

            expect(created.warnings).toEqual([warning]);
        });

        test("createConnectorFromFormV2 carries no warnings member when the platform sent none", async () => {
            respond(201, { data: connectorV2({ id: "new", warnings: [] }) });
            const form = { id: "", name: "Orders API", description: "The orders service", type: "api", subtype: "openapi", values: { base_url: "x" } } as Connector;

            expect(await createConnectorFromFormV2(form)).not.toHaveProperty("warnings");
        });

        test("uploadConnectorResourceV2 keeps the warnings of a spec upload, such as a removed approval gate", async () => {
            const warning = 'approval gate on "delete_pet" was removed: the connector no longer provides that tool';
            respond(200, { data: connectorV2({ hasResource: true, warnings: [warning] }) });
            const form = new FormData();
            form.append("file", new File(["{}"], "spec.json"));

            const uploaded = await uploadConnectorResourceV2("a", form);

            expect(uploaded.warnings).toEqual([warning]);
        });

        test("a v2 error rejects with the message the screens show", async () => {
            respond(409, { message: "A connector with this name already exists" });
            await expect(createConnectorV2({ name: "n", description: "d", type: "api" })).rejects.toThrow("A connector with this name already exists");
        });
    });

    describe("probes", () => {
        test("diffConnectorSecretsV2 PUTs the values and returns the per-secret result", async () => {
            respond(200, { data: { secrets: { password: { matches: true } } } });

            const diff = await diffConnectorSecretsV2("a", { password: "<REDACTED>" });

            expect(call(0)).toMatchObject({ url: "/api/v2/platform/connectors/a/secretsDiff", method: "PUT" });
            expect(jsonBody(0)).toEqual({ values: { password: "<REDACTED>" } });
            expect(diff.secrets.password?.matches).toBe(true);
        });

        test("testConnectorConnectionV2 turns a null message into an absent one", async () => {
            respond(200, { data: { success: true, message: null, errorClass: null, stage: null } });

            const result = await testConnectorConnectionV2({ connectorId: "a" });

            expect(call(0)).toMatchObject({ url: "/api/v2/platform/connectors/testConnection", method: "PUT" });
            expect(result).toEqual({ success: true });
        });

        test("testConnectorConnectionV2 keeps a failure's message", async () => {
            respond(200, { data: { success: false, message: "Could not connect", errorClass: "network", stage: "dial" } });
            expect(await testConnectorConnectionV2({ values: { broker_url: "x" } })).toEqual({ success: false, message: "Could not connect" });
        });

        test("detectConnectorAuthV2 sends an uploaded file as specificationContent", async () => {
            respond(200, { data: { type: "detectAuthResult", schemes: [], suggestedAuth: null } });
            const form = new FormData();
            form.append("file", new File(['{"openapi":"3.0.0"}'], "spec.json"));

            const result = await detectConnectorAuthV2(form);

            expect(call(0)).toMatchObject({ url: "/api/v2/platform/connectors/openapi/detectAuth", method: "PUT" });
            expect(jsonBody(0)).toEqual({ specificationContent: '{"openapi":"3.0.0"}' });
            expect(result).toEqual({ schemes: [], suggestedAuth: null });
        });

        test("detectConnectorAuthV2 sends a URL as specificationUrl", async () => {
            respond(200, { data: { type: "detectAuthResult", schemes: [], suggestedAuth: null } });
            const form = new FormData();
            form.append("specificationUrl", "https://api.example.com/openapi.json");

            await detectConnectorAuthV2(form);

            expect(jsonBody(0)).toEqual({ specificationUrl: "https://api.example.com/openapi.json" });
        });

        test("detectConnectorAuthV2 restores the auth type spellings the form maps on", async () => {
            const details = { scheme: null, bearerFormat: null, in: null, name: null, authorizationUrl: null, tokenUrl: null, refreshUrl: null, openIdConnectUrl: null };
            respond(200, {
                data: {
                    type: "detectAuthResult",
                    schemes: [{ schemeName: "tls", authType: "mutual_tls", details }],
                    suggestedAuth: { authType: "mutual_tls", config: {} },
                },
            });
            const form = new FormData();
            form.append("specificationUrl", "https://x");

            const result = await detectConnectorAuthV2(form);

            expect(result.schemes[0].authType).toBe("mutualTLS");
            expect(result.suggestedAuth?.authType).toBe("mutualTLS");
            expect(result.schemes[0].details).toEqual({});
        });
    });

    describe("schemas", () => {
        const subtype = (overrides: Record<string, unknown> = {}) => ({
            subtype: "openapi",
            name: "OpenAPI",
            description: "REST",
            usageGuidance: null,
            connectionTemplate: null,
            warning: null,
            experimental: false,
            fieldSchema: {
                base_url: {
                    label: "Base URL",
                    type: "string",
                    required: true,
                    secret: false,
                    hidden: false,
                    order: 0,
                    default: null,
                    placeholder: "https://api.example.com",
                    hint: null,
                    options: [],
                    validation: { minLength: 1, maxLength: null, min: null, max: null, pattern: null, errorMsg: null },
                    conditionalDisplay: null,
                },
            },
            ...overrides,
        });

        test("reads every page and maps id back to type", async () => {
            respond(200, page([{ id: "api", type: "connectorSchema", subtypes: [subtype()] }], 2));
            respond(200, page([{ id: "sql", type: "connectorSchema", subtypes: [] }]));

            const { data } = await getConnectorSchemasV2();

            expect(data.map(d => d.type)).toEqual(["api", "sql"]);
            expect(call(1).url).toContain("/api/v2/platform/connectorSchemas?pageNumber=2");
        });

        test("drops nulls and empty options so the field reads as it did on v1", async () => {
            respond(200, page([{ id: "api", type: "connectorSchema", subtypes: [subtype()] }]));

            const { data } = await getConnectorSchemasV2();

            const sub = data[0].subtypes[0];
            expect(sub).not.toHaveProperty("usageGuidance");
            expect(sub.fieldSchema.base_url).toEqual({ label: "Base URL", type: "string", required: true, secret: false, hidden: false, order: 0, placeholder: "https://api.example.com", validation: { minLength: 1 } });
        });
    });

    test("getConnectorSpecificationV2 unwraps the document into the shape v1 returns", async () => {
        respond(200, { data: { content: '{"openapi":"3.0.0"}', format: "json" } });

        const spec = await getConnectorSpecificationV2("a");

        expect(call(0).url).toBe("/api/v2/platform/connectors/a/specification");
        expect(spec).toEqual({ content: '{"openapi":"3.0.0"}', format: "json" });
    });

    test("getConnectorSpecificationV2 escapes the id so it cannot change the route", async () => {
        respond(200, { data: { content: "", format: "json" } });

        await getConnectorSpecificationV2("a/../b");

        expect(call(0).url).toBe("/api/v2/platform/connectors/a%2F..%2Fb/specification");
    });

    test("connectorResourcePathV2 points at the v2 download", () => {
        expect(connectorResourcePathV2("a")).toBe("/api/v2/platform/connectors/a/resource");
    });
});
