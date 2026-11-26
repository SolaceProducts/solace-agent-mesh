// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import type { AuthConfig } from "@/api/remote-agents";

export interface AuthStoredSecrets {
    agentCardAuth: Set<string>;
    taskAuth: Set<string>;
}

export const useTrackAuthStoredSecrets = (agentCardAuthConfig?: AuthConfig, agentAuthConfig?: AuthConfig): AuthStoredSecrets => {
    return useMemo(() => {
        const result: AuthStoredSecrets = {
            agentCardAuth: new Set(),
            taskAuth: new Set(),
        };

        if (agentCardAuthConfig?.type) {
            switch (agentCardAuthConfig.type) {
                case "bearer":
                    if (agentCardAuthConfig.token === "<REDACTED>") {
                        result.agentCardAuth.add("token");
                    }
                    break;
                case "apikey":
                    if (agentCardAuthConfig.apiKey === "<REDACTED>") {
                        result.agentCardAuth.add("apiKey");
                    }
                    break;
                case "oauth2":
                    if (agentCardAuthConfig.clientSecret === "<REDACTED>") {
                        result.agentCardAuth.add("clientSecret");
                    }
                    break;
                case "basic":
                    if (agentCardAuthConfig.password === "<REDACTED>") {
                        result.agentCardAuth.add("password");
                    }
                    break;
                default:
                    break;
            }
        }

        if (agentAuthConfig?.type) {
            switch (agentAuthConfig.type) {
                case "bearer":
                    if (agentAuthConfig.token === "<REDACTED>") {
                        result.taskAuth.add("token");
                    }
                    break;
                case "apikey":
                    if (agentAuthConfig.apiKey === "<REDACTED>") {
                        result.taskAuth.add("apiKey");
                    }
                    break;
                case "oauth2":
                    if (agentAuthConfig.clientSecret === "<REDACTED>") {
                        result.taskAuth.add("clientSecret");
                    }
                    break;
                case "basic":
                    if (agentAuthConfig.password === "<REDACTED>") {
                        result.taskAuth.add("password");
                    }
                    break;
                default:
                    break;
            }
        }

        return result;
    }, [agentCardAuthConfig, agentAuthConfig]);
};

export const hasStoredAuthSecret = (storedSecrets: AuthStoredSecrets, authScope: "agentCardAuth" | "taskAuth", fieldName: string): boolean => {
    return storedSecrets[authScope].has(fieldName);
};
