// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";
import { z } from "zod";

import type { ConnectorSubtypeSchema, GatewaySchema } from "@/types/models";

import { agentConnectorsSchema, agentInstructionsSchema, agentModelSchema, agentToolsetsSchema } from "@/features/agents/agentEditSchemas";
import { agentInfoSchema } from "@/features/agents/common";
import { createEventMeshConnectorSchema } from "@/features/connectors/eventMeshUtils";
import { baseConnectorSchema, createFullConnectorSchemaValidator } from "@/features/connectors/utils/schemaGenerator";
import { createFullGatewaySchemaValidator } from "@/features/entrypoints/hooks/useGatewaySchemas";
import { MODEL_FORM_FIELD_NAMES } from "@/features/models/modelProviderUtils";

import { PRODUCT_ANALYTICS_FORM_FIELDS } from "./formFields";
import typesSource from "./types.ts?raw";

function unionMembers(typeName: string): string[] {
    const marker = `export type ${typeName} =`;
    const start = typesSource.indexOf(marker);
    expect(start, `${typeName} not found in types.ts`).toBeGreaterThanOrEqual(0);
    const block = typesSource.slice(start + marker.length).split(";")[0];
    const members = [...block.matchAll(/"([^"]+)"/g)].map(match => match[1]);
    expect(members.length, `no members parsed from ${typeName}`).toBeGreaterThan(0);
    return members.sort();
}

function gatewaySchemaOfType(type: string): GatewaySchema {
    return {
        type,
        name: type,
        description: "",
        schema: {},
        yamlTemplatePath: "",
        requiredEnvVars: [],
    };
}

const connectorSubtypeSchema: ConnectorSubtypeSchema = {
    subtype: "openapi" as ConnectorSubtypeSchema["subtype"],
    name: "",
    description: "",
    fieldSchema: {},
};

const formSchemas = [
    agentInfoSchema,
    agentInstructionsSchema,
    agentToolsetsSchema,
    agentModelSchema,
    agentConnectorsSchema,
    baseConnectorSchema,
    createFullConnectorSchemaValidator(connectorSubtypeSchema),
    createEventMeshConnectorSchema(false),
    createFullGatewaySchemaValidator(gatewaySchemaOfType("slack")),
    createFullGatewaySchemaValidator(gatewaySchemaOfType("mcp")),
];

const REACT_HOOK_FORM_RULE_TYPES = ["required", "min", "max", "validate"];

describe("form analytics vocabularies", () => {
    it("declares every top-level key the instrumented form schemas can raise an error on", () => {
        const derived = new Set<string>();
        for (const schema of formSchemas) {
            for (const key of Object.keys(schema.shape)) {
                derived.add(key);
            }
        }
        for (const name of MODEL_FORM_FIELD_NAMES) {
            derived.add(name);
        }
        const topLevel = PRODUCT_ANALYTICS_FORM_FIELDS.filter(member => !member.includes("."))
            .slice()
            .sort();
        expect([...derived].sort()).toEqual(topLevel);
    });

    it("declares every zod issue code the resolver can copy onto an error, the rule names, and the fallback", () => {
        const derived = [...Object.keys(z.ZodIssueCode), "unknown", ...REACT_HOOK_FORM_RULE_TYPES].sort();
        expect(derived).toEqual(unionMembers("ProductAnalyticsFormErrorType"));
    });
});
