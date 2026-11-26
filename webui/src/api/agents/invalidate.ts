// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { QueryClient } from "@tanstack/react-query";

import { connectorKeys } from "@/api/connectors/keys";
import { skillKeys } from "@/api/skills/keys";
import { toolsetKeys } from "@/api/toolsets/keys";

import { agentKeys } from "./keys";

export function invalidateAgentRelations(queryClient: QueryClient): void {
    queryClient.invalidateQueries({ queryKey: agentKeys.lists() });
    queryClient.invalidateQueries({ queryKey: toolsetKeys.lists() });
    queryClient.invalidateQueries({ queryKey: skillKeys.lists() });
    queryClient.invalidateQueries({ queryKey: connectorKeys.lists() });
}
