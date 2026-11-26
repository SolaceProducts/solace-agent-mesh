// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ColumnDef } from "@tanstack/react-table";

import { type AgentPerformanceMetrics } from "@/types/activities";

import { type AgentTableFeatures } from "./agentTableFeatures";
import { DataTableColumnHeader } from "./DataTableColumnHeader";

const formatMilliseconds = (ms: number) => `${(ms / 1000).toFixed(2)}s`;

export const getAgentColumns = (): ColumnDef<AgentTableFeatures, AgentPerformanceMetrics>[] => [
    {
        accessorKey: "displayName",
        header: ({ column }) => <DataTableColumnHeader column={column} title="Agent Name" />,
        cell: ({ row }) => <div>{row.getValue("displayName")}</div>,
    },
    {
        id: "totalActiveTime",
        accessorFn: agent => agent.totalLlmTimeMs + agent.totalToolTimeMs,
        header: ({ column }) => <DataTableColumnHeader column={column} title="Total Active Time" />,
        cell: ({ row }) => <div>{formatMilliseconds(row.getValue("totalActiveTime"))}</div>,
        sortFn: "basic",
    },
    {
        accessorKey: "totalLlmTimeMs",
        header: ({ column }) => <DataTableColumnHeader column={column} title="LLM Time" />,
        cell: ({ row }) => <div>{formatMilliseconds(row.getValue("totalLlmTimeMs"))}</div>,
    },
    {
        accessorKey: "totalToolTimeMs",
        header: ({ column }) => <DataTableColumnHeader column={column} title="Tool Time" />,
        cell: ({ row }) => <div>{formatMilliseconds(row.getValue("totalToolTimeMs"))}</div>,
    },
];
