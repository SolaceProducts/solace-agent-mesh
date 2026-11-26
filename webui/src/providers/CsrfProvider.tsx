// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useCallback, useState } from "react";

import { api } from "@/api/client";

import { CsrfContext, type CsrfContextValue } from "@/providers/contexts/CsrfContext";

function getCookie(name: string): string | null {
    if (typeof document === "undefined") return null;
    const value = `; ${document.cookie}`;
    const parts = value.split(`; ${name}=`);
    if (parts.length === 2) return parts.pop()?.split(";").shift() || null;
    return null;
}

const delay = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

const getCsrfToken = async (retries = 5, delayMs = 50): Promise<string | null> => {
    try {
        const response = await api.webui.get("/api/v1/csrf-token", {
            credentials: "include",
            fullResponse: true,
        });

        if (!response.ok) {
            throw new Error(`CSRF endpoint returned status ${response.status}`);
        }

        const responseData = await response.json();
        if (responseData.csrfToken) {
            console.log("CSRF token found in response body:", responseData.csrfToken);
            return responseData.csrfToken;
        }

        for (let i = 0; i < retries; i++) {
            const token = getCookie("csrf_token");
            if (token) {
                console.log(`CSRF token found in cookie after ${i} retries:`, token);
                return token;
            }
            console.log(`CSRF token not found in cookie, attempt ${i + 1}/${retries}. Waiting ${delayMs}ms...`);
            await delay(delayMs);
        }

        throw new Error("CSRF token not available in response or cookie after retries");
    } catch (error) {
        console.error("Error fetching/reading CSRF token:", error);
        return null;
    }
};

interface CsrfProviderProps {
    children: ReactNode;
}

export function CsrfProvider({ children }: Readonly<CsrfProviderProps>) {
    const [csrfToken, setCsrfToken] = useState<string | null>(null);

    const fetchCsrfToken = useCallback(async (): Promise<string | null> => {
        if (csrfToken) return csrfToken;

        const token = await getCsrfToken();
        if (token) {
            setCsrfToken(token);
        } else {
            throw new Error("Failed to obtain CSRF token after config fetch failed.");
        }
        return token;
    }, [csrfToken]);

    const clearCsrfToken = useCallback(() => {
        setCsrfToken(null);
    }, []);

    const contextValue: CsrfContextValue = {
        fetchCsrfToken,
        clearCsrfToken,
    };

    return <CsrfContext.Provider value={contextValue}>{children}</CsrfContext.Provider>;
}
