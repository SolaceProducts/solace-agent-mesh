// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { IN_PROGRESS_STATUSES, STATUS_LABELS } from "@/types/scheduled-tasks";

import { cn } from "@/utils";

export { IN_PROGRESS_STATUSES, STATUS_LABELS };

export const getStatusBadge = (status: string) => {
    const statusConfig = {
        completed: { bg: "bg-(--success-w20)", text: "text-(--success-wMain)", label: "Completed" },
        quiet: { bg: "bg-(--secondary-w20)", text: "text-(--secondary-text-wMain)", label: "Nothing to report" },
        failed: { bg: "bg-(--error-w20)", text: "text-(--error-wMain)", label: "Failed" },
        pending: { bg: "bg-(--info-w20)", text: "text-(--info-wMain)", label: "Pending" },
        running: { bg: "bg-(--info-w20)", text: "text-(--info-wMain)", label: "Running" },
        timeout: { bg: "bg-(--warning-w20)", text: "text-(--warning-wMain)", label: "Timeout" },
        cancelled: { bg: "bg-(--secondary-w20)", text: "text-(--secondary-text-wMain)", label: "Cancelled" },
        skipped: { bg: "bg-(--secondary-w20)", text: "text-(--secondary-text-wMain)", label: "Skipped" },
        active: { bg: "bg-(--success-w20)", text: "text-(--success-wMain)", label: "Active" },
        paused: { bg: "bg-(--secondary-w20)", text: "text-(--secondary-text-wMain)", label: "Paused" },
        error: { bg: "bg-(--error-w20)", text: "text-(--error-wMain)", label: "Error" },
    };
    const config = statusConfig[status as keyof typeof statusConfig] ?? { bg: "bg-(--secondary-w20)", text: "text-(--secondary-text-wMain)", label: status };
    return <span className={cn("inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium", config.bg, config.text)}>{config.label}</span>;
};

interface StatusBadgeProps {
    status: string;
}

export const StatusBadge: React.FC<StatusBadgeProps> = ({ status }) => getStatusBadge(status);
