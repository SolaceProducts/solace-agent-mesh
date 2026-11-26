// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef } from "react";
import { useSearchParams } from "react-router-dom";

import { isSubjectKey, SHARED_FILTER_PARAM, type UrlWriteOptions } from "./dashboardFilters";
import { useFeedbackFilterMemory } from "./feedbackFilterMemory";
import { type DateRange, DEFAULT_DATE_PRESET, isSameRange, parseRange, presetRange, serializeRange } from "./feedbackFilters";

export const ANY_SUBJECT = "all";
export const ANY_RATING = "all";
export type RatingFilter = "all" | "up" | "down";

const PARAM = { ...SHARED_FILTER_PARAM, rating: "rating", user: "user", page: "page", selected: "selected" } as const;

const isRating = (value: string | null): value is RatingFilter => value === "up" || value === "down" || value === ANY_RATING;

function parsePage(value: string | null): number {
    const page = Number(value);
    return Number.isInteger(page) && page >= 1 ? page : 1;
}

interface RememberedRating {
    rating: RatingFilter;
}

export interface FeedbackHistoryUrlState {
    subjectKey: string;
    setSubjectKey: (value: string, label?: string) => void;
    rating: RatingFilter;
    setRating: (value: RatingFilter) => void;
    userId: string | null;
    setUserId: (value: string | null) => void;
    range: DateRange;
    setRange: (value: DateRange, options?: UrlWriteOptions) => void;
    page: number;
    setPage: (page: number) => void;
    selectedId: string | null;
    setSelectedId: (id: string | null) => void;
    hasActiveFilters: boolean;
    clearFilters: () => void;
}

export function useFeedbackHistoryUrlState(effectiveDefaultRange?: DateRange): FeedbackHistoryUrlState {
    const [params, setParams] = useSearchParams();
    const { read, remember } = useFeedbackFilterMemory();

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

    const setFilter = useCallback(
        (key: string, value: string, defaultValue: string, options?: UrlWriteOptions) =>
            update(next => {
                if (value === defaultValue) next.delete(key);
                else next.set(key, value);
                next.delete(PARAM.page);
            }, options),
        [update]
    );

    const hasRestored = useRef(false);

    useEffect(() => {
        if (hasRestored.current) return;
        hasRestored.current = true;
        if (params.has(PARAM.rating)) return;
        const restoredRating = read<RememberedRating>("history");
        if (!restoredRating || restoredRating.rating === ANY_RATING) return;
        setParams(
            prev => {
                const next = new URLSearchParams(prev);
                next.set(PARAM.rating, restoredRating.rating);
                return next;
            },
            { replace: true }
        );
    }, [params, read, setParams]);

    const rating = params.get(PARAM.rating);
    const subject = params.get(PARAM.subject);
    const subjectKey = subject !== null && isSubjectKey(subject) ? subject : ANY_SUBJECT;
    const ratingValue: RatingFilter = isRating(rating) ? rating : ANY_RATING;
    const defaultRange = effectiveDefaultRange ?? presetRange(DEFAULT_DATE_PRESET);
    const rawRange = params.get(PARAM.range);
    const range = rawRange === null ? defaultRange : parseRange(rawRange);
    const userId = params.get(PARAM.user) || null;

    useEffect(() => {
        remember("history", { rating: ratingValue });
    }, [ratingValue, remember]);

    return {
        subjectKey,
        setSubjectKey: (value: string, label?: string) =>
            update(next => {
                if (value === ANY_SUBJECT) {
                    next.delete(PARAM.subject);
                    next.delete(PARAM.name);
                } else {
                    next.set(PARAM.subject, value);
                    if (label) next.set(PARAM.name, label);
                    else next.delete(PARAM.name);
                }
                next.delete(PARAM.page);
            }),
        rating: ratingValue,
        setRating: (value: RatingFilter) => setFilter(PARAM.rating, value, ANY_RATING),
        userId,
        setUserId: (value: string | null) =>
            update(next => {
                if (value) next.set(PARAM.user, value);
                else next.delete(PARAM.user);
                next.delete(PARAM.page);
            }),
        range,
        setRange: (value: DateRange, options?: UrlWriteOptions) => setFilter(PARAM.range, serializeRange(value), serializeRange(defaultRange), options),
        page: parsePage(params.get(PARAM.page)),
        setPage: (page: number) => update(next => (page <= 1 ? next.delete(PARAM.page) : next.set(PARAM.page, String(page)))),
        selectedId: params.get(PARAM.selected),
        setSelectedId: (id: string | null) => update(next => (id ? next.set(PARAM.selected, id) : next.delete(PARAM.selected))),
        hasActiveFilters: subjectKey !== ANY_SUBJECT || ratingValue !== ANY_RATING || userId !== null || !isSameRange(range, defaultRange),
        clearFilters: () =>
            update(next => {
                next.delete(PARAM.subject);
                next.delete(PARAM.rating);
                next.delete(PARAM.user);
                next.delete(PARAM.range);
                next.delete(PARAM.page);
            }),
    };
}
