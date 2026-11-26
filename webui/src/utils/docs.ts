// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const DOC_BASE_URL = "https://docs.solace.com/";
const DOC_PATHS = {
    buildingTools: "building/tools.htm",
    buildingToolsets: "building/toolsets.htm",
    buildingWorkflows: "building/workflows/index.htm",
    buildingSkills: "building/skills.htm",
    configureLlmProvider: "installing/configure.htm#llm-provider",
    managingProjects: "using-agent-mesh/managing-projects.htm",
    buildingConnectors: "building/connectors/index.htm",
    eventMeshConnector: "building/connectors/event-mesh/index.htm",
    buildingEntrypointsGoogleChat: "building/entrypoints/index.htm",
    buildingEntrypointsGoogleChatPrerequisites: "building/entrypoints/index.htm",
    buildingEntrypointsTeams: "building/entrypoints/teams/index.htm",
    buildingEntrypointsWhatsapp: "building/entrypoints/whatsapp/index.htm",
    buildingEntrypointsWhatsappPrerequisites: "building/entrypoints/whatsapp/index.htm#prerequisites",
    evalPerformanceDatasets: "evaluating-agent-performance/datasets.htm",
    evalPerformanceEvaluators: "evaluating-agent-performance/evaluators.htm",
    evalPerformanceLlmJudge: "evaluating-agent-performance/evaluators.htm#llm-as-a-judge-evaluators",
    evalPerformanceCsvImportExport: "evaluating-agent-performance/datasets.htm#csv-import-export-format",
    userManagementRoles: "building/user-management/index.htm#managing-roles",
    userManagementUsers: "building/user-management/index.htm#managing-users",
    userManagementClaimMappings: "building/user-management/index.htm#managing-claim-mappings",
    budgets: "administering/budgets/index.htm",
    costAndTokenUsage: "administering/budgets/index.htm#reviewing-cost-and-token-usage",
    smartContextSavings: "concepts/smart-context-management.htm",
} as const;

const docsUrl = (path: string): string => `${DOC_BASE_URL}Agent-Mesh/Framework/${path}`;

export const DOC_LINKS = Object.fromEntries(Object.entries(DOC_PATHS).map(([key, path]) => [key, docsUrl(path)])) as Record<keyof typeof DOC_PATHS, string>;

export const EXTERNAL_DOC_LINKS = {
    topicWildcards: DOC_BASE_URL + "Messaging/Wildcard-Charaters-Topic-Subs.htm",
    releaseStages: DOC_BASE_URL + "Cloud/stages_concept.htm",
} as const;
