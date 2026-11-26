// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Bot, GitBranch, Layers, type LucideIcon, Repeat, Split, Workflow, Wrench } from "lucide-react";

import type { UsageNodeKind, UsageRootKind } from "@/api/usage";

export const NODE_KIND_ICONS: Record<UsageNodeKind, LucideIcon> = {
    agent: Bot,
    workflow: Workflow,
    tool: Wrench,
    switch: Split,
    loop: Repeat,
    map: Layers,
    decision: GitBranch,
};

export { CircleDashed as REMOVED_NODE_ICON } from "lucide-react";

export const ROOT_KIND_ICONS: Record<UsageRootKind, LucideIcon> = { agent: Bot, workflow: Workflow };
export const ROOT_KIND_LABELS: Record<UsageRootKind, string> = { agent: "Agent", workflow: "Workflow" };
