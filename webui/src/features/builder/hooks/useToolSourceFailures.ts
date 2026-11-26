// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useRef } from "react";

import { useAgents } from "@/api/agents";
import { useConnectors } from "@/api/connectors";

import { awaitingToolRegistrations, hasSessionAgent, type SessionComponent, type ToolSourceFailure, toolSourceFailures } from "../toolSourceFailures";

const REGISTRATION_POLL_MS = 3000;

export const REGISTRATION_POLL_LIMIT_MS = 5 * 60 * 1000;

export function useToolSourceFailures(components: readonly SessionComponent[] | undefined): ToolSourceFailure[] {
    const pollingSince = useRef<number | null>(null);
    const { data: connectors } = useConnectors();
    const { data: agents } = useAgents({
        enabled: hasSessionAgent(components),
        refetchOnMount: "always",
        refetchInterval: current => {
            if (!awaitingToolRegistrations(components, current)) {
                pollingSince.current = null;
                return false;
            }
            pollingSince.current ??= Date.now();
            return Date.now() - pollingSince.current < REGISTRATION_POLL_LIMIT_MS ? REGISTRATION_POLL_MS : false;
        },
    });
    return useMemo(() => toolSourceFailures(components, agents, connectors), [components, agents, connectors]);
}
