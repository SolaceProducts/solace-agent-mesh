// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type ConsentState = "unset" | "granted" | "denied";

export interface ConsentResponse {
    state: ConsentState;
}

export function isCollecting(consent: ConsentResponse | undefined): boolean {
    return consent?.state === "unset" || consent?.state === "granted";
}

export function isKnownConsentState(consent: ConsentResponse | undefined): boolean {
    return isCollecting(consent) || consent?.state === "denied";
}
