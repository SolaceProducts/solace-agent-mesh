// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

interface ModelSetupRequest {
    requested: boolean;
    shown: () => void;
    appShown: () => void;
    clear: () => void;
}

export const ModelSetupRequestContext = createContext<ModelSetupRequest>({ requested: false, shown: () => {}, appShown: () => {}, clear: () => {} });

export function useModelSetupRequest() {
    return useContext(ModelSetupRequestContext);
}
