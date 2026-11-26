// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { RefreshCcw } from "lucide-react";

import { type ButtonWithCallback, EmptyState } from "@/common";

interface ScheduledTasksGateParams {
    hasNoInvokeAccess: boolean;
    hasCardsError: boolean;
    onRetry: () => void;
    isRetrying: boolean;
}

export function scheduledTasksGate({ hasNoInvokeAccess, hasCardsError, onRetry, isRetrying }: ScheduledTasksGateParams): React.ReactNode | null {
    const refreshButtons: ButtonWithCallback[] = [{ icon: <RefreshCcw className="h-4 w-4" />, text: "Refresh", variant: "outline", disabled: isRetrying, onClick: onRetry }];
    if (hasCardsError) {
        return <EmptyState variant="error" title="Unable to Load Agents" subtitle="We couldn't check which agents and workflows you can schedule against." buttons={refreshButtons} />;
    }
    if (hasNoInvokeAccess) {
        return <EmptyState variant="notFound" title="No Agents or Workflows" subtitle="Contact your administrator if you should have access to scheduled tasks." buttons={refreshButtons} />;
    }
    return null;
}
