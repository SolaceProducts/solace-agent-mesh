// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { Activity, BookOpenText, Bot, CalendarDays, CircleUserRound, FlaskConical, FolderOpen, LayoutGrid, LogOut, Settings, Sparkles, Telescope } from "lucide-react";

import { type NavItemConfig } from "@/types/nav";

import { EVAL_ROUTES } from "@/features/evaluations/constants";
import { OBSERVABILITY_ROUTES } from "@/features/observability/routes";
import { BUDGETS_ROUTE } from "@/features/systemSettings/routes";

interface UseNavigationItemsProps {
    persistenceEnabled: boolean;
    builderEnabled: boolean;
    artifactsPageEnabled: boolean;
    schedulerEnabled: boolean;
    isAuthenticated: boolean;
    logoutEnabled: boolean;
    onUserAccountClick: () => void;
    onLogoutClick: () => void;
    userCanReadAgents: boolean;
    userCanReadConnectors: boolean;
    userCanReadToolsets: boolean;
    userCanReadSkills: boolean;
    userCanReadWorkflows: boolean;
    userCanReadGateways: boolean;
    userCanReadModelConfig: boolean;
    userCanReadEvaluations: boolean;
    canSeeUserManagement: boolean;
    canSeeBudgets: boolean;
    canSeeFeedback: boolean;
    canSeeUsage: boolean;
}

const activitiesNavItem: NavItemConfig = {
    id: "activities",
    label: "Activities",
    route: "/activities",
    routeMatch: "/activities",
};

const observabilityNavItems = (canSeeFeedback: boolean, canSeeUsage: boolean): NavItemConfig[] => {
    const otherPages: NavItemConfig[] = [];
    if (canSeeFeedback) otherPages.push({ id: "feedback", label: "Feedback", route: OBSERVABILITY_ROUTES.feedback, routeMatch: OBSERVABILITY_ROUTES.feedback });
    if (canSeeUsage) otherPages.push({ id: "usage", label: "Usage", route: OBSERVABILITY_ROUTES.usage, routeMatch: OBSERVABILITY_ROUTES.usage });
    if (otherPages.length === 0) {
        return [{ ...activitiesNavItem, icon: Activity, position: "top" }];
    }
    return [
        {
            id: "observability",
            label: "Signals & Usage",
            icon: Telescope,
            position: "top",
            children: [activitiesNavItem, ...otherPages],
        },
    ];
};

function buildSystemSettingsChildren({ canSeeUserManagement, canSeeBudgets, userCanReadModelConfig }: Readonly<{ canSeeUserManagement: boolean; canSeeBudgets: boolean; userCanReadModelConfig: boolean }>): NavItemConfig[] {
    const children: NavItemConfig[] = [];

    if (canSeeUserManagement) {
        children.push({
            id: "user-management",
            label: "User Management",
            route: "/user-management",
            routeMatch: "/user-management",
        });
    }

    if (canSeeBudgets) {
        children.push({
            id: "budgets",
            label: "Budgets",
            route: BUDGETS_ROUTE,
            routeMatch: BUDGETS_ROUTE,
        });
    }

    if (userCanReadModelConfig) {
        children.push({
            id: "models",
            label: "Models",
            route: "/models",
            routeMatch: "/models",
        });
    }

    return children;
}

export function useNavigationItems({
    persistenceEnabled,
    builderEnabled,
    artifactsPageEnabled,
    schedulerEnabled,
    isAuthenticated,
    logoutEnabled,
    onUserAccountClick,
    onLogoutClick,
    userCanReadAgents,
    userCanReadConnectors,
    userCanReadToolsets,
    userCanReadSkills,
    userCanReadWorkflows,
    userCanReadGateways,
    userCanReadModelConfig,
    userCanReadEvaluations,
    canSeeUserManagement,
    canSeeBudgets,
    canSeeFeedback,
    canSeeUsage,
}: UseNavigationItemsProps): { items: NavItemConfig[] } {
    const items: NavItemConfig[] = useMemo(() => {
        const assetsChildren: NavItemConfig[] = [
            {
                id: "prompts",
                label: "Prompts",
                route: "/prompts",
                routeMatch: "/prompts",
            },
        ];

        if (artifactsPageEnabled) {
            assetsChildren.push({
                id: "artifacts",
                label: "Artifacts",
                route: "/artifacts",
                routeMatch: "/artifacts",
            });
        }

        const systemManagementChildren: NavItemConfig[] = [];

        if (builderEnabled) {
            systemManagementChildren.push({
                id: "build",
                label: "Quick Build",
                badge: <Sparkles className="ml-1.5 h-3.5 w-3.5 text-current" />,
                route: "/builder",
                routeMatch: "/builder",
            });
        }

        if (userCanReadAgents) {
            systemManagementChildren.push({
                id: "agents",
                label: "Agents",
                route: "/agents",
                routeMatch: "/agents",
            });
        }

        if (userCanReadWorkflows) {
            systemManagementChildren.push({
                id: "workflows",
                label: "Workflows",
                route: "/workflows",
                routeMatch: "/workflows",
                tooltip: "Early Access Feature",
            });
        }

        if (userCanReadConnectors) {
            systemManagementChildren.push({
                id: "connectors-mgmt",
                label: "Connectors",
                route: "/connectors",
                routeMatch: "/connectors",
            });
        }

        if (userCanReadGateways) {
            systemManagementChildren.push({
                id: "gateways-mgmt",
                label: "Entrypoints",
                route: "/entrypoints",
                routeMatch: "/entrypoints",
            });
        }

        if (userCanReadToolsets) {
            systemManagementChildren.push({
                id: "toolsets",
                label: "Toolsets",
                route: "/toolsets",
                routeMatch: "/toolsets",
            });
        }

        if (userCanReadSkills) {
            systemManagementChildren.push({
                id: "skills",
                label: "Skills",
                route: "/skills",
                routeMatch: "/skills",
            });
        }

        const systemSettingsChildren = buildSystemSettingsChildren({ canSeeUserManagement, canSeeBudgets, userCanReadModelConfig });

        return [
            {
                id: "agentMesh",
                label: "Agent Mesh",
                icon: Bot,
                route: "/agent-mesh",
                routeMatch: "/agent-mesh",
                position: "top",
            },
            {
                id: "projects",
                label: "Projects",
                icon: FolderOpen,
                route: "/projects",
                routeMatch: "/projects",
                position: "top",
                hidden: !persistenceEnabled,
            },
            {
                id: "schedules",
                label: "Schedules",
                icon: CalendarDays,
                route: "/scheduled-tasks",
                routeMatch: "/scheduled-tasks",
                tooltip: "Experimental Feature",
                position: "top",
                hidden: !schedulerEnabled,
            },
            {
                id: "assets",
                label: "Assets",
                icon: BookOpenText,
                children: assetsChildren,
                position: "top",
            },

            ...observabilityNavItems(canSeeFeedback, canSeeUsage),
            {
                id: "evaluations",
                label: "Evaluations",
                icon: FlaskConical,
                routeMatch: "/evaluations",
                position: "top",
                hidden: !userCanReadEvaluations,
                children: [
                    {
                        id: "eval-reports",
                        label: "Reports",
                        route: "/evaluations",
                        routeMatch: "/evaluations",
                    },
                    {
                        id: "eval-lab",
                        label: "Experiment Lab",
                        route: EVAL_ROUTES.LAB,
                        routeMatch: EVAL_ROUTES.LAB,
                    },
                ],
            },
            ...(systemManagementChildren.length > 0
                ? [
                      {
                          id: "systemManagement",
                          label: "Builder",
                          icon: LayoutGrid,
                          children: systemManagementChildren,
                          position: "top",
                      } satisfies NavItemConfig,
                  ]
                : []),
            ...(systemSettingsChildren.length > 0
                ? [
                      {
                          id: "systemSettings",
                          label: "System Settings",
                          icon: Settings,
                          children: systemSettingsChildren,
                          position: "top",
                      } satisfies NavItemConfig,
                  ]
                : []),
            {
                id: "userAccount",
                label: "User Account",
                icon: CircleUserRound,
                onClick: onUserAccountClick,
                position: "bottom",
            },
            {
                id: "logout",
                label: "Log Out",
                icon: LogOut,
                onClick: onLogoutClick,
                position: "bottom",
                hidden: !(logoutEnabled && isAuthenticated),
            },
        ];
    }, [
        persistenceEnabled,
        builderEnabled,
        artifactsPageEnabled,
        schedulerEnabled,
        isAuthenticated,
        logoutEnabled,
        onUserAccountClick,
        onLogoutClick,
        userCanReadAgents,
        userCanReadConnectors,
        userCanReadToolsets,
        userCanReadSkills,
        userCanReadWorkflows,
        userCanReadGateways,
        userCanReadModelConfig,
        userCanReadEvaluations,
        canSeeUserManagement,
        canSeeBudgets,
        canSeeFeedback,
        canSeeUsage,
    ]);

    return { items };
}
