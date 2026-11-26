// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useState } from "react";

import type { TargetType } from "@/types/scheduled-tasks";

import { DatePicker, Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Textarea } from "@/ui";
import { cn } from "@/utils";

import { ScheduleBuilder, SchedulePreviewBox, TimeOfDayPicker } from "./ScheduleBuilder";
import { INTERVAL_UNITS, type IntervalUnit, MIN_INTERVAL_SECONDS, parseInterval, type TaskConfig } from "./task-config";
import { describeScheduleExpression } from "./utils";
import { WorkflowInputHint } from "./WorkflowInputHint";

const COMMON_TIMEZONES = [
    "America/New_York",
    "America/Chicago",
    "America/Denver",
    "America/Los_Angeles",
    "America/Toronto",
    "America/Vancouver",
    "Europe/London",
    "Europe/Paris",
    "Europe/Berlin",
    "Asia/Tokyo",
    "Asia/Shanghai",
    "Asia/Singapore",
    "Asia/Dubai",
    "Australia/Sydney",
    "Pacific/Auckland",
    "UTC",
];

const INTERVAL_FALLBACK: { value: number; unit: IntervalUnit } = { value: 30, unit: "m" };

const IntervalValueInput: React.FC<{ value: number; unit: IntervalUnit; onChange: (newValue: number) => void; invalid?: boolean }> = ({ value, unit, onChange, invalid }) => {
    const [draft, setDraft] = useState(Number.isFinite(value) ? String(value) : "");

    useEffect(() => {
        setDraft(Number.isFinite(value) ? String(value) : "");
    }, [value, unit]);

    return (
        <Input
            id="interval-value"
            type="text"
            inputMode="numeric"
            value={draft}
            onChange={e => {
                const next = e.target.value;
                if (next === "" || /^\d+$/.test(next)) {
                    setDraft(next);
                    const n = Number.parseInt(next, 10);
                    if (Number.isFinite(n) && n >= 1) {
                        onChange(n);
                    }
                }
            }}
            onBlur={() => {
                if (!draft || Number.parseInt(draft, 10) < 1) {
                    setDraft(Number.isFinite(value) ? String(value) : "");
                }
            }}
            className={cn("max-w-[7rem]", invalid && "border-(--error-w100)")}
            aria-invalid={invalid}
        />
    );
};

export type ConflictState = { reason: string; fields: Array<"instructions" | "schedule"> } | null;

type UpdateConfig = (updates: Partial<TaskConfig>) => void;

interface SectionProps {
    config: TaskConfig;
    validationErrors: Record<string, string>;
    conflictError: ConflictState;
    updateConfig: UpdateConfig;
}

type FieldSectionProps = Pick<SectionProps, "config" | "validationErrors" | "updateConfig">;

const FieldError: React.FC<{ message?: string }> = ({ message }) => (message ? <p className="text-sm text-(--error-wMain)">{message}</p> : null);

const RequiredMark: React.FC = () => <span className="text-(--primary-wMain)">*</span>;

const BasicInformation: React.FC<FieldSectionProps> = ({ config, validationErrors, updateConfig }) => (
    <div className="space-y-4">
        <h3 className="text-base font-semibold">Basic Information</h3>

        <div className="space-y-2">
            <Label htmlFor="task-name">
                Task Name <RequiredMark />
            </Label>
            <Input id="task-name" placeholder="e.g., Daily Report Generation" value={config.name} onChange={e => updateConfig({ name: e.target.value })} className={validationErrors.name ? "border-(--error-w100)" : ""} />
            <FieldError message={validationErrors.name} />
        </div>

        <div className="space-y-2">
            <Label htmlFor="task-description">Description</Label>
            <Textarea id="task-description" placeholder="e.g., Generates a daily summary report" value={config.description} onChange={e => updateConfig({ description: e.target.value })} rows={3} />
        </div>
    </div>
);

const IntervalFields: React.FC<FieldSectionProps> = ({ config, validationErrors, updateConfig }) => {
    const parsed = parseInterval(config.scheduleExpression) ?? INTERVAL_FALLBACK;
    const invalid = !!validationErrors.scheduleExpression;
    return (
        <div className="space-y-2">
            <Label htmlFor="interval-value">
                Interval <RequiredMark />
            </Label>
            <div className="flex items-center gap-2">
                <IntervalValueInput value={parsed.value} unit={parsed.unit} onChange={n => updateConfig({ scheduleExpression: `${n}${parsed.unit}` })} invalid={invalid} />
                <Select value={parsed.unit} onValueChange={val => updateConfig({ scheduleExpression: `${parsed.value}${val}` })}>
                    <SelectTrigger className={cn("max-w-[10rem]", invalid && "border-(--error-w100)")} aria-invalid={invalid}>
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        {INTERVAL_UNITS.map(u => (
                            <SelectItem key={u.value} value={u.value}>
                                {u.label}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </div>
            <FieldError message={validationErrors.scheduleExpression} />
            <p className="text-xs text-(--secondary-text-wMain)">Interval must be between {MIN_INTERVAL_SECONDS} seconds and 1 year.</p>
        </div>
    );
};

const OneTimeFields: React.FC<FieldSectionProps> = ({ config, validationErrors, updateConfig }) => {
    const [datePart = "", timePart = ""] = config.scheduleExpression.split("T");
    const invalid = !!validationErrors.scheduleExpression;
    return (
        <>
            <div className="space-y-2">
                <Label>
                    Date & Time <RequiredMark />
                </Label>
                <div className="flex items-center gap-2">
                    <DatePicker value={datePart} onChange={date => updateConfig({ scheduleExpression: date ? `${date}T${timePart || "09:00:00"}` : "" })} min={new Date().toISOString().split("T")[0]} invalid={invalid} />
                    <TimeOfDayPicker value={timePart.substring(0, 5)} onChange={time => updateConfig({ scheduleExpression: datePart ? `${datePart}T${time}:00` : "" })} invalid={invalid} />
                </div>
                <FieldError message={validationErrors.scheduleExpression} />
            </div>
            {!invalid && config.scheduleExpression && <SchedulePreviewBox description={describeScheduleExpression("one_time", config.scheduleExpression)} />}
        </>
    );
};

const ScheduleConfiguration: React.FC<SectionProps & { scheduleExprByType: Record<TaskConfig["scheduleType"], string> }> = ({ config, validationErrors, conflictError, scheduleExprByType, updateConfig }) => {
    const scheduleConflict = conflictError?.fields.includes("schedule");
    return (
        <div className="space-y-4">
            <h3 className="text-base font-semibold">Schedule</h3>

            <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                    <Label htmlFor="schedule-type">
                        Schedule Type <RequiredMark />
                    </Label>
                    <Select
                        value={config.scheduleType}
                        onValueChange={value => {
                            const newType = value as TaskConfig["scheduleType"];
                            updateConfig({ scheduleType: newType, scheduleExpression: scheduleExprByType[newType] });
                        }}
                    >
                        <SelectTrigger className={cn("w-full", scheduleConflict && "border-(--warning-w100)")}>
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="cron">Recurring Schedule</SelectItem>
                            <SelectItem value="interval">Interval</SelectItem>
                            <SelectItem value="one_time">One Time</SelectItem>
                        </SelectContent>
                    </Select>
                    {scheduleConflict && <p className="text-sm text-(--warning-wMain)">{conflictError?.reason}</p>}
                </div>

                <div className="space-y-2">
                    <Label htmlFor="timezone">
                        Timezone <RequiredMark />
                    </Label>
                    <Select value={config.timezone} onValueChange={value => updateConfig({ timezone: value })}>
                        <SelectTrigger className={cn("w-full", validationErrors.timezone && "border-(--error-w100)")}>
                            <SelectValue placeholder="Select timezone" />
                        </SelectTrigger>
                        <SelectContent>
                            {COMMON_TIMEZONES.map(tz => (
                                <SelectItem key={tz} value={tz}>
                                    {tz}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <FieldError message={validationErrors.timezone} />
                </div>
            </div>

            {config.scheduleType === "cron" && <ScheduleBuilder value={config.scheduleExpression} onChange={cron => updateConfig({ scheduleExpression: cron })} />}
            {config.scheduleType === "interval" && <IntervalFields config={config} validationErrors={validationErrors} updateConfig={updateConfig} />}
            {config.scheduleType === "one_time" && <OneTimeFields config={config} validationErrors={validationErrors} updateConfig={updateConfig} />}
        </div>
    );
};

interface TargetFieldsProps {
    config: TaskConfig;
    validationErrors: Record<string, string>;
    targetOptions: Array<{ name: string; displayName?: string }>;
    targetNoun: string;
    updateConfig: UpdateConfig;
    taskMessageAfterTargetChange: (nextTargetType: TargetType, nextTargetName: string) => string;
}

const TargetFields: React.FC<TargetFieldsProps> = ({ config, validationErrors, targetOptions, targetNoun, updateConfig, taskMessageAfterTargetChange }) => (
    <div className="grid grid-cols-2 gap-4">
        <div className="space-y-2">
            <Label htmlFor="target-type">
                Target <RequiredMark />
            </Label>
            <Select value={config.targetType} onValueChange={value => updateConfig({ targetType: value as TargetType, targetAgentName: "", taskMessage: taskMessageAfterTargetChange(value as TargetType, "") })}>
                <SelectTrigger className="w-full">
                    <SelectValue />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="agent">Agent</SelectItem>
                    <SelectItem value="workflow">Workflow</SelectItem>
                </SelectContent>
            </Select>
        </div>

        <div className="space-y-2">
            <Label htmlFor="target-agent">
                {config.targetType === "workflow" ? "Workflow" : "Agent"} <RequiredMark />
            </Label>
            <Select value={config.targetAgentName} onValueChange={value => updateConfig({ targetAgentName: value, taskMessage: taskMessageAfterTargetChange(config.targetType, value) })}>
                <SelectTrigger className={cn("w-full", validationErrors.targetAgentName && "border-(--error-w100)")}>
                    <SelectValue placeholder={`Select a ${targetNoun}`} />
                </SelectTrigger>
                <SelectContent>
                    {targetOptions.length === 0 ? (
                        <div className="px-2 py-1.5 text-sm text-(--secondary-text-wMain)">No {targetNoun}s available</div>
                    ) : (
                        targetOptions.map(agent => (
                            <SelectItem key={agent.name} value={agent.name}>
                                {agent.displayName || agent.name}
                            </SelectItem>
                        ))
                    )}
                </SelectContent>
            </Select>
            <FieldError message={validationErrors.targetAgentName} />
        </div>
    </div>
);

const InstructionsField: React.FC<SectionProps & { workflowInputSchema: Record<string, unknown> | undefined }> = ({ config, validationErrors, conflictError, workflowInputSchema, updateConfig }) => {
    const instructionsConflict = conflictError?.fields.includes("instructions");
    return (
        <div className="space-y-2">
            <Label htmlFor="task-message">
                Instructions <RequiredMark />
            </Label>
            {workflowInputSchema ? <WorkflowInputHint schema={workflowInputSchema} /> : <p className="text-sm text-(--secondary-text-wMain)">Enter the task instructions for the agent to execute on the configured schedule.</p>}
            <Textarea
                id="task-message"
                placeholder={workflowInputSchema ? "Enter the workflow input as a JSON object..." : "Enter the message to send to the agent..."}
                value={config.taskMessage}
                onChange={e => updateConfig({ taskMessage: e.target.value })}
                rows={6}
                className={cn(workflowInputSchema && "font-mono", validationErrors.taskMessage && "border-(--error-w100)", !validationErrors.taskMessage && instructionsConflict && "border-(--warning-w100)")}
            />
            <p className="text-xs text-(--secondary-text-wMain)">Ensure the instructions match the schedule and task configuration.</p>
            <FieldError message={validationErrors.taskMessage} />
            {instructionsConflict && (
                <p className="text-sm text-(--warning-wMain)">
                    {conflictError?.reason} <span className="text-xs text-(--secondary-text-wMain)">— if that's intended you can ignore this; otherwise adjust the interval, or edit the instructions/name to match the cadence.</span>
                </p>
            )}
        </div>
    );
};

interface ManualTaskFormProps extends SectionProps {
    scheduleExprByType: Record<TaskConfig["scheduleType"], string>;
    targetOptions: Array<{ name: string; displayName?: string }>;
    targetNoun: string;
    workflowInputSchema: Record<string, unknown> | undefined;
    taskMessageAfterTargetChange: (nextTargetType: TargetType, nextTargetName: string) => string;
}

export const ManualTaskForm: React.FC<ManualTaskFormProps> = ({ config, validationErrors, conflictError, scheduleExprByType, targetOptions, targetNoun, workflowInputSchema, updateConfig, taskMessageAfterTargetChange }) => (
    <div className="flex-1 overflow-y-auto px-8 py-6">
        <div className="mx-auto max-w-4xl space-y-6">
            <BasicInformation config={config} validationErrors={validationErrors} updateConfig={updateConfig} />
            <ScheduleConfiguration config={config} validationErrors={validationErrors} conflictError={conflictError} scheduleExprByType={scheduleExprByType} updateConfig={updateConfig} />

            <div className="space-y-4">
                <h3 className="text-base font-semibold">Task Configuration</h3>

                <div className="space-y-2">
                    <Label>
                        Output <RequiredMark />
                    </Label>
                    <p className="text-sm">Chat</p>
                </div>

                <TargetFields config={config} validationErrors={validationErrors} targetOptions={targetOptions} targetNoun={targetNoun} updateConfig={updateConfig} taskMessageAfterTargetChange={taskMessageAfterTargetChange} />
                <InstructionsField config={config} validationErrors={validationErrors} conflictError={conflictError} workflowInputSchema={workflowInputSchema} updateConfig={updateConfig} />
            </div>
        </div>
    </div>
);
