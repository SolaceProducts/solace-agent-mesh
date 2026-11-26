// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { DetectAuthResponse, DetectedAuthConfig } from "@/api/connectors/service";
import { connectorsApi } from "@/api/connectors/versioned";

export type { DetectAuthResponse, DetectedAuthConfig, SecuritySchemeDetails } from "@/api/connectors/service";

export async function detectOpenAPIAuth(file: File | undefined, specificationUrl: string | undefined): Promise<DetectAuthResponse | null> {
    if (!file && !specificationUrl) {
        console.warn("detectOpenAPIAuth: Neither file nor specificationUrl provided");
        return null;
    }

    if (file && specificationUrl) {
        console.warn("detectOpenAPIAuth: Both file and specificationUrl provided, using file only");
    }

    try {
        const formData = new FormData();

        if (file) {
            formData.append("file", file);
        } else if (specificationUrl) {
            formData.append("specificationUrl", specificationUrl);
        }

        const response = await connectorsApi.detectAuth(formData);

        return response;
    } catch (error) {
        console.error("Failed to detect OpenAPI auth:", error);
        return null;
    }
}

export function mapDetectedAuthType(detectedAuthType: string): string {
    const authTypeMap: Record<string, string> = {
        apikey: "apikey",
        http: "http",
        oauth: "oauth",
        mutualTLS: "none",
    };

    return authTypeMap[detectedAuthType] || "none";
}

export function applyDetectedAuthConfig(suggestedAuth: DetectedAuthConfig | null, setValue: (name: string, value: any) => void, currentAuthType?: string, forceApply: boolean = false): void {
    if (!suggestedAuth) {
        return;
    }

    const { authType, config } = suggestedAuth;

    if (!forceApply && currentAuthType && currentAuthType !== "none") {
        return;
    }

    const connectorAuthType = mapDetectedAuthType(authType);
    setValue("values.auth_type", connectorAuthType);

    if (connectorAuthType === "oauth" && config.authorizationUrl) {
        setValue("values.auth_oauth_mode", "manual");
        setValue("values.auth_oauth_authorization_url", config.authorizationUrl);
        setValue("values.auth_oauth_token_url", config.tokenUrl || "");

        if (config.refreshUrl) {
            setValue("values.auth_oauth_refresh_url", config.refreshUrl);
        }

        if (config.scopes && Array.isArray(config.scopes)) {
            setValue("values.auth_oauth_scopes", config.scopes.join(" "));
        }
    } else if (connectorAuthType === "http") {
        const httpScheme = config.httpScheme || "bearer";
        setValue("values.auth_http_scheme", httpScheme);
    } else if (connectorAuthType === "apikey") {
        if (config.keyName) {
            setValue("values.auth_apikey_name", config.keyName);
        }

        if (config.keyLocation) {
            setValue("values.auth_apikey_in", config.keyLocation);
        }
    }
}
