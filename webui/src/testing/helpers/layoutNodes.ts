// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { LayoutNode as ActivityLayoutNode } from "@/features/activities/flowChart/utils/types";
import type { LayoutNode as WorkflowLayoutNode } from "@/features/workflowVisualization/utils/types";

type BaseLayoutNode = {
    id: string;
    type: string;
    x: number;
    y: number;
    width: number;
    height: number;
    children: BaseLayoutNode[];
    data: { label: string };
};

type LayoutNodeOverrides<T extends BaseLayoutNode> = Partial<Omit<T, "type" | "data">> & {
    type: T["type"];
    data?: Partial<T["data"]>;
};

function createLayoutNode<T extends BaseLayoutNode>({ id, type, data, ...rest }: LayoutNodeOverrides<T>): T {
    return {
        id: id ?? `${type}-node`,
        type,
        x: 0,
        y: 0,
        width: 280,
        height: 56,
        children: [],
        ...rest,
        data: { label: "", ...data },
    } as unknown as T;
}

export function createActivityLayoutNode(overrides: LayoutNodeOverrides<ActivityLayoutNode>): ActivityLayoutNode {
    return createLayoutNode<ActivityLayoutNode>(overrides);
}

export function createWorkflowLayoutNode(overrides: LayoutNodeOverrides<WorkflowLayoutNode>): WorkflowLayoutNode {
    return createLayoutNode<WorkflowLayoutNode>(overrides);
}
