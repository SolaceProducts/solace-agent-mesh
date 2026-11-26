// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Parameter, RequestBody } from "./openapi";

export interface Tool {
    operationId: string;
    name: string;
    description: string;
    method: string;
    endpoint: string;
    deprecated?: boolean;
    inputSchema?: Record<string, unknown>;
    parameters?: Array<Parameter>;
    requestBody?: RequestBody | null;
}
