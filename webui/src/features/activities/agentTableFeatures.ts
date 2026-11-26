// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { columnVisibilityFeature, createSortedRowModel, rowSortingFeature, sortFn_alphanumeric, sortFn_basic, sortFn_text, tableFeatures } from "@tanstack/react-table";

export const agentTableFeatures = tableFeatures({
    columnVisibilityFeature,
    rowSortingFeature,
    sortedRowModel: createSortedRowModel(),
    sortFns: { alphanumeric: sortFn_alphanumeric, basic: sortFn_basic, text: sortFn_text },
});

export type AgentTableFeatures = typeof agentTableFeatures;
