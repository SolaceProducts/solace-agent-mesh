// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "../client";

import type { ConsentResponse } from "./types";

export const getConsent = async (): Promise<ConsentResponse> => {
    return api.webui.get<ConsentResponse>("/api/v1/productAnalytics/consent");
};

export const updateConsent = async (granted: boolean): Promise<ConsentResponse> => {
    return api.webui.put<ConsentResponse>("/api/v1/productAnalytics/consent", { granted });
};
