// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useUserContext } from "@/providers/contexts";

import { useEditWorkflowInBuilder } from "@/features/workflows/useEditWorkflowInBuilder";
import { WorkflowVisualizationPage } from "@/features/workflowVisualization/WorkflowVisualizationPage";

export function WorkflowVisualizationPageWrapper() {
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canEdit = userHasCapability(CAPABILITIES.updateWorkflows);

    const handleEdit = useEditWorkflowInBuilder();

    return <WorkflowVisualizationPage onEdit={canEdit ? handleEdit : undefined} />;
}
