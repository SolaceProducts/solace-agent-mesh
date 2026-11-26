// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { WorkflowConfig } from "@/utils/agentUtils";

import { WorkflowDetailPanel } from "@/features/workflows/WorkflowDetailPanel";

import type { AgentCardInfo } from "@/types";

export type WorkflowPanelView = "details" | "code";

interface WorkflowDetailsSidePanelProps {
    workflow: AgentCardInfo | null;
    config: WorkflowConfig | null;
    view: WorkflowPanelView;
    onClose: () => void;
    onViewChange: (view: WorkflowPanelView) => void;
}

const WorkflowDetailsSidePanel = ({ workflow, config, view: _view, onClose, onViewChange: _onViewChange }: WorkflowDetailsSidePanelProps) => {
    void _view;
    void _onViewChange;

    if (!workflow || !config) {
        return null;
    }

    return <WorkflowDetailPanel workflow={workflow} config={config} onClose={onClose} showOpenButton={false} />;
};

export default WorkflowDetailsSidePanel;
