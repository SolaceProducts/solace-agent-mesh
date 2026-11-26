// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";

import type { Connector, ConnectorSubtypeSchema, ConnectorType } from "@/types";

import type { PaginatedResponse } from "../common";

export type ConnectorsListResponse = PaginatedResponse<Connector>;

export interface GetConnectorsParams {
    pageNumber?: number;
    pageSize?: number;
    name?: string;
}

export function absolutizeSpecURL(connector: Connector): Connector {
    const specURL = connector.values?.specification_url;
    if (typeof specURL !== "string" || !specURL.startsWith("/")) {
        return connector;
    }
    return { ...connector, values: { ...connector.values, specification_url: api.platformOrigin() + specURL } };
}

export interface CreateConnectorRequest {
    name: string;
    description: string;
    type: string;
    subtype?: string;
    values?: Record<string, unknown>;
    [key: string]: unknown;
}

export interface ConnectorSpecification {
    content: string;
    format: "json" | "yaml";
}

export interface ConnectorSecretsDiffResponse {
    secrets: Partial<Record<string, { matches: boolean }>>;
}

export interface UpdateConnectorRequest {
    name: string;
    description: string;
    values?: Record<string, unknown>;
}

export interface ConnectorTypeSchema {
    type: ConnectorType;
    subtypes: ConnectorSubtypeSchema[];
}

export interface SecuritySchemeDetails {
    schemeName: string;
    authType: string;
    details: Record<string, any>;
}

export interface DetectedAuthConfig {
    authType: string;
    config: Record<string, any>;
}

export interface DetectAuthResponse {
    schemes: SecuritySchemeDetails[];
    suggestedAuth: DetectedAuthConfig | null;
}
