// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useRef, useState } from "react";
import { CalendarDays, NotepadText } from "lucide-react";

import { Badge, CardTitle, Label } from "@/ui";

import { INTERVAL_UNIT_LABELS, parseInterval, type TaskConfig } from "./task-config";
import { useTargetDisplayName } from "./useTargetDisplayName";
import { describeScheduleExpression } from "./utils";

function getScheduleTypeLabel(type: string) {
    switch (type) {
        case "cron":
            return "Recurring Schedule";
        case "interval":
            return "Interval";
        case "one_time":
            return "One Time";
        default:
            return type;
    }
}

interface TaskPreviewPanelProps {
    config: TaskConfig;
    highlightedFields: string[];
}

const UpdatedBadge: React.FC = () => (
    <Badge variant="default" className="bg-(--primary-wMain) text-xs text-(--primary-text-w10)">
        Updated
    </Badge>
);

const FieldRow: React.FC<{ label: string; value?: string; emptyLabel?: string; updated?: boolean; mono?: boolean; multiline?: boolean; indented?: boolean; required?: boolean }> = ({
    label,
    value,
    emptyLabel,
    updated,
    mono,
    multiline,
    indented,
    required = false,
}) => {
    const isEmpty = !value || !value.trim();
    const valueClasses = indented ? `rounded-md px-3 py-2 text-sm ${multiline ? "min-h-[120px] whitespace-pre-wrap" : ""} ${mono ? "font-mono" : ""}` : `text-sm ${mono ? "font-mono" : ""} ${multiline ? "min-h-[120px] whitespace-pre-wrap" : ""}`;
    return (
        <div className="space-y-2">
            <div className="flex items-center gap-2">
                <Label className="text-sm font-medium text-(--secondary-text-wMain)">
                    {label}
                    {required && <span className="text-(--primary-wMain)"> *</span>}
                </Label>
                {updated && <UpdatedBadge />}
            </div>
            <div className={valueClasses}>{isEmpty ? <span className="text-(--secondary-text-wMain) italic">{emptyLabel || `No ${label.toLowerCase()} yet`}</span> : value}</div>
        </div>
    );
};

export const TaskPreviewPanel: React.FC<TaskPreviewPanelProps> = ({ config, highlightedFields }) => {
    const hasContent = !!config.name?.trim();
    const hadContentOnPreviousRenderRef = useRef(false);
    const [showBadges, setShowBadges] = useState(false);

    useEffect(() => {
        if (highlightedFields.length > 0 && hadContentOnPreviousRenderRef.current) {
            setShowBadges(true);
        } else if (highlightedFields.length === 0) {
            setShowBadges(false);
        }
        hadContentOnPreviousRenderRef.current = hasContent;
    }, [highlightedFields, hasContent]);

    const isFieldHighlighted = (field: string) => highlightedFields.includes(field) && showBadges;

    const targetAgentDisplay = useTargetDisplayName(config.targetAgentName);

    const friendlyExpression = describeScheduleExpression(config.scheduleType, config.scheduleExpression);
    const showFriendlyPreview = !!config.scheduleExpression && config.scheduleType !== "interval" && config.scheduleType !== "one_time";

    const interval = config.scheduleType === "interval" ? parseInterval(config.scheduleExpression) : null;
    const oneTimeDate = config.scheduleType === "one_time" ? config.scheduleExpression.split("T")[0] || "" : "";
    const oneTimeTime = config.scheduleType === "one_time" ? (config.scheduleExpression.split("T")[1] || "").substring(0, 5) : "";

    return (
        <div className="flex h-full flex-col">
            <div className="hidden border-b px-4 py-3 sm:block">
                <div className="flex items-center gap-2">
                    <div className="flex h-8 w-8 items-center justify-center rounded-full bg-(--secondary-w10)">
                        <CalendarDays className="h-4 w-4 text-(--secondary-text-wMain)" />
                    </div>
                    <h3 className="text-sm font-semibold">Preview Scheduled Task</h3>
                </div>
            </div>

            {hasContent ? (
                <div className="flex-1 space-y-6 overflow-y-auto px-6 py-6">
                    <div>
                        <CardTitle className="mb-4 text-base">Basic Information</CardTitle>
                        <div className="space-y-4">
                            <FieldRow label="Task Name" value={config.name} updated={isFieldHighlighted("name")} indented required />
                            <FieldRow label="Description" value={config.description} updated={isFieldHighlighted("description")} indented />
                        </div>
                    </div>

                    <div>
                        <CardTitle className="mb-4 text-base">Schedule</CardTitle>
                        <div className="space-y-4">
                            <div className="grid grid-cols-2 gap-x-6 gap-y-4">
                                <FieldRow label="Schedule Type" value={getScheduleTypeLabel(config.scheduleType)} updated={isFieldHighlighted("scheduleType")} required />
                                <FieldRow label="Timezone" value={config.timezone} updated={isFieldHighlighted("timezone")} required />
                            </div>

                            {config.scheduleType === "interval" && (
                                <div className="space-y-2">
                                    <div className="flex items-center gap-2">
                                        <Label className="text-sm font-medium text-(--secondary-text-wMain)">Interval</Label>
                                        {isFieldHighlighted("scheduleExpression") && <UpdatedBadge />}
                                    </div>
                                    <div className="flex items-center gap-3 text-sm">
                                        {interval ? (
                                            <>
                                                <span>{interval.value}</span>
                                                <span className="text-(--secondary-text-wMain)">{INTERVAL_UNIT_LABELS[interval.unit]}</span>
                                            </>
                                        ) : (
                                            <span className="text-(--secondary-text-wMain) italic">No interval yet</span>
                                        )}
                                    </div>
                                </div>
                            )}

                            {config.scheduleType === "one_time" && (
                                <div className="space-y-2">
                                    <div className="flex items-center gap-2">
                                        <Label className="text-sm font-medium text-(--secondary-text-wMain)">Date &amp; Time</Label>
                                        {isFieldHighlighted("scheduleExpression") && <UpdatedBadge />}
                                    </div>
                                    <div className="flex items-center gap-3 text-sm">
                                        {oneTimeDate || oneTimeTime ? (
                                            <>
                                                <span>{oneTimeDate || "—"}</span>
                                                <span>{oneTimeTime || "—"}</span>
                                            </>
                                        ) : (
                                            <span className="text-(--secondary-text-wMain) italic">No date selected yet</span>
                                        )}
                                    </div>
                                </div>
                            )}

                            {showFriendlyPreview && (
                                <div className="space-y-2">
                                    <Label className="text-sm font-medium text-(--secondary-text-wMain)">Preview</Label>
                                    <div className="rounded-md bg-(--background-w10) px-3 py-2 text-sm">{friendlyExpression}</div>
                                </div>
                            )}
                        </div>
                    </div>

                    <div>
                        <CardTitle className="mb-4 text-base">Task Configuration</CardTitle>
                        <div className="space-y-4">
                            <FieldRow label="Output" value="Chat" required />
                            <div className="grid grid-cols-2 gap-x-6 gap-y-4">
                                <FieldRow label="Target" value={config.targetType === "workflow" ? "Workflow" : "Agent"} updated={isFieldHighlighted("targetType")} required />
                                <FieldRow label={config.targetType === "workflow" ? "Workflow" : "Agent"} value={targetAgentDisplay} updated={isFieldHighlighted("targetAgentName")} emptyLabel="Not set" required />
                            </div>
                            <FieldRow label="Instructions" value={config.taskMessage} updated={isFieldHighlighted("taskMessage")} multiline indented emptyLabel="No instructions yet" required />
                        </div>
                    </div>
                </div>
            ) : (
                <div className="flex h-full flex-col items-center justify-center p-8 text-center">
                    <div className="mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-(--secondary-w10)">
                        <NotepadText className="h-8 w-8 text-(--secondary-text-wMain)" />
                    </div>
                    <h3 className="mb-2 text-lg font-semibold">No Scheduled Task Yet</h3>
                    <p className="max-w-sm text-sm text-(--secondary-text-wMain)">Start chatting with the AI assistant to create your template. The preview will update in real-time as you describe your task.</p>
                </div>
            )}
        </div>
    );
};
