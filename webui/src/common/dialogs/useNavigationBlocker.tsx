// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type JSX, useCallback, useEffect, useState } from "react";
import { useBlocker } from "react-router-dom";

import { ConfirmationDialog } from "./ConfirmationDialog";

interface UseNavigationBlockerReturn {
    NavigationBlocker: () => JSX.Element | null;
    allowNavigation: (navigationFn: () => void) => void;
    setBlockingEnabled: (enabled: boolean) => void;
}

export function useNavigationBlocker(): UseNavigationBlockerReturn {
    const [showConfirmationDialog, setShowConfirmationDialog] = useState(false);
    const [isNavigationAllowed, setIsNavigationAllowed] = useState(false);
    const [blockingEnabled, setBlockingEnabled] = useState(false);
    const [pendingNavigation, setPendingNavigation] = useState<(() => void) | null>(null);

    const blocker = useBlocker(({ currentLocation, nextLocation }) => blockingEnabled && !isNavigationAllowed && currentLocation.pathname !== nextLocation.pathname);

    useEffect(() => {
        if (blocker.state === "blocked") {
            setShowConfirmationDialog(true);
        }
    }, [blocker]);

    useEffect(() => {
        if (isNavigationAllowed && pendingNavigation) {
            pendingNavigation();
            setPendingNavigation(null);
        }
    }, [isNavigationAllowed, pendingNavigation]);

    const confirmNavigation = useCallback(() => {
        setShowConfirmationDialog(false);
        if (blocker.state === "blocked") {
            blocker.proceed();
        }
    }, [blocker]);

    const cancelNavigation = useCallback(() => {
        setShowConfirmationDialog(false);
        if (blocker.state === "blocked") {
            blocker.reset();
        }
    }, [blocker]);

    const allowNavigation = useCallback((navigationFn: () => void) => {
        setBlockingEnabled(false);
        setIsNavigationAllowed(true);
        setPendingNavigation(() => navigationFn);
    }, []);

    const NavigationBlocker = useCallback(() => {
        return (
            <ConfirmationDialog
                title="Unsaved Changes Will Be Discarded"
                description="Leaving the form will discard any unsaved changes."
                open={showConfirmationDialog}
                onConfirm={confirmNavigation}
                onCancel={cancelNavigation}
                onOpenChange={setShowConfirmationDialog}
                actionLabels={{ cancel: "Return to Form", confirm: "Leave & Discard Changes" }}
            />
        );
    }, [showConfirmationDialog, confirmNavigation, cancelNavigation]);

    return {
        NavigationBlocker,
        allowNavigation,
        setBlockingEnabled,
    };
}
