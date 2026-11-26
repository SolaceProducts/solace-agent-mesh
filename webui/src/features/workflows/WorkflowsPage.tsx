// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { useBooleanFlagDetails } from "@openfeature/react-sdk";
import { RefreshCcw } from "lucide-react";

import { useWorkflowsQuery } from "@/api/workflows";

import { useUserContext } from "@/providers/contexts";

import { SCOPES } from "@/utils/scopes";

import { Button } from "@/ui/button";
import { LifecycleBadge } from "@/ui/lifecycleBadge";

import { toAgentCardInfo } from "@/features/workflows/toAgentCardInfo";
import { useCreateWorkflowInBuilder } from "@/features/workflows/useCreateWorkflowInBuilder";
import { useEditWorkflowInBuilder } from "@/features/workflows/useEditWorkflowInBuilder";
import { WorkflowList } from "@/features/workflows/WorkflowList";

import { EmptyState, Header } from "@/common";

export function WorkflowsPage() {
    const { data: platformWorkflows = [], isLoading, error, refetch } = useWorkflowsQuery();
    const { userHasCapability, CAPABILITIES, hasScope } = useUserContext();
    const { value: rbacEnabled } = useBooleanFlagDetails("rbac", false);

    const workflowAgents = useMemo(() => platformWorkflows.map(toAgentCardInfo), [platformWorkflows]);
    const workflowIdByName = useMemo(() => Object.fromEntries(platformWorkflows.map(w => [w.name, w.id])), [platformWorkflows]);

    const handleRefresh = () => {
        refetch();
    };

    const handleEditInBuilder = useEditWorkflowInBuilder();
    const handleBuildWithAI = useCreateWorkflowInBuilder();

    if (isLoading) {
        return <EmptyState title="Loading..." variant="loading" />;
    }

    if (error) {
        return <EmptyState title="Error loading workflows" subtitle={error.message} variant="error" />;
    }

    const canCreate = userHasCapability(CAPABILITIES.createWorkflows);
    const canUpdate = userHasCapability(CAPABILITIES.updateWorkflows);
    const canDelete = userHasCapability(CAPABILITIES.deleteWorkflows);
    const canViewAccess = rbacEnabled && (userHasCapability(CAPABILITIES.readWorkflows) || hasScope(SCOPES.rbac.read));

    return (
        <div className="flex h-full w-full flex-col overflow-x-auto">
            <div className="flex h-full min-w-0 flex-col">
                <Header
                    title={
                        <>
                            Workflows <LifecycleBadge>EARLY ACCESS</LifecycleBadge>
                        </>
                    }
                    buttons={[
                        <Button key="refresh" variant="ghost" title="Refresh Workflows" onClick={handleRefresh}>
                            <RefreshCcw />
                            <span className="hidden sm:inline">Refresh</span>
                        </Button>,
                    ]}
                />
                <div className="relative min-h-0 flex-1">
                    <WorkflowList
                        workflows={workflowAgents}
                        className="bg-(--background-w10)"
                        onEditInBuilder={canUpdate ? handleEditInBuilder : undefined}
                        onBuildWithAI={canCreate ? handleBuildWithAI : undefined}
                        canDelete={canDelete}
                        canEdit={canUpdate}
                        canViewAccess={canViewAccess}
                        workflowIdByName={workflowIdByName}
                    />
                </div>
            </div>
        </div>
    );
}
