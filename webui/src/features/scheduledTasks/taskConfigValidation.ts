// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { intervalToSeconds, type IntervalUnit, MAX_INTERVAL_SECONDS, MIN_INTERVAL_SECONDS, type TaskConfig } from "./task-config";
import { validateWorkflowInput } from "./workflowInput";

type JsonObject = Record<string, unknown>;

const ZONE_SUFFIX = /(Z|[+-]\d{2}:?\d{2})$/;
const STRICT_ISO_8601_LOCAL_DATE_TIME = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?(\.\d+)?$/;

function oneTimeExpressionError(expression: string): string | undefined {
    if (!STRICT_ISO_8601_LOCAL_DATE_TIME.test(expression.trim().replace(ZONE_SUFFIX, ""))) {
        return "Invalid date & time — use format YYYY-MM-DDTHH:MM:SS";
    }
    const scheduled = new Date(expression);
    if (Number.isNaN(scheduled.getTime())) {
        return "Invalid date & time";
    }
    return scheduled.getTime() <= Date.now() ? "Scheduled time must be in the future" : undefined;
}

function intervalExpressionError(expression: string): string | undefined {
    const match = /^(\d+)([smhd])$/i.exec(expression.trim());
    if (!match) {
        return "Interval must be a positive number followed by s, m, h, or d (e.g. 30m)";
    }
    const seconds = intervalToSeconds(Number.parseInt(match[1], 10), match[2].toLowerCase() as IntervalUnit);
    if (seconds < MIN_INTERVAL_SECONDS) {
        return `Interval must be at least ${MIN_INTERVAL_SECONDS} seconds`;
    }
    return seconds > MAX_INTERVAL_SECONDS ? "Interval must be at most 1 year" : undefined;
}

function scheduleExpressionError(config: TaskConfig): string | undefined {
    if (!config.scheduleExpression.trim()) {
        return "Schedule expression is required";
    }
    if (config.scheduleType === "one_time") {
        return oneTimeExpressionError(config.scheduleExpression);
    }
    return config.scheduleType === "interval" ? intervalExpressionError(config.scheduleExpression) : undefined;
}

function taskMessageError(taskMessage: string, workflowInputSchema: JsonObject | undefined): string | undefined {
    if (!taskMessage.trim()) {
        return "Instructions are required";
    }
    return workflowInputSchema ? (validateWorkflowInput(taskMessage, workflowInputSchema) ?? undefined) : undefined;
}

export function validateTaskConfig(config: TaskConfig, workflowInputSchema: JsonObject | undefined): Record<string, string> {
    const candidates: Record<string, string | undefined> = {
        name: config.name.trim() ? undefined : "Task name is required",
        scheduleExpression: scheduleExpressionError(config),
        timezone: config.timezone.trim() ? undefined : "Timezone is required",
        targetAgentName: config.targetAgentName.trim() ? undefined : "Target agent is required",
        taskMessage: taskMessageError(config.taskMessage, workflowInputSchema),
    };
    return Object.fromEntries(Object.entries(candidates).filter((entry): entry is [string, string] => entry[1] !== undefined));
}
