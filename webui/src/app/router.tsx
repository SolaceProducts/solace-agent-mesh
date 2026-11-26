// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useWhenProviderReady } from "@openfeature/react-sdk";
import { createHashRouter, Navigate, redirect } from "react-router-dom";
import { type Params, type RouteObject } from "react-router-dom";

import { evaluationsApi } from "@/api/evaluations";

import { useUserContext } from "@/providers/contexts";

import { ActivitiesPage } from "@/features/activities";
import { AgentConnectExternalPage, AgentEditPage, AgentManagementPage, AgentMeshPage, AgentViewPage, RemoteAgentViewPage } from "@/features/agents";
import { BuilderAccessDenied } from "@/features/builder/BuilderAccessDenied";
import { BuilderPage } from "@/features/builder/BuilderPage";
import { ArtifactsPage, ChatPage, RecentChatsPage } from "@/features/chat";
import { TextSelectionProvider } from "@/features/chat/selection/TextSelectionProvider";
import { APIToolDetailPage, ConnectorEditPage, ConnectorsPage, ConnectorViewPage, EventMeshToolDetailPage, MCPToolDetailPage } from "@/features/connectors";
import { GatewayEditPage, GatewaysPage, GatewayViewPage } from "@/features/entrypoints";
import { EVAL_ROUTES } from "@/features/evaluations/constants";
import { EvalCreateEvaluatorPage } from "@/features/evaluations/EvalCreateEvaluatorPage";
import { EvalCreateExperimentPage } from "@/features/evaluations/EvalCreateExperimentPage";
import { EvalDatasetDetailPage } from "@/features/evaluations/EvalDatasetDetailPage";
import { EvalExampleDetailPage } from "@/features/evaluations/EvalExampleDetailPage";
import { EvalExperimentDetailPage } from "@/features/evaluations/EvalExperimentDetailPage";
import { EvalExperimentLabPage } from "@/features/evaluations/EvalExperimentLabPage";
import { EvalExperimentReportPage } from "@/features/evaluations/EvalExperimentReportPage";
import { EvalOverviewPage } from "@/features/evaluations/EvalOverviewPage";
import { ModelDetailsPage, ModelEditPage, ModelsPage } from "@/features/models";
import { NotificationChannelConfirmPage, NotificationsPage } from "@/features/notifications";
import { BudgetUsagePageContainer, FeedbackDashboardPage, FeedbackGate, FeedbackHistoryPage, FeedbackLayout, UsageGate, UsagePageContainer } from "@/features/observability";
import { ProjectsPage } from "@/features/projects";
import { PromptsPage } from "@/features/prompts";
import { ScheduledTasksPage } from "@/features/scheduledTasks/ScheduledTasksPage";
import { SharedChatViewPage } from "@/features/share/SharedChatViewPage";
import { SharedSessionPage } from "@/features/share/SharedSessionPage";
import { SkillsPage } from "@/features/skills/SkillsPage";
import { SkillUploadPage } from "@/features/skills/SkillUploadPage";
import { SkillViewPage } from "@/features/skills/SkillViewPage";
import { BudgetsGate, BudgetsPageContainer } from "@/features/systemSettings";
import { ToolsetEditPage, ToolsetsPage, ToolsetViewPage } from "@/features/toolsets";
import { ClaimMappingPage, RolesPage, USER_MANAGEMENT_ROUTES, UserManagementLayout, UsersPage } from "@/features/userManagement";
import { WorkflowEditPage, WorkflowsPage, WorkflowVisualizationPageWrapper as WorkflowVisualizationPage } from "@/features/workflows";

import { useBuilderEnabled, useIsPromptVersionHistoryEnabled, useIsSchedulerEnabled, useIsUserBudgetsSupported, useIsUserManagementEnabled } from "@/hooks";

import AppLayout from "./AppLayout";
import RouteErrorBoundary from "./RouteErrorBoundary";

export const newAgentLoader = () => ({ isNew: true });
export const editAgentLoader = ({ params }: { params: Params }) => ({ agentId: params.id, isNew: false });
export const editExternalAgentLoader = ({ params }: { params: Params }) => ({ remoteAgentId: params.id, isEdit: true });

export const useCreateRouter = () => {
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const builderEnabled = useBuilderEnabled();
    const schedulerEnabled = useIsSchedulerEnabled();
    const canSeeUserManagement = useIsUserManagementEnabled();
    const userBudgetsSupported = useIsUserBudgetsSupported();
    const canWriteModelConfig = userHasCapability(CAPABILITIES.createModelConfig) || userHasCapability(CAPABILITIES.updateModelConfig);
    const canWriteConnectors = userHasCapability(CAPABILITIES.createConnectors) || userHasCapability(CAPABILITIES.updateConnectors);
    const versionHistoryEnabled = useIsPromptVersionHistoryEnabled();
    const flagsReady = useWhenProviderReady({ suspendUntilReady: false });
    const remoteToolFallback = flagsReady ? <Navigate to="/agent-mesh" replace /> : null;
    const promptVersionsFallback = flagsReady ? <Navigate to="/prompts" replace /> : null;

    const chatRedirect: RouteObject[] = [{ index: true, element: <Navigate to="/chat" replace /> }];
    const gatedChildren = (allowed: boolean, children: RouteObject[], fallback: RouteObject[] = chatRedirect) => (allowed ? children : fallback);

    return createHashRouter([
        {
            path: "/share/:shareId",
            element: (
                <TextSelectionProvider>
                    <SharedSessionPage />
                </TextSelectionProvider>
            ),
            errorElement: <RouteErrorBoundary />,
        },
        {
            path: "/",
            element: <AppLayout />,
            children: [
                {
                    errorElement: <RouteErrorBoundary />,
                    children: [
                        {
                            index: true,
                            element: <Navigate to="/chat" replace />,
                        },
                        {
                            path: "chat",
                            element: <ChatPage />,
                        },
                        {
                            path: "agent-mode/chat",
                            element: <ChatPage />,
                        },
                        {
                            path: "agent-mode/recent-chats",
                            element: <RecentChatsPage />,
                        },
                        {
                            path: "shared-chat/:shareId",
                            element: <SharedChatViewPage />,
                        },
                        {
                            path: "recent-chats",
                            element: <RecentChatsPage />,
                        },
                        ...(builderEnabled
                            ? [
                                  {
                                      path: "builder",
                                      element: <BuilderPage />,
                                  },
                              ]
                            : [
                                  {
                                      path: "builder",
                                      element: <BuilderAccessDenied />,
                                  },
                              ]),
                        {
                            path: "projects",
                            children: [
                                {
                                    index: true,
                                    element: <ProjectsPage />,
                                },
                                {
                                    path: ":id",
                                    element: <ProjectsPage />,
                                    loader: ({ params }) => {
                                        return { projectId: params.id };
                                    },
                                },
                            ],
                        },
                        {
                            path: "artifacts",
                            element: <ArtifactsPage />,
                        },
                        {
                            path: "notifications",
                            element: <NotificationsPage />,
                        },
                        {
                            path: "notifications/confirm",
                            element: <NotificationChannelConfirmPage />,
                        },
                        {
                            path: "prompts",
                            children: [
                                {
                                    index: true,
                                    element: <PromptsPage />,
                                },
                                {
                                    path: "new",
                                    element: <PromptsPage />,
                                    loader: ({ request }) => {
                                        const url = new URL(request.url);
                                        const mode = url.searchParams.get("mode") || "manual";
                                        return { view: "builder", mode };
                                    },
                                },
                                {
                                    path: ":id/edit",
                                    element: <PromptsPage />,
                                    loader: ({ params }) => {
                                        return { promptId: params.id, view: "builder", mode: "edit" };
                                    },
                                },
                                {
                                    path: ":id/versions",
                                    element: versionHistoryEnabled ? <PromptsPage /> : promptVersionsFallback,
                                    loader: ({ params }) => {
                                        return { promptId: params.id, view: "versions" };
                                    },
                                },
                            ],
                        },
                        {
                            path: "scheduled-tasks",
                            children: gatedChildren(schedulerEnabled, [
                                {
                                    index: true,
                                    element: <ScheduledTasksPage />,
                                },
                            ]),
                        },
                        {
                            path: "agent-mesh",
                            children: userHasCapability(CAPABILITIES.readAgents)
                                ? [
                                      {
                                          index: true,
                                          element: <AgentMeshPage />,
                                      },
                                      {
                                          path: "workflows/:workflowName",
                                          element: <WorkflowVisualizationPage />,
                                      },
                                      {
                                          path: "external/:id",
                                          element: <RemoteAgentViewPage />,
                                          loader: ({ params }: { params: Params }) => {
                                              return { remoteAgentId: params.id };
                                          },
                                      },
                                      {
                                          path: ":id",
                                          element: <AgentViewPage />,
                                          loader: ({ params }) => {
                                              return { agentId: params.id };
                                          },
                                      },
                                  ]
                                : [
                                      {
                                          index: true,
                                          element: <AgentMeshPage />,
                                      },
                                      {
                                          path: "workflows/:workflowName",
                                          element: <WorkflowVisualizationPage />,
                                      },
                                  ],
                        },
                        {
                            path: "workflows",
                            children: gatedChildren(userHasCapability(CAPABILITIES.readWorkflows), [
                                {
                                    index: true,
                                    element: <WorkflowsPage />,
                                },
                                {
                                    path: ":id/edit",
                                    element: <WorkflowEditPage />,
                                },
                            ]),
                        },
                        {
                            path: "agents",
                            children: gatedChildren(userHasCapability(CAPABILITIES.readAgents), [
                                {
                                    index: true,
                                    element: <AgentManagementPage />,
                                },
                                {
                                    path: "external/new",
                                    element: <AgentConnectExternalPage />,
                                },
                                {
                                    path: "external/:id/edit",
                                    element: <AgentConnectExternalPage />,
                                    loader: editExternalAgentLoader,
                                },
                                {
                                    path: "new/edit",
                                    element: <AgentEditPage />,
                                    loader: newAgentLoader,
                                },
                                {
                                    path: ":id/edit",
                                    element: <AgentEditPage />,
                                    loader: editAgentLoader,
                                },
                            ]),
                        },
                        {
                            path: "connectors",
                            children: gatedChildren(
                                userHasCapability(CAPABILITIES.readConnectors),
                                [
                                    {
                                        index: true,
                                        element: <ConnectorsPage />,
                                    },
                                    ...(canWriteConnectors
                                        ? [
                                              {
                                                  path: "new/edit",
                                                  element: <ConnectorEditPage />,
                                                  loader: ({ request }: { request: Request }) => {
                                                      const url = new URL(request.url);
                                                      const type = url.searchParams.get("type");
                                                      const subtype = url.searchParams.get("subtype");
                                                      return { isNew: true, type, subtype };
                                                  },
                                              },
                                              {
                                                  path: ":id/edit",
                                                  element: <ConnectorEditPage />,
                                                  loader: ({ params, request }: { params: Params; request: Request }) => {
                                                      const url = new URL(request.url);
                                                      const type = url.searchParams.get("type");
                                                      const subtype = url.searchParams.get("subtype");
                                                      return { connectorId: params.id, isNew: false, type, subtype };
                                                  },
                                              },
                                          ]
                                        : []),
                                    {
                                        path: ":id/tools/mcp/:toolName",
                                        element: <MCPToolDetailPage />,
                                        loader: ({ params }) => {
                                            return { connectorId: params.id, toolName: params.toolName };
                                        },
                                    },
                                    {
                                        path: ":id/tools/api/:operationId",
                                        element: <APIToolDetailPage />,
                                        loader: ({ params }) => {
                                            return { connectorId: params.id, operationId: params.operationId };
                                        },
                                    },
                                    {
                                        path: ":id/tools/event_mesh/:toolName",
                                        element: <EventMeshToolDetailPage />,
                                        loader: ({ params }) => {
                                            return { connectorId: params.id, toolName: params.toolName };
                                        },
                                    },
                                    {
                                        path: ":id",
                                        element: <ConnectorViewPage />,
                                        loader: ({ params }) => {
                                            return { connectorId: params.id };
                                        },
                                    },
                                ],
                                [
                                    {
                                        index: true,
                                        element: <Navigate to="/agent-mesh" replace />,
                                    },
                                ]
                            ),
                        },
                        {
                            path: "entrypoints",
                            children: gatedChildren(userHasCapability(CAPABILITIES.readGateways), [
                                {
                                    index: true,
                                    element: <GatewaysPage />,
                                },
                                {
                                    path: "new/edit",
                                    element: <GatewayEditPage />,
                                    loader: ({ params, request }) => {
                                        const url = new URL(request.url);
                                        const type = url.searchParams.get("type");
                                        return { gatewayId: params.id, isNew: true, type };
                                    },
                                },
                                {
                                    path: ":id/edit",
                                    element: <GatewayEditPage />,
                                    loader: ({ params }) => {
                                        return { gatewayId: params.id, isNew: false };
                                    },
                                },
                                {
                                    path: ":id",
                                    element: <GatewayViewPage />,
                                    loader: ({ params }) => {
                                        return { gatewayId: params.id };
                                    },
                                },
                            ]),
                        },
                        {
                            path: "evaluations",
                            children: gatedChildren(userHasCapability(CAPABILITIES.readEvaluations), [
                                {
                                    index: true,
                                    element: <EvalOverviewPage />,
                                },
                                {
                                    path: "lab",
                                    element: <EvalExperimentLabPage />,
                                },
                                {
                                    path: "datasets",
                                    loader: () => redirect(EVAL_ROUTES.LAB_DATASETS_TAB),
                                },
                                {
                                    path: "datasets/:datasetId",
                                    element: <EvalDatasetDetailPage />,
                                    loader: ({ params }: { params: Params }) => {
                                        return { datasetId: params.datasetId };
                                    },
                                },
                                {
                                    path: "experiments",
                                    loader: () => redirect(EVAL_ROUTES.LAB_EXPERIMENTS_TAB),
                                },
                                {
                                    path: "experiments/new",
                                    element: <EvalCreateExperimentPage />,
                                },
                                {
                                    path: "experiments/:experimentId",
                                    element: <EvalExperimentDetailPage />,
                                    loader: ({ params }: { params: Params }) => {
                                        return { experimentId: params.experimentId };
                                    },
                                },
                                {
                                    path: "experiments/:experimentId/runs",
                                    loader: ({ params }: { params: Params }) => {
                                        return redirect(`/evaluations/experiments/${params.experimentId}?tab=runs`);
                                    },
                                },
                                {
                                    path: "evaluators",
                                    loader: () => redirect(EVAL_ROUTES.LAB_EVALUATORS_TAB),
                                },
                                {
                                    path: "evaluators/new",
                                    element: <EvalCreateEvaluatorPage />,
                                },
                                {
                                    path: "evaluators/:evaluatorId",
                                    element: <EvalCreateEvaluatorPage />,
                                    loader: ({ params }: { params: Params }) => {
                                        return { evaluatorId: params.evaluatorId };
                                    },
                                },
                                {
                                    path: "runs/:runId",
                                    loader: async ({ params }: { params: Params }) => {
                                        if (!params.runId) return redirect(EVAL_ROUTES.LAB_EXPERIMENTS_TAB);
                                        try {
                                            const run = await evaluationsApi.getRun(params.runId);
                                            return redirect(EVAL_ROUTES.runGroup(run.runGroupId));
                                        } catch {
                                            return redirect(EVAL_ROUTES.LAB_EXPERIMENTS_TAB);
                                        }
                                    },
                                },
                                {
                                    path: "runGroups/:groupId",
                                    element: <EvalExperimentReportPage />,
                                    loader: ({ params }: { params: Params }) => {
                                        return { groupId: params.groupId };
                                    },
                                },
                                {
                                    path: "runGroups/:groupId/examples/:exampleKey",
                                    element: <EvalExampleDetailPage />,
                                    loader: ({ params }: { params: Params }) => {
                                        return {
                                            groupId: params.groupId,
                                            exampleKey: params.exampleKey ? decodeURIComponent(params.exampleKey) : undefined,
                                        };
                                    },
                                },
                            ]),
                        },
                        {
                            path: "models",
                            children: gatedChildren(userHasCapability(CAPABILITIES.readModelConfig), [
                                {
                                    index: true,
                                    element: <ModelsPage />,
                                },
                                {
                                    path: ":id",
                                    element: <ModelDetailsPage />,
                                },
                                ...(canWriteModelConfig
                                    ? [
                                          {
                                              path: "new/edit",
                                              element: <ModelEditPage />,
                                          },
                                          {
                                              path: ":id/edit",
                                              element: <ModelEditPage />,
                                          },
                                      ]
                                    : []),
                            ]),
                        },
                        {
                            path: "toolsets",
                            children: gatedChildren(
                                userHasCapability(CAPABILITIES.readToolsets),
                                [
                                    {
                                        index: true,
                                        element: <ToolsetsPage />,
                                    },
                                    {
                                        path: "new/edit",
                                        element: <ToolsetEditPage />,
                                        loader: () => {
                                            return { isNew: true };
                                        },
                                    },
                                    {
                                        path: ":id/edit",
                                        element: <ToolsetEditPage />,
                                        loader: ({ params }: { params: Params }) => {
                                            return { isNew: false, toolsetId: params.id };
                                        },
                                    },
                                    {
                                        path: ":id",
                                        element: <ToolsetViewPage />,
                                        loader: ({ params }: { params: Params }) => {
                                            return { toolsetId: params.id };
                                        },
                                    },
                                ],
                                [
                                    {
                                        index: true,
                                        element: remoteToolFallback,
                                    },
                                    {
                                        path: "*",
                                        element: remoteToolFallback,
                                    },
                                ]
                            ),
                        },
                        {
                            path: "skills",
                            children: gatedChildren(
                                userHasCapability(CAPABILITIES.readSkills),
                                [
                                    {
                                        index: true,
                                        element: <SkillsPage />,
                                    },
                                    {
                                        path: "upload",
                                        element: <SkillUploadPage />,
                                    },
                                    {
                                        path: ":id/upload",
                                        element: <SkillUploadPage />,
                                        loader: ({ params }: { params: Params }) => {
                                            return { skillId: params.id };
                                        },
                                    },
                                    {
                                        path: ":id",
                                        element: <SkillViewPage />,
                                        loader: ({ params }: { params: Params }) => {
                                            return { skillId: params.id };
                                        },
                                    },
                                ],
                                [
                                    {
                                        index: true,
                                        element: remoteToolFallback,
                                    },
                                    {
                                        path: "*",
                                        element: remoteToolFallback,
                                    },
                                ]
                            ),
                        },
                        {
                            path: "activities",
                            element: <ActivitiesPage />,
                        },
                        {
                            path: "feedback",
                            element: <FeedbackGate />,
                            children: [
                                {
                                    element: <FeedbackLayout />,
                                    children: [
                                        {
                                            index: true,
                                            element: <FeedbackDashboardPage />,
                                        },
                                        {
                                            path: "history",
                                            element: <FeedbackHistoryPage />,
                                        },
                                    ],
                                },
                            ],
                        },
                        {
                            path: "usage",
                            element: <UsageGate />,
                            children: [{ index: true, element: <UsagePageContainer /> }],
                        },
                        {
                            path: "user-management",
                            children: gatedChildren(canSeeUserManagement, [
                                {
                                    element: <UserManagementLayout />,
                                    children: [
                                        {
                                            index: true,
                                            loader: () => redirect(USER_MANAGEMENT_ROUTES.claimMapping),
                                        },
                                        {
                                            path: "users",
                                            element: <UsersPage />,
                                        },
                                        {
                                            path: "claim-mapping",
                                            element: <ClaimMappingPage />,
                                        },
                                        {
                                            path: "roles",
                                            element: <RolesPage />,
                                        },
                                    ],
                                },
                            ]),
                        },
                        {
                            path: "budgets",
                            element: <BudgetsGate />,
                            children: [
                                {
                                    index: true,
                                    element: <BudgetsPageContainer />,
                                },
                                userBudgetsSupported
                                    ? {
                                          path: ":budgetId/usage",
                                          element: <UsageGate />,
                                          children: [{ index: true, element: <BudgetUsagePageContainer /> }],
                                      }
                                    : { path: ":budgetId/usage", element: <Navigate to="/budgets" replace /> },
                            ],
                        },
                        {
                            path: "*",
                            element: <Navigate to="/chat" replace />,
                        },
                    ],
                },
            ],
        },
    ]);
};
