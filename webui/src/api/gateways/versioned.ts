// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { CreateGatewayRequest, Gateway, GatewayConnectionTestRequest, GatewayConnectionTestResponse, GatewayDeployment, GatewayNetworkingDetails, GatewaySchema, WhatsAppMessageTemplatesRequest, WhatsAppMessageTemplatesResponse } from "@/types";

import type { GatewayDeploymentRequest } from "./service";
import {
    createGatewayV2,
    deleteGatewayV2,
    deployGatewayV2,
    getAllGatewaysV2,
    getGatewayDeploymentsV2,
    getGatewayNetworkingDetailsV2,
    getGatewaySchemasV2,
    getGatewayV2,
    getWhatsAppMessageTemplatesV2,
    saveGatewayV2,
    testGatewayConnectionV2,
} from "./v2";

export interface GatewaysApi {
    getAll: () => Promise<Gateway[]>;
    get: (id: string) => Promise<Gateway>;
    create: (request: CreateGatewayRequest) => Promise<{ id?: string } | null>;
    save: (gatewayId: string | undefined, fields: CreateGatewayRequest) => Promise<{ deploymentStatus?: string; deployError?: string }>;
    delete: (id: string) => Promise<void>;
    deploy: (request: GatewayDeploymentRequest) => Promise<GatewayDeployment | null>;
    getDeployments: (gatewayId: string) => Promise<GatewayDeployment[]>;
    getNetworkingDetails: (gatewayId: string) => Promise<GatewayNetworkingDetails | null>;
    getSchemas: () => Promise<GatewaySchema[]>;
    testConnection: (request: GatewayConnectionTestRequest) => Promise<GatewayConnectionTestResponse>;
    getWhatsAppMessageTemplates: (request: WhatsAppMessageTemplatesRequest) => Promise<WhatsAppMessageTemplatesResponse>;
}

export const gatewaysApi: GatewaysApi = {
    getAll: getAllGatewaysV2,
    get: getGatewayV2,
    create: createGatewayV2,
    save: saveGatewayV2,
    delete: deleteGatewayV2,
    deploy: deployGatewayV2,
    getDeployments: getGatewayDeploymentsV2,
    getNetworkingDetails: getGatewayNetworkingDetailsV2,
    getSchemas: getGatewaySchemasV2,
    testConnection: testGatewayConnectionV2,
    getWhatsAppMessageTemplates: getWhatsAppMessageTemplatesV2,
};
