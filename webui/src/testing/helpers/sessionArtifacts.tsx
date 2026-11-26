// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQueryClient } from "@tanstack/react-query";

import { artifactKeys } from "@/api/artifacts";

import type { ArtifactInfo } from "@/types";

export function SeedSessionArtifacts({ sessionId, artifacts }: { sessionId: string; artifacts: ArtifactInfo[] }) {
    const queryClient = useQueryClient();
    queryClient.setQueryData(artifactKeys.session(sessionId), artifacts);
    return null;
}
