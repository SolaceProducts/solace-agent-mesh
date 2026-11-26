// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const getAccessToken = () => localStorage.getItem("access_token");

export const getSamAccessToken = () => localStorage.getItem("sam_access_token");

export const getApiBearerToken = (): string | null => {
    const samToken = getSamAccessToken();

    if (samToken) {
        return samToken;
    }
    return getAccessToken();
};

export const getErrorMessage = (error: unknown, fallbackMessage: string = "An unknown error occurred"): string => {
    if (error instanceof Error) {
        return error.message ?? fallbackMessage;
    }
    return fallbackMessage;
};
