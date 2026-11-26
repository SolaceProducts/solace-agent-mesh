// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useNavigate } from "react-router-dom";

import { useUserContext } from "@/providers/contexts";

import type { TaskSubject, TaskTrigger } from "@/types";
import { Button } from "@/ui";

interface TaskOriginProps {
    subject?: TaskSubject;
    trigger?: TaskTrigger;
}

export const TaskOrigin = ({ subject, trigger }: TaskOriginProps) => {
    const navigate = useNavigate();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const source = trigger?.channel ?? trigger?.kind;
    const details = [trigger?.route && `route ${trigger.route}`, trigger?.event, trigger?.deliveryId && `delivery ${trigger.deliveryId}`].filter(Boolean);
    if (!subject && !source && !trigger?.entrypointName && details.length === 0) return null;

    const entrypointId = trigger?.entrypointId;
    const canOpenEntrypoint = !!entrypointId && userHasCapability(CAPABILITIES.readGateways);
    const entrypointName = trigger?.entrypointName ?? entrypointId;

    return (
        <p className="truncate text-xs text-(--secondary-text-wMain)" data-testid="task-origin">
            {subject && <span data-testid="task-origin-subject">{subject.name || subject.id}</span>}
            {subject && (source || entrypointName) && " · "}
            {(source || entrypointName) && "Started by "}
            {source && <span data-testid="task-origin-source">{source}</span>}
            {source && entrypointName && " "}
            {entrypointName &&
                (canOpenEntrypoint ? (
                    <Button variant="link" className="h-auto p-0 text-xs" data-testid="task-origin-entrypoint" onClick={() => navigate(`/entrypoints/${encodeURIComponent(entrypointId)}`)}>
                        {entrypointName}
                    </Button>
                ) : (
                    <span data-testid="task-origin-entrypoint">{entrypointName}</span>
                ))}
            {details.length > 0 && <span data-testid="task-origin-details"> · {details.join(" · ")}</span>}
        </p>
    );
};
