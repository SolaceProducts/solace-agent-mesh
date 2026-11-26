// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";
import { Outlet, useLocation } from "react-router-dom";

import { useModelConfigStatus } from "@/api/models/hooks";

import { useAuthContext, useChatContext, useConfigContext, useUIModeContext, useUserContext } from "@/providers/contexts";

import { useDesktopFileDialogErrors } from "@/hooks/useDesktopFileDialogErrors";
import { useIsArtifactsPageEnabled } from "@/hooks/useIsArtifactsPageEnabled";
import { useIsNotificationsEnabled } from "@/hooks/useIsNotificationsEnabled";
import { useIsSchedulerEnabled } from "@/hooks/useIsSchedulerEnabled";
import { useLocalStorage } from "@/hooks/useLocalStorage";
import { useIsMobile } from "@/hooks/useMobile";
import { useMoveSession } from "@/hooks/useMoveSession";
import { useNotificationSSE } from "@/hooks/useNotificationSSE";
import { useShareSession } from "@/hooks/useShareSession";

import { DesktopExperimentalBanner } from "@/common/banners/DesktopExperimentalBanner";
import { ModelWarningBanner } from "@/common/banners/ModelWarningBanner";
import { ModelSetupDialog } from "@/common/dialogs/ModelSetupDialog";
import { ProductAnalyticsPageViews } from "@/common/product-analytics";
import { ToastContainer } from "@/common/toast";

import { clearSignInCompleted, signInJustCompleted } from "@/utils/url";

import { Button } from "@/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/ui/sheet";

import { FreeCreditsNavItem, FreeModelSignInExpiredDialog, useModelSetupRequest, WelcomeSetupPage } from "@/features/activation";
import { BuilderTabContent } from "@/features/builder/BuilderTabContent";
import { ChatSessionDeleteDialog, MoveSessionDialog } from "@/features/chat";
import { SelectionContextMenu, useTextSelection } from "@/features/chat/selection";
import { CollapsibleNavigationSidebar, UIModeGuard } from "@/features/navigation";
import { NotificationsBell, type NotificationsOutletContext } from "@/features/notifications";
import { SettingsDialog, type SettingsSection } from "@/features/settings";
import { ShareChatDialog } from "@/features/share/ShareChatDialog";

import { useBudgetsEnabled, useBuilderEnabled, useFeedbackManagementEnabled, useIsActivationGateEnabled, useIsUserManagementEnabled, useNavigationItems, usePageTitle, useRadixPointerEventsFix, useUsageDashboardEnabled } from "@/hooks";
import { BuilderProvider, ChatProvider } from "@/providers";

import { activeNavItemId } from "./activeNavItem";
import { AlreadySignedInHandoff, SignedInHandoff } from "./SignedInHandoff";
import { pendingSignInSource, rememberSignInSource, settleSignInSource } from "./signInSource";

function AppLayoutContent() {
    const location = useLocation();
    const { hasModelConfigWrite, sessionToDelete, closeSessionDeleteModal, confirmSessionDelete } = useChatContext();
    const { persistenceEnabled } = useConfigContext();
    const { isAuthenticated, login, logout, useAuthorization } = useAuthContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { showNavigationSidebar } = useUIModeContext();

    const { isMenuOpen, menuPosition, selectedText, clearSelection, sourceTaskId } = useTextSelection();
    const isMobile = useIsMobile();
    const [isMobileNavOpen, setIsMobileNavOpen] = useState(false);
    const [isSettingsDialogOpen, setIsSettingsDialogOpen] = useState(false);
    const [settingsSection, setSettingsSection] = useState<SettingsSection>("general");
    const [isModelSetupDialogOpen, setIsModelSetupDialogOpen] = useState(false);
    const { isMoveDialogOpen, sessionToMove, handleMoveConfirm, closeMoveDialog } = useMoveSession();
    const { isShareDialogOpen, sessionToShare, setShareDialogOpen } = useShareSession();
    const [modelSetupDismissed, setModelSetupDismissed] = useLocalStorage("model-setup-dialog-dismissed", false);
    const { data: modelConfigStatus } = useModelConfigStatus();
    const freeDesktop = useIsActivationGateEnabled();
    const { requested: modelSetupRequested, clear: clearModelSetupRequest, appShown } = useModelSetupRequest();

    useEffect(() => appShown(), [appShown]);

    useEffect(() => {
        if (!freeDesktop && modelConfigStatus && !modelConfigStatus.configured && !modelSetupDismissed) {
            setIsModelSetupDialogOpen(true);
        }
    }, [freeDesktop, modelConfigStatus, modelSetupDismissed]);

    const handleModelSetupDialogChange = useCallback(
        (open: boolean) => {
            setIsModelSetupDialogOpen(open);
            if (!open) {
                setModelSetupDismissed(true);
            }
        },
        [setModelSetupDismissed]
    );

    const handleUserAccountClick = useCallback(() => {
        setSettingsSection("general");
        setIsSettingsDialogOpen(true);
    }, []);
    const openNotificationSettings = useCallback(() => {
        setSettingsSection("notifications");
        setIsSettingsDialogOpen(true);
    }, []);
    const outletContext = useMemo<NotificationsOutletContext>(() => ({ openNotificationSettings }), [openNotificationSettings]);

    const builderEnabled = useBuilderEnabled();
    const artifactsPageEnabled = useIsArtifactsPageEnabled();
    const notificationsPageEnabled = useIsNotificationsEnabled();
    const schedulerEnabled = useIsSchedulerEnabled();
    const userCanReadAgents = userHasCapability(CAPABILITIES.readAgents);
    const userCanReadConnectors = userHasCapability(CAPABILITIES.readConnectors);
    const userCanReadToolsets = userHasCapability(CAPABILITIES.readToolsets);
    const userCanReadSkills = userHasCapability(CAPABILITIES.readSkills);
    const userCanReadWorkflows = userHasCapability(CAPABILITIES.readWorkflows);
    const userCanReadGateways = userHasCapability(CAPABILITIES.readGateways);
    const userCanReadModelConfig = userHasCapability(CAPABILITIES.readModelConfig);
    const userCanReadEvaluations = userHasCapability(CAPABILITIES.readEvaluations);
    const canSeeUserManagement = useIsUserManagementEnabled();
    const canSeeBudgets = useBudgetsEnabled();
    const canSeeFeedback = useFeedbackManagementEnabled();
    const canSeeUsage = useUsageDashboardEnabled();
    const renderNavFooter = useCallback(
        (collapsed: boolean) => (
            <>
                {freeDesktop && <FreeCreditsNavItem collapsed={collapsed} />}
                {notificationsPageEnabled && <NotificationsBell collapsed={collapsed} onOpenSettings={openNotificationSettings} />}
            </>
        ),
        [freeDesktop, notificationsPageEnabled, openNotificationSettings]
    );

    const { items: newNavItems } = useNavigationItems({
        persistenceEnabled: persistenceEnabled ?? false,
        builderEnabled,
        artifactsPageEnabled,
        schedulerEnabled,
        isAuthenticated,
        logoutEnabled: useAuthorization,
        onUserAccountClick: handleUserAccountClick,
        onLogoutClick: logout,
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
    });

    useRadixPointerEventsFix();

    usePageTitle(newNavItems);

    useNotificationSSE();

    useDesktopFileDialogErrors();

    useEffect(() => {
        const handler = () => setIsMobileNavOpen(true);
        globalThis.addEventListener("open-mobile-nav", handler);
        return () => globalThis.removeEventListener("open-mobile-nav", handler);
    }, []);

    useEffect(() => {
        setIsMobileNavOpen(false);
    }, [location.pathname]);

    const activeItem = useMemo(() => activeNavItemId(location.hash ? location.hash.replace("#", "") : location.pathname || "/"), [location.pathname, location.hash]);

    if (useAuthorization && !isAuthenticated) {
        return (
            <div className="flex h-screen items-center justify-center bg-(--background-w10)">
                <Button onClick={login}>Login</Button>
            </div>
        );
    }

    return (
        <div className="app-layout-root relative flex" style={{ height: "calc(100dvh - env(safe-area-inset-top) - env(safe-area-inset-bottom))", marginTop: "env(safe-area-inset-top)" }} data-persistenceenabled={persistenceEnabled}>
            {__SAM_PRODUCT_ANALYTICS__ && <ProductAnalyticsPageViews />}
            <UIModeGuard />
            {showNavigationSidebar && (
                <div className="hidden md:contents">
                    <CollapsibleNavigationSidebar items={newNavItems} activeItemId={activeItem} showNewChatButton showRecentChats={persistenceEnabled ?? false} footer={renderNavFooter} />
                </div>
            )}
            <main className="flex h-full w-full min-w-0 flex-1 flex-col">
                <DesktopExperimentalBanner />
                <ModelWarningBanner />
                <Outlet context={outletContext} />
            </main>
            {showNavigationSidebar && (
                <Sheet open={isMobile && isMobileNavOpen} onOpenChange={setIsMobileNavOpen}>
                    <SheetContent side="left" className="w-64 p-0 sm:max-w-sm md:hidden">
                        <SheetHeader className="sr-only">
                            <SheetTitle>Navigation</SheetTitle>
                            <SheetDescription>Application navigation menu</SheetDescription>
                        </SheetHeader>
                        <CollapsibleNavigationSidebar items={newNavItems} activeItemId={activeItem} isCollapsed={false} hideCollapseButton showNewChatButton showRecentChats={persistenceEnabled ?? false} footer={renderNavFooter} />
                    </SheetContent>
                </Sheet>
            )}
            <ToastContainer />
            <SelectionContextMenu isOpen={isMenuOpen} position={menuPosition} selectedText={selectedText || ""} onClose={clearSelection} sourceTaskId={sourceTaskId} />
            <SettingsDialog open={isSettingsDialogOpen} onOpenChange={setIsSettingsDialogOpen} initialSection={settingsSection} />
            {freeDesktop && <FreeModelSignInExpiredDialog />}
            {freeDesktop ? modelSetupRequested && <WelcomeSetupPage onDone={clearModelSetupRequest} /> : <ModelSetupDialog open={isModelSetupDialogOpen} onOpenChange={handleModelSetupDialogChange} hasWritePermissions={hasModelConfigWrite} />}
            <MoveSessionDialog isOpen={isMoveDialogOpen} onClose={closeMoveDialog} onConfirm={handleMoveConfirm} session={sessionToMove} currentProjectId={sessionToMove?.project?.id} />
            <ChatSessionDeleteDialog open={!!sessionToDelete} onCancel={closeSessionDeleteModal} onConfirm={confirmSessionDelete} sessionName={sessionToDelete?.name || ""} />
            {sessionToShare && <ShareChatDialog sessionId={sessionToShare.id} sessionTitle={sessionToShare.name || "Untitled Chat"} open={isShareDialogOpen} onOpenChange={setShareDialogOpen} />}
        </div>
    );
}

function accountLabel(userInfo: Record<string, unknown> | null): string | null {
    const email = userInfo?.email;
    return typeof email === "string" && email ? email : null;
}

function AppLayout() {
    const { isAuthenticated, useAuthorization, userInfo, logout } = useAuthContext();
    const [signInSource, setSignInSource] = useState(() => pendingSignInSource());
    const [justSignedIn] = useState(() => signInJustCompleted());
    const showSignInHandoff = useAuthorization && isAuthenticated && signInSource !== null;

    useEffect(() => {
        if (!isAuthenticated) return;
        clearSignInCompleted();
        if (showSignInHandoff) settleSignInSource();
    }, [isAuthenticated, showSignInHandoff]);

    if (showSignInHandoff && signInSource) {
        const dismiss = () => setSignInSource(null);
        if (justSignedIn) {
            return <SignedInHandoff sourceLabel={signInSource.label} onContinue={dismiss} />;
        }
        const useDifferentAccount = () => {
            rememberSignInSource(signInSource.key);
            logout();
        };
        return <AlreadySignedInHandoff sourceLabel={signInSource.label} account={accountLabel(userInfo)} onContinue={dismiss} onUseDifferentAccount={useDifferentAccount} />;
    }

    return (
        <BuilderProvider builderTabContent={BuilderTabContent}>
            <ChatProvider>
                <AppLayoutContent />
            </ChatProvider>
        </BuilderProvider>
    );
}

export default AppLayout;
