// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface OpenAPISpecFile extends Record<string, any> {
    openapi: string;
    info: {
        title?: string;
        description?: string;
        version?: string;
        [key: string]: any;
    };
    servers?: Array<{
        url: string;
        description?: string;
        [key: string]: any;
    }>;
    security?: Array<Record<string, any>>;
    paths: Record<string, Record<string, Path>>;
    components?: {
        schemas?: {
            [key: string]: any;
        };
        securitySchemes?: {
            [key: string]: any;
        };
        [key: string]: any;
    };
    [key: string]: any;
}

export interface Path {
    operationId?: string;
    summary?: string;
    description?: string;
    parameters?: Array<Parameter>;
    requestBody?: RequestBody;
    responses?: Record<string, any>;
    [key: string]: any;
}

export interface Parameter {
    name: string;
    in: "query" | "header" | "path" | "cookie";
    required: boolean;
    schema?: Record<string, any>;
    description?: string;
    [key: string]: any;
}

export interface RequestBody {
    description?: string;
    content: Record<string, Record<string, any>>;
    required?: boolean;
    $ref?: string;
}
