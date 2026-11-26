// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, type ReactNode, useContext } from "react";

const ArtifactSessionContext = createContext<string | undefined>(undefined);

export const ArtifactSessionProvider = ({ sessionId, children }: { sessionId?: string; children: ReactNode }) => {
    return <ArtifactSessionContext.Provider value={sessionId}>{children}</ArtifactSessionContext.Provider>;
};

export const useArtifactSession = (): string | undefined => useContext(ArtifactSessionContext);
