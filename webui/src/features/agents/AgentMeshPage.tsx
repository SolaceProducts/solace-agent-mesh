// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, RefreshCcw } from "lucide-react";
import { useLocation, useSearchParams } from "react-router-dom";

import { useAgentCards } from "@/api/agent-cards";
import { agentKeys, useAgents } from "@/api/agents";
import { remoteAgentKeys, useRemoteAgents } from "@/api/remote-agents";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { LayoutType } from "@/types/a2ui";

import { DuplicateNameWarningBanner } from "@/features/agents/DuplicateNameWarningBanner";
import { useDuplicateDisplayName, useUndismissedDuplicate } from "@/features/agents/hooks";
import { EvalIntroDialog } from "@/features/evaluations/components";
import { useEvalIntroDialog } from "@/features/evaluations/hooks";

import { Header, MessageBanner, messageColourVariants, MorePopoverButton, PageLayout } from "@/common";

import { AgentMeshCards } from "./AgentMeshCards";
import { AgentMeshMorePopover } from "./AgentMeshMorePopover";
import { AgentTable } from "./AgentTable";
import { HierarchicalLayout } from "./hierarchicalLayout/HierarchicalLayout";
import { type AgentKind, kindToTab, tabToKind } from "./utils/filters";
import { WORKFLOW_LAYOUTS } from "./utils/layouts";

const LAYOUTS = {
    cards: LayoutType.CARDS,
    table: LayoutType.TABLE,
    hierarchical: LayoutType.HIERARCHICAL,
};
const LAYOUT_OPTIONS = Object.values(LAYOUTS);
const LAYOUT_STORAGE_KEY = "sam-layout-preference";

function getParamWithFallback<T extends string>(urlParam: string | null, validOptions: readonly T[], defaultValue: T, localStorageKey?: string): T {
    if (urlParam && validOptions.includes(urlParam as T)) {
        return urlParam as T;
    }

    if (localStorageKey) {
        const saved = localStorage.getItem(localStorageKey);
        if (saved && validOptions.includes(saved as T)) {
            return saved as T;
        }
    }

    return defaultValue;
}

export function AgentMeshPage() {
    const { navigate } = useChatContext();
    const { data: yamlAgentCards = [], isLoading: yamlAgentCardsLoading, error: yamlAgentCardsError, refetch: agentsRefetch } = useAgentCards();
    const yamlAgentCardsRefetch = agentsRefetch;
    const { refetch: remoteAgentsRefetch } = useRemoteAgents();
    const { refetch: userAgentsRefetch } = useAgents();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const [searchParams, setSearchParams] = useSearchParams();
    const location = useLocation();
    const queryClient = useQueryClient();
    const { evalIntroOpen, setEvalIntroOpen, tryOpenEvalIntro, evalIntroDismissed, setEvalIntroDismissed } = useEvalIntroDialog();

    const onAgentDeployed = useCallback(
        (agentName: string) => {
            void tryOpenEvalIntro(agentName);
        },
        [tryOpenEvalIntro]
    );

    useEffect(() => {
        if (location.state?.showEvalIntro) {
            void tryOpenEvalIntro(location.state.agentName);
            navigate({ pathname: location.pathname, search: location.search, hash: location.hash }, { replace: true, state: null });
        }
    }, [location.hash, location.pathname, location.search, location.state, navigate, tryOpenEvalIntro]);

    const userCanCreateAgents = userHasCapability(CAPABILITIES.createAgents);

    const { hasDuplicates } = useDuplicateDisplayName();
    const { undismissedDuplicates } = useUndismissedDuplicate();

    const layoutParam = searchParams.get("layout");
    const [selectedLayout, setSelectedLayout] = useState<string>(getParamWithFallback(layoutParam, LAYOUT_OPTIONS, LAYOUTS.cards, LAYOUT_STORAGE_KEY));

    useEffect(() => {
        const newLayout = getParamWithFallback(layoutParam, LAYOUT_OPTIONS, LAYOUTS.cards, LAYOUT_STORAGE_KEY);
        if (newLayout !== selectedLayout) {
            setSelectedLayout(newLayout);
        }
    }, [searchParams, layoutParam, selectedLayout]);

    const agentKind = tabToKind(searchParams.get("tab"));

    const handleLayoutChange = (layout: string) => {
        setSelectedLayout(layout);

        localStorage.setItem(LAYOUT_STORAGE_KEY, layout);

        const newParams = new URLSearchParams(searchParams);
        newParams.set("layout", layout);

        setSearchParams(newParams);
    };

    const handleTabChange = useCallback(
        (kind: AgentKind, { replace = false }: { replace?: boolean } = {}) => {
            const newParams = new URLSearchParams(searchParams);
            newParams.set("tab", kindToTab(kind));
            setSearchParams(newParams, { replace });
        },
        [searchParams, setSearchParams]
    );

    const handleDeepLinkKind = useCallback(
        (kind: AgentKind) => {
            if (kind !== agentKind) handleTabChange(kind, { replace: true });
        },
        [agentKind, handleTabChange]
    );

    const handleRefreshAgents = useCallback(() => {
        if (userHasCapability(CAPABILITIES.readAgents)) {
            userAgentsRefetch();
            remoteAgentsRefetch();
        }
        agentsRefetch();
        yamlAgentCardsRefetch();
        queryClient.invalidateQueries({ queryKey: agentKeys.lists() });
        queryClient.invalidateQueries({ queryKey: remoteAgentKeys.lists() });
    }, [userHasCapability, CAPABILITIES, userAgentsRefetch, remoteAgentsRefetch, yamlAgentCardsRefetch, agentsRefetch, queryClient]);

    const buttons = useMemo(() => {
        if (!userCanCreateAgents) {
            return [
                <AgentMeshMorePopover key="more-popover" onRefetchAgents={handleRefreshAgents}>
                    {MorePopoverButton}
                </AgentMeshMorePopover>,
            ];
        }
        return [
            <Button key="refresh-agents" variant="ghost" title="Refresh Agents" onClick={handleRefreshAgents}>
                <RefreshCcw />
                <span className="hidden sm:inline">Refresh</span>
            </Button>,
        ];
    }, [userCanCreateAgents, handleRefreshAgents]);

    const tabs = useMemo(
        () => [
            { id: kindToTab("agent"), label: "Agents", isActive: agentKind === "agent", onClick: () => handleTabChange("agent") },
            { id: kindToTab("workflow"), label: "Workflows", isActive: agentKind === "workflow", onClick: () => handleTabChange("workflow") },
        ],
        [agentKind, handleTabChange]
    );

    const header = useMemo(() => <Header title="Agent Mesh" tabs={tabs} buttons={buttons} />, [tabs, buttons]);

    if (yamlAgentCardsLoading) {
        return (
            <div className="space-y-6">
                <div className="flex h-96 items-center justify-center">
                    <Loader2 className="size-8 animate-spin text-(--brand-wMain)" />
                </div>
            </div>
        );
    }

    if (yamlAgentCardsError) {
        return (
            <div className="space-y-6">
                <div className="flex h-96 items-center justify-center">
                    <MessageBanner variant="error" message={`Error loading agents. ${yamlAgentCardsError.message}`} />
                </div>
            </div>
        );
    }

    const duplicateBanner = hasDuplicates && undismissedDuplicates.length > 0 ? <DuplicateNameWarningBanner duplicateGroups={undismissedDuplicates} dismissible className="mx-6 mt-4 mb-3" /> : null;

    const availableLayouts: readonly LayoutType[] = agentKind === "workflow" ? WORKFLOW_LAYOUTS : LAYOUT_OPTIONS;
    const availableLayoutNames: readonly string[] = availableLayouts;
    const effectiveLayout = availableLayoutNames.includes(selectedLayout) ? selectedLayout : LAYOUTS.cards;

    const renderLayoutContent = () => {
        switch (effectiveLayout) {
            case LayoutType.HIERARCHICAL:
                return (
                    <div className="flex h-full flex-col overflow-hidden bg-(--background-w20)">
                        <div className={cn("flex", "h-8", "shrink-0", "w-full", "items-center", "justify-center", messageColourVariants.info)}>Only running agents are displayed in the hierarchical view.</div>
                        {duplicateBanner}
                        <div className="flex min-h-0 flex-1">
                            <HierarchicalLayout agents={yamlAgentCards ?? []} currentLayout={effectiveLayout} onLayoutChange={handleLayoutChange} />
                        </div>
                    </div>
                );
            case LayoutType.TABLE:
                return <AgentTable selectedLayout={effectiveLayout} availableLayouts={availableLayouts} onLayoutChange={handleLayoutChange} duplicateBanner={duplicateBanner} onDeployed={onAgentDeployed} agentKind={agentKind} />;
            default:
                return (
                    <AgentMeshCards
                        selectedLayout={effectiveLayout}
                        availableLayouts={availableLayouts}
                        onLayoutChange={handleLayoutChange}
                        duplicateBanner={duplicateBanner}
                        onDeployed={onAgentDeployed}
                        agentKind={agentKind}
                        onDeepLinkKindResolved={handleDeepLinkKind}
                    />
                );
        }
    };

    return (
        <PageLayout>
            <div className="flex h-full flex-col">
                {header}
                <div className="relative min-h-0 flex-1">{renderLayoutContent()}</div>
            </div>
            <EvalIntroDialog open={evalIntroOpen} onOpenChange={setEvalIntroOpen} dismissed={evalIntroDismissed} onDismissedChange={setEvalIntroDismissed} />
        </PageLayout>
    );
}
