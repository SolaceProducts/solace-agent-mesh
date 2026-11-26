// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { CreateGatewayRequest } from "@/types";

export type { CreateGatewayRequest };

export type GatewayDeploymentAction = "deploy" | "undeploy" | "update";

export interface GatewayDeploymentRequest {
    gatewayId: string;
    action: GatewayDeploymentAction;
}
