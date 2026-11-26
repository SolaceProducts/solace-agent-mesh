// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Connector } from "@/types";

import type { ConnectorSecretsDiffResponse, ConnectorSpecification, ConnectorTypeSchema, CreateConnectorRequest, DetectAuthResponse, UpdateConnectorRequest } from "./service";
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
    updateConnectorV2,
    uploadConnectorResourceV2,
} from "./v2";

export interface ConnectorsApi {
    getPage: typeof getConnectorsV2;
    get: (id: string) => Promise<Connector>;
    getAll: () => Promise<Connector[]>;
    create: (payload: CreateConnectorRequest) => Promise<{ id?: string } | null>;
    createFromForm: (connector: Connector) => Promise<Connector>;
    update: (id: string, payload: UpdateConnectorRequest) => Promise<Connector>;
    delete: (id: string) => Promise<void>;
    uploadResource: (id: string, form: FormData) => Promise<Connector>;
    diffSecrets: (id: string, values: Record<string, unknown>) => Promise<ConnectorSecretsDiffResponse>;
    testConnection: (request: { connectorId?: string; values?: Record<string, unknown> }) => Promise<{ success: boolean; message?: string }>;
    getSchemas: () => Promise<{ data: ConnectorTypeSchema[] }>;
    detectAuth: (form: FormData) => Promise<DetectAuthResponse>;
    getSpecification: (id: string) => Promise<ConnectorSpecification>;
    resourcePath: (id: string) => string;
}

export const connectorsApi: ConnectorsApi = {
    getPage: getConnectorsV2,
    get: getConnectorV2,
    getAll: getAllConnectorsV2,
    create: createConnectorV2,
    createFromForm: createConnectorFromFormV2,
    update: updateConnectorV2,
    delete: deleteConnectorV2,
    uploadResource: uploadConnectorResourceV2,
    diffSecrets: diffConnectorSecretsV2,
    testConnection: testConnectorConnectionV2,
    getSchemas: getConnectorSchemasV2,
    detectAuth: detectConnectorAuthV2,
    getSpecification: getConnectorSpecificationV2,
    resourcePath: connectorResourcePathV2,
};
