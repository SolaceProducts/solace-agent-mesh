// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Sparkles, X } from "lucide-react";

import { Button } from "@/ui/button";
import { Label } from "@/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";

import { ALL_SUBJECTS, type DashboardDatePreset, type DateRange, subjectKindOf } from "./dashboardFilters";
import { DateRangeFilter } from "./DateRangeFilter";
import { SUBJECT_TYPE_ICONS } from "./feedbackLabels";

export interface SubjectChoice {
    key: string;
    label: string;
}

interface FeedbackDashboardFiltersProps {
    subjectKey: string;
    subjectChoices: SubjectChoice[];
    subjectChoicesTruncated?: boolean;
    optionsError?: boolean;
    onSubjectChange: (key: string) => void;
    range: DateRange;
    datePresets: ReadonlyArray<{ value: DashboardDatePreset; label: string }>;
    onRangeChange: (range: DateRange) => void;
    minDate?: string;
    maxDate: string;
    hasActiveFilters: boolean;
    onResetFilters: () => void;
    onAskAi?: () => void;
}

export function FeedbackDashboardFilters({
    subjectKey,
    subjectChoices,
    subjectChoicesTruncated,
    optionsError,
    onSubjectChange,
    range,
    datePresets,
    onRangeChange,
    minDate,
    maxDate,
    hasActiveFilters,
    onResetFilters,
    onAskAi,
}: Readonly<FeedbackDashboardFiltersProps>) {
    return (
        <div className="flex flex-wrap items-end gap-4 border-b pb-4">
            <div className="flex flex-col gap-1">
                <Label className="text-(--secondary-text-wMain)">Agent / Workflow</Label>
                <Select value={subjectKey} onValueChange={onSubjectChange}>
                    <SelectTrigger className="w-64" aria-label="Agent / Workflow">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value={ALL_SUBJECTS}>All Agents &amp; Workflows</SelectItem>
                        {subjectChoices.map(choice => {
                            const kind = subjectKindOf(choice.key);
                            const SubjectIcon = kind ? SUBJECT_TYPE_ICONS[kind] : undefined;
                            return (
                                <SelectItem key={choice.key} value={choice.key}>
                                    <span className="flex min-w-0 items-center gap-2">
                                        {SubjectIcon && <SubjectIcon className="size-4 shrink-0 text-(--secondary-text-wMain)" aria-hidden />}
                                        <span className="truncate">{choice.label}</span>
                                    </span>
                                </SelectItem>
                            );
                        })}
                        {subjectChoicesTruncated && <div className="px-2 py-1.5 text-xs text-(--secondary-text-wMain)">Showing the first 100 by name.</div>}
                        {optionsError && <div className="px-2 py-1.5 text-xs text-(--secondary-text-wMain)">Couldn't load the subject list.</div>}
                    </SelectContent>
                </Select>
            </div>

            <div className="flex flex-col gap-1">
                <Label className="text-(--secondary-text-wMain)">Date Range</Label>
                <DateRangeFilter value={range} onChange={onRangeChange} presets={datePresets} minDate={minDate} maxDate={maxDate} />
            </div>

            {hasActiveFilters && (
                <Button variant="ghost" onClick={onResetFilters}>
                    <X className="size-4" /> Reset Filters
                </Button>
            )}

            {onAskAi && (
                <Button variant="outline" className="ml-auto" onClick={onAskAi}>
                    <Sparkles className="size-4" /> Ask AI
                </Button>
            )}
        </div>
    );
}
