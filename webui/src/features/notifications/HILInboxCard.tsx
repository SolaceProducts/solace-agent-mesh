// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useState } from "react";

import { useRespondToPrompt } from "@/api/notifications";
import type { NotificationItem } from "@/api/notifications/types";

import { A2UISurfaceRenderer } from "@/features/chat/a2ui/A2UIRenderer";
import type { SurfaceAction } from "@/features/chat/a2ui/types";

export function HILInboxCard({ item, hideTitle = false }: Readonly<{ item: NotificationItem; hideTitle?: boolean }>) {
    const respond = useRespondToPrompt();
    const [done, setDone] = useState<string | null>(null);

    const handleAction = useCallback(
        (action: SurfaceAction) => {
            const cancelled = action.eventName === "cancel";
            respond.mutate(
                {
                    id: item.id,
                    status: cancelled ? "cancelled" : "answered",
                    data: cancelled ? {} : action.context,
                },
                { onSuccess: () => setDone(action.completionText || (cancelled ? "Cancelled." : "Response submitted.")) }
            );
        },
        [item.id, respond]
    );

    if (!item.surface) {
        return null;
    }

    const respondError = respond.error as (Error & { status?: number }) | null;
    const errorMessage = respondError?.status ? respondError.message : "Failed to submit your response. Please try again.";

    return (
        <div className="rounded-md border border-(--secondary-w40) p-4">
            {item.title && !hideTitle && <p className="mb-2 text-sm font-semibold">{item.title}</p>}
            {done ? (
                <p className="text-sm text-(--secondary-text-wMain)">✓ {done}</p>
            ) : (
                <>
                    <A2UISurfaceRenderer surface={item.surface} onAction={handleAction} disabled={respond.isPending} />
                    {respond.isError && <p className="mt-2 text-xs text-(--error-text-wMain)">{errorMessage}</p>}
                </>
            )}
        </div>
    );
}
