// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useChatArtifacts, useSessionArtifactContent } from "@/api/artifacts/hooks";

import { useChatContext } from "@/providers/contexts";

import { APPLY_COMPLETE_ARTIFACT_FILENAME, type ApplyComplete } from "../EditCompleteCard";
import { TEST_PLAN_ARTIFACT_FILENAME, type TestPlan } from "../TestPlanCard";
import { TEST_REPORT_ARTIFACT_FILENAME, type TestReport } from "../TestReportCard";

import { decodeBase64ContentSafe } from "./artifactUtils";

export function useTestPlan(): { plan: TestPlan | null; isLoading: boolean; runNumber: number | null } {
    const { plan, isLoading, runNumber } = useArtifactJSON<TestPlan>(TEST_PLAN_ARTIFACT_FILENAME);
    return { plan, isLoading, runNumber: runNumber === null ? null : runNumber + 1 };
}

export function useTestReport(): { report: TestReport | null; isLoading: boolean; runNumber: number | null } {
    const { plan, isLoading, runNumber } = useArtifactJSON<TestReport>(TEST_REPORT_ARTIFACT_FILENAME);
    return { report: plan, isLoading, runNumber: runNumber === null ? null : runNumber + 1 };
}

export function useApplyComplete(): { applyComplete: ApplyComplete | null; isLoading: boolean; runNumber: number | null } {
    const { plan, isLoading, runNumber } = useArtifactJSON<ApplyComplete>(APPLY_COMPLETE_ARTIFACT_FILENAME);
    return { applyComplete: plan, isLoading, runNumber };
}

function useArtifactJSON<T>(filename: string): { plan: T | null; isLoading: boolean; runNumber: number | null } {
    const { sessionId } = useChatContext() as unknown as { sessionId: string | null };
    const { data: allArtifacts } = useChatArtifacts();

    const latestVersion = useMemo(() => {
        if (!allArtifacts) return undefined;
        for (const a of allArtifacts) {
            if (a.filename !== filename) continue;
            return typeof a.version === "number" ? a.version : undefined;
        }
        return undefined;
    }, [allArtifacts, filename]);

    const query = useSessionArtifactContent(sessionId, latestVersion === undefined ? null : filename, latestVersion);

    const parsed = useMemo<T | null>(() => {
        if (!query.data) return null;
        const text = decodeBase64ContentSafe(query.data.content);
        if (text === null) return null;
        try {
            return JSON.parse(text) as T;
        } catch {
            return null;
        }
    }, [query.data]);

    return useMemo(() => ({ plan: parsed, isLoading: query.isPending && latestVersion !== undefined, runNumber: latestVersion ?? null }), [parsed, query.isPending, latestVersion]);
}
