// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { MCPOAuthInitiateRequest } from "@/types";

export const buildAuthConfigFromValues = (values: Record<string, any>): any => {
    const authType = values.auth_type;

    if (!authType || authType === "none") {
        return undefined;
    }

    const authConfig: any = { type: authType };

    if (authType === "http") {
        const scheme = values.auth_http_scheme;
        authConfig.http = { scheme };

        if (scheme === "bearer") {
            authConfig.http.token = values.auth_http_bearer_token;
        } else if (scheme === "basic") {
            authConfig.http.username = values.auth_http_basic_username;
            authConfig.http.password = values.auth_http_basic_password;
        }
    } else if (authType === "apikey") {
        authConfig.apikey = {
            in: values.auth_apikey_location,
            name: values.auth_apikey_name,
            value: values.auth_apikey_value,
        };
    }

    return authConfig;
};

export function parseKeyValuePairs(raw: unknown): Record<string, string> | null {
    if (!raw || !Array.isArray(raw)) return null;

    const dict: Record<string, string> = {};
    for (const pair of raw as Array<{ key: string; value: string }>) {
        const key = pair.key?.trim();
        const value = pair.value?.trim();
        if (key && value) dict[key] = value;
    }
    return Object.keys(dict).length > 0 ? dict : null;
}

export const buildOAuthInitiateRequest = (formValues: Record<string, any>): MCPOAuthInitiateRequest => {
    const prefix = "auth_oauth";

    return {
        serverUrl: formValues.server_url,
        connectionType: formValues.connection_type,
        mode: formValues[`${prefix}_mode`] || "discovery",
        authorizationUrl: formValues[`${prefix}_authorization_url`],
        tokenUrl: formValues[`${prefix}_token_url`],
        clientId: formValues[`${prefix}_client_id`],
        clientSecret: formValues[`${prefix}_client_secret`],
        scopes: formValues[`${prefix}_scopes`],
        tokenEndpointAuthMethod: formValues[`${prefix}_token_endpoint_auth_method`],
    };
};
