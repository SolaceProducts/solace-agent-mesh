// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ApiError } from "@/api/client";

export const AGREEMENT_LIST_PLACEHOLDER = "{#agreement_list#}";

export const TERMS_UNREACHABLE = "Couldn't reach Solace to load the license agreement. Check your network connection and try again.";

export function splitCheckboxText(checkboxText: string): [before: string, after: string] | null {
    const at = checkboxText.indexOf(AGREEMENT_LIST_PLACEHOLDER);
    if (at < 0) return null;
    return [checkboxText.slice(0, at), checkboxText.slice(at + AGREEMENT_LIST_PLACEHOLDER.length)];
}

export function listSeparator(index: number, count: number): string {
    if (index === 0) return "";
    return index === count - 1 ? " and " : ", ";
}

const NO_AGREEMENT_LINKED_STATUS = 503;

export function termsLoadRetryable(error: unknown): boolean {
    return (error as ApiError | null)?.status !== NO_AGREEMENT_LINKED_STATUS;
}

export interface AgreementFallback {
    before: string;
    linkText: string;
    after: string;
}

export const EULA_FALLBACK: AgreementFallback = { before: "I agree to the ", linkText: "Solace Agent Mesh End User License Agreement", after: "" };
export const FREE_MODEL_TERMS_FALLBACK: AgreementFallback = { before: "I agree to Solace's ", linkText: "terms and conditions", after: " for free model access." };
