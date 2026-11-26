// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface ActivationAccount {
    subject: string;
    email: string;
    name: string;
}

export interface ActivationAgreements {
    eulaClickwrapId: string;
}

export interface ActivationSignInFailure {
    code: string;
    message: string;
}

export interface Activation {
    state: string;
    signingIn: boolean;
    signInFailure?: ActivationSignInFailure;
    account?: ActivationAccount;
    agreements: ActivationAgreements;
    freeModelAvailable: boolean;
    freeModel?: ActivationFreeModel;
    freeModelChosen: boolean;
}

export interface ActivationFreeModel {
    grantId: string;
    provider: string;
    apiBase: string;
    generalModelName: string;
    planningModelName: string;
}

export interface AcceptActivationRequest {
    freeModel: boolean;
    openedDocumentIds?: string[];
}

export interface TermsDocument {
    id: string;
    name: string;
    version: string;
    url: string;
}

export interface ClickwrapTerms {
    agreed: boolean;
    checkboxText: string;
    documents: TermsDocument[];
}

export interface FreeModelStatus {
    status: string;
    remainingPercent: number | null;
    models: FreeModelDisplay[];
}

export interface FreeModelDisplay {
    modelName: string;
    displayName: string;
}
