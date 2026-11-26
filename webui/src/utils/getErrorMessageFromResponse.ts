// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const getErrorMessageFromResponse = async (response: Response): Promise<string> => {
    try {
        const errorObj = await response.json();
        const message = errorObj.message ?? errorObj.error?.message ?? "An error occurred.";

        if (errorObj.validationDetails && typeof errorObj.validationDetails === "object") {
            const validationErrors: string[] = [];
            for (const key in errorObj.validationDetails) {
                const errors = errorObj.validationDetails[key];
                if (Array.isArray(errors)) {
                    validationErrors.push(...errors);
                }
            }
            if (validationErrors.length > 0) {
                return `${message}: ${validationErrors.join(" ")}`;
            }
        }

        return message;
    } catch {
        return response.statusText ?? "An error occurred.";
    }
};
