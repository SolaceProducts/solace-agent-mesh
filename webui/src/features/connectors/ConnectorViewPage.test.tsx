// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import { mockConnectorsSchemaMap, mockGetConnectorSchema, mockWebSearchConnector } from "@/testing/data";
import { connectorToV2 } from "@/testing/data/platformWireV2";
import { getPaginatedMeta } from "@/testing/helpers";
import { v2Page, v2ResourceOf } from "@/testing/helpers/platformResponses";

import meta from "./ConnectorViewPage.stories";

const mongoCapability = { key: "document_db_mongodb_connector", name: "MongoDB Connector", description: "", status: "beta", platform: "linux", hasEnvOverride: false };

const documentDbCapability = { key: "document_db_connector", name: "Document DB Connector", description: "", status: "experimental", platform: "linux", hasEnvOverride: false };

const connectorOf = (subtype: string) => ({
    id: `mock-${subtype}`,
    name: `Mock ${subtype}`,
    description: "",
    type: "document_db",
    subtype,
    values: {},
    agentCount: 0,
});

const server = setupServer(
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/config/capabilities", () => HttpResponse.json({ data: [mongoCapability], meta: getPaginatedMeta(1) })),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({})),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v2/platform/agents", () => HttpResponse.json(v2Page([]))),
    http.get("*/api/v2/platform/connectors/:id", ({ params }) => HttpResponse.json(v2ResourceOf("/api/v2/platform/connectors", connectorOf(String(params.id).replace("mock-", "")))))
);

const viewStory = (subtype: string, experimental: boolean) =>
    composeStory(
        {
            parameters: {
                chatContext: {
                    sessionId: "mock-session-id",
                    connectorSchemasMap: mockConnectorsSchemaMap,
                    getConnectorSchema: (type: string, schemaSubtype: string) => {
                        const base = mockGetConnectorSchema("sql", "postgres");
                        return base ? { ...base, type, subtype: schemaSubtype, experimental } : undefined;
                    },
                    useLoaderData: () => ({ connectorId: `mock-${subtype}` }),
                },
                configContext: { persistenceEnabled: true },
            },
        },
        meta
    );

describe("ConnectorViewPage lifecycle badge", () => {
    beforeAll(() => server.listen());
    afterEach(() => server.resetHandlers());
    afterAll(() => server.close());

    test("a MongoDB connector shows BETA from its subtype capability, ignoring the schema's experimental flag", async () => {
        const ViewMongo = viewStory("mongodb", false);
        render(<ViewMongo />);

        expect(await screen.findByText("BETA")).toBeInTheDocument();
        expect(screen.queryByText("EXPERIMENTAL")).not.toBeInTheDocument();
    });

    test("a DynamoDB connector with no subtype capability shows EXPERIMENTAL from the schema", async () => {
        const ViewDynamo = viewStory("dynamodb", true);
        render(<ViewDynamo />);

        expect(await screen.findByText("EXPERIMENTAL")).toBeInTheDocument();
        expect(screen.queryByText("BETA")).not.toBeInTheDocument();
    });

    test("a subtype capability wins over its type's capability", async () => {
        server.use(http.get("*/api/v1/config/capabilities", () => HttpResponse.json({ data: [documentDbCapability, mongoCapability], meta: getPaginatedMeta(2) })));
        const ViewMongo = viewStory("mongodb", false);
        render(<ViewMongo />);

        expect(await screen.findByText("BETA")).toBeInTheDocument();
        expect(screen.queryByText("EXPERIMENTAL")).not.toBeInTheDocument();
    });

    test("a web_search connector shows EARLY ACCESS from the web_search_connector capability, with its fields", async () => {
        const webSearchCapability = { key: "web_search_connector", name: "Web Search Connector", description: "", status: "early_access", platform: "linux", hasEnvOverride: false };
        server.use(
            http.get("*/api/v1/config/capabilities", () => HttpResponse.json({ data: [webSearchCapability], meta: getPaginatedMeta(1) })),
            http.get("*/api/v2/platform/connectors/:id", () => HttpResponse.json({ data: connectorToV2(mockWebSearchConnector) }))
        );
        const ViewWebSearch = composeStory(
            {
                parameters: {
                    chatContext: {
                        sessionId: "mock-session-id",
                        connectorSchemasMap: mockConnectorsSchemaMap,
                        getConnectorSchema: mockGetConnectorSchema,
                        useLoaderData: () => ({ connectorId: mockWebSearchConnector.id }),
                    },
                    configContext: { persistenceEnabled: true },
                },
            },
            meta
        );
        render(<ViewWebSearch />);

        expect(await screen.findByText("EARLY ACCESS")).toBeInTheDocument();
        expect(await screen.findByText("API Base URL")).toBeInTheDocument();
        expect(screen.getByText("Results per Search")).toBeInTheDocument();
        expect(screen.getByText("Safe Search")).toBeInTheDocument();
    });
});
