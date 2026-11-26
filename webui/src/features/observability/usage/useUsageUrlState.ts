// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";

import type { UsageKind } from "@/api/usage";

import { serializeDetailTarget, type UsageDetailTarget } from "./usageDetailTarget";
import { isUsageKind, USAGE_KINDS } from "./usageKinds";
import { DEFAULT_USAGE_RANGE, parseUsageRange, serializeUsageRange, type UsageRange } from "./usagePeriod";

const PERIOD_PARAM = "period";
const KIND_PARAM = "type";
const DETAIL_PARAM = "detail";
const NAME_PARAM = "name";
const DEFAULT_KIND: UsageKind = "agents";

export function usageDetailSearchFor(target: UsageDetailTarget): string {
    const params = new URLSearchParams();
    if (target.kind !== DEFAULT_KIND) params.set(KIND_PARAM, target.kind);
    params.set(DETAIL_PARAM, serializeDetailTarget(target));
    return `?${params.toString()}`;
}

function navigationFor(target: UsageDetailTarget | null, panelOpen: boolean, state: unknown): { replace?: boolean; state?: unknown } {
    if (target === null) return { replace: true };
    return panelOpen ? { replace: true, state } : { state: { usagePanel: true } };
}

export function useUsageUrlState(kinds: readonly UsageKind[] = USAGE_KINDS) {
    const [searchParams, setSearchParams] = useSearchParams();
    const serializedRange = serializeUsageRange(parseUsageRange(searchParams.get(PERIOD_PARAM)));
    const rawKind = searchParams.get(KIND_PARAM);
    const kind = isUsageKind(rawKind) && kinds.includes(rawKind) ? rawKind : DEFAULT_KIND;
    const serializedDetail = searchParams.get(DETAIL_PARAM);
    const nameFilter = searchParams.get(NAME_PARAM) ?? "";
    const location = useLocation();

    const setRange = useCallback(
        (range: UsageRange, { replace = false }: { replace?: boolean } = {}) => {
            setSearchParams(
                prev => {
                    const next = new URLSearchParams(prev);
                    if (serializeUsageRange(range) === serializeUsageRange(DEFAULT_USAGE_RANGE)) next.delete(PERIOD_PARAM);
                    else next.set(PERIOD_PARAM, serializeUsageRange(range));
                    return next;
                },
                replace ? { replace, state: location.state } : undefined
            );
        },
        [setSearchParams, location.state]
    );

    const setKind = useCallback(
        (next: UsageKind) => {
            setSearchParams(prev => {
                const params = new URLSearchParams(prev);
                if (next === DEFAULT_KIND) params.delete(KIND_PARAM);
                else params.set(KIND_PARAM, next);
                params.delete(NAME_PARAM);
                return params;
            });
        },
        [setSearchParams]
    );

    const setNameFilter = useCallback(
        (name: string) => {
            setSearchParams(
                prev => {
                    const params = new URLSearchParams(prev);
                    if (name === "") params.delete(NAME_PARAM);
                    else params.set(NAME_PARAM, name);
                    return params;
                },
                { replace: true, state: location.state, flushSync: true }
            );
        },
        [setSearchParams, location.state]
    );

    const navigate = useNavigate();
    const openedHere = (location.state as { usagePanel?: boolean } | null)?.usagePanel === true;

    const setDetail = useCallback(
        (target: UsageDetailTarget | null) => {
            if (target === null && openedHere) {
                navigate(-1);
                return;
            }
            if (target !== null && serializeDetailTarget(target) === serializedDetail) return;
            setSearchParams(
                prev => {
                    const params = new URLSearchParams(prev);
                    if (target) params.set(DETAIL_PARAM, serializeDetailTarget(target));
                    else params.delete(DETAIL_PARAM);
                    return params;
                },
                navigationFor(target, serializedDetail !== null, location.state)
            );
        },
        [setSearchParams, navigate, openedHere, serializedDetail, location.state]
    );

    return { serializedRange, setRange, kind, setKind, nameFilter, setNameFilter, serializedDetail, setDetail };
}
