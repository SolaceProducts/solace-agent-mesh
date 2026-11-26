// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";
import { useSearchParams } from "react-router-dom";

import { ALL_SUBJECTS, type DateRange, DEFAULT_DASHBOARD_PRESET, isSameRange, isSubjectKey, parseRange, presetRange, serializeRange, SHARED_FILTER_PARAM, type UrlWriteOptions } from "./dashboardFilters";

export interface DashboardUrlState {
    subjectKey: string;
    subjectLabel: string | undefined;
    setSubject: (key: string, label?: string) => void;
    range: DateRange;
    setRange: (value: DateRange, options?: UrlWriteOptions) => void;
    hasActiveFilters: boolean;
    resetFilters: () => void;
}

export function useDashboardUrlState(effectiveDefaultRange?: DateRange): DashboardUrlState {
    const [params, setParams] = useSearchParams();

    const update = useCallback(
        (mutate: (next: URLSearchParams) => void, options?: UrlWriteOptions) => {
            setParams(prev => {
                const next = new URLSearchParams(prev);
                mutate(next);
                return next;
            }, options);
        },
        [setParams]
    );

    const subject = params.get(SHARED_FILTER_PARAM.subject);
    const subjectKey = subject !== null && isSubjectKey(subject) ? subject : ALL_SUBJECTS;
    const subjectLabel = params.get(SHARED_FILTER_PARAM.name) ?? undefined;
    const defaultRange = effectiveDefaultRange ?? presetRange(DEFAULT_DASHBOARD_PRESET);
    const rawRange = params.get(SHARED_FILTER_PARAM.range);
    const range = rawRange === null ? defaultRange : parseRange(rawRange);

    return {
        subjectKey,
        subjectLabel,
        setSubject: (key: string, label?: string) =>
            update(next => {
                if (key === ALL_SUBJECTS) {
                    next.delete(SHARED_FILTER_PARAM.subject);
                    next.delete(SHARED_FILTER_PARAM.name);
                } else {
                    next.set(SHARED_FILTER_PARAM.subject, key);
                    if (label) next.set(SHARED_FILTER_PARAM.name, label);
                    else next.delete(SHARED_FILTER_PARAM.name);
                }
            }),
        range,
        setRange: (value: DateRange, options?: UrlWriteOptions) => update(next => (isSameRange(value, defaultRange) ? next.delete(SHARED_FILTER_PARAM.range) : next.set(SHARED_FILTER_PARAM.range, serializeRange(value))), options),
        hasActiveFilters: subjectKey !== ALL_SUBJECTS || !isSameRange(range, defaultRange),
        resetFilters: () =>
            update(next => {
                next.delete(SHARED_FILTER_PARAM.subject);
                next.delete(SHARED_FILTER_PARAM.name);
                next.delete(SHARED_FILTER_PARAM.range);
            }),
    };
}
