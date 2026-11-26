// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { DOC_LINKS } from "@/utils";

export const MAX_EXAMPLES_PER_DATASET = 100;

export const EVAL_DEFAULT_RUN_AS = "system:eval";

export const MAX_EXAMPLE_ARTIFACT_BYTES = 10_000_000;

export const LAB_TABS = ["experiments", "datasets", "evaluators"] as const;
export type LabTab = (typeof LAB_TABS)[number];
export function isLabTab(value: string | null): value is LabTab {
    return value !== null && (LAB_TABS as readonly string[]).includes(value);
}

export const EVAL_ROUTES = {
    REPORTS: "/evaluations",
    LAB: "/evaluations/lab",
    LAB_EVALUATORS_TAB: "/evaluations/lab?tab=evaluators",
    LAB_DATASETS_TAB: "/evaluations/lab?tab=datasets",
    LAB_EXPERIMENTS_TAB: "/evaluations/lab?tab=experiments",
    EVALUATORS: "/evaluations/evaluators",
    EVALUATOR_NEW: "/evaluations/evaluators/new",
    EXPERIMENT_NEW: "/evaluations/experiments/new",
    evaluator: (id: string) => `/evaluations/evaluators/${id}`,
    experiment: (id: string) => `/evaluations/experiments/${id}`,
    experimentForEvaluator: (evaluatorId: string) => `/evaluations/experiments/new?evaluatorId=${evaluatorId}`,
    labEvaluatorsSelected: (id: string) => `/evaluations/lab?tab=evaluators&selected=${id}`,
    labDatasetsSelected: (id: string) => `/evaluations/lab?tab=datasets&selected=${id}`,
    runGroup: (groupId: string) => `/evaluations/runGroups/${groupId}`,
    runGroupExample: (groupId: string, exampleKey: string) => `/evaluations/runGroups/${groupId}/examples/${encodeURIComponent(exampleKey)}`,
} as const;

export const EVAL_SORT = {
    NAME_ASC: "name:asc",
    NAME_DESC: "name:desc",
    EXPERIMENT_USAGE_DESC: "experiment_count:desc",
} as const;

export type EvalSortValue = (typeof EVAL_SORT)[keyof typeof EVAL_SORT];

export const EVAL_DOCS = {
    DATASETS: DOC_LINKS.evalPerformanceDatasets,
    EVALUATORS: DOC_LINKS.evalPerformanceEvaluators,
    WRITING_CRITERIA: DOC_LINKS.evalPerformanceLlmJudge,
    CHOICE_SCORES: DOC_LINKS.evalPerformanceLlmJudge,
    IMPORTING_EXAMPLES: DOC_LINKS.evalPerformanceCsvImportExport,
} as const;

export const EXPECTED_ARTIFACT_ACCEPT = ".txt,.md,.markdown,.json,.yaml,.yml,.csv,.tsv,.xml,.log,.py,.go,.js,.jsx,.ts,.tsx,.rs,.java,.c,.cc,.cpp,.h,.hpp,.rb,.php,.sql,.toml,.ini,.cfg,.env,.dockerfile,.mmd,text/*";
