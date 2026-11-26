// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQuery } from "@tanstack/react-query";

import type { PaginatedResponse } from "@/api/common";
import { evaluationsApi } from "@/api/evaluations";

import type { EvalReportTarget, EvalRunGroupSummary, EvalTargetType } from "@/types/eval";

const EVAL_REPORTS_PAGINATED_KEY = "evalReportsPaginated";
const EVAL_REPORTS_AGENTS_KEY = "evalReportsAgents";

export interface UseEvalReportsListOptions {
    pageNumber: number;
    pageSize: number;
    name?: string;
    agent?: string;
    targetType?: EvalTargetType;
}

export function useEvalReportsList(options: UseEvalReportsListOptions) {
    const { pageNumber, pageSize, name, agent, targetType } = options;
    const viewMaySkipActiveRuns = pageNumber > 1 || (name?.length ?? 0) > 0 || (agent?.length ?? 0) > 0 || !!targetType;
    return useQuery<PaginatedResponse<EvalRunGroupSummary>, Error>({
        queryKey: [EVAL_REPORTS_PAGINATED_KEY, { pageNumber, pageSize, name: name ?? null, agent: agent ?? null, targetType: targetType ?? null }],
        queryFn: () => evaluationsApi.listRunGroupsPaginated({ pageNumber, pageSize, name: name || undefined, agent: agent || undefined, targetType }),
        refetchInterval: query => {
            if (viewMaySkipActiveRuns) {
                return 15_000;
            }
            const rows = query.state.data?.data;
            const anyActive = rows?.some(g => g.status === "pending" || g.status === "running");
            return anyActive ? 15_000 : 60_000;
        },
        staleTime: 5_000,
        refetchOnWindowFocus: false,
    });
}

export function useEvalReportAgents() {
    return useQuery<EvalReportTarget[], Error>({
        queryKey: [EVAL_REPORTS_AGENTS_KEY],
        queryFn: evaluationsApi.listRunGroupAgents,
        staleTime: 5 * 60_000,
        refetchOnWindowFocus: false,
    });
}
