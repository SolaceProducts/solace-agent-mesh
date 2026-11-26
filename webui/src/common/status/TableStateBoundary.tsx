// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactNode } from "react";

import { type ButtonWithCallback, EmptyState } from "./EmptyState";

interface TableStateBoundaryProps {
    resourceName: string;
    isLoading?: boolean;
    error?: Error | null;
    itemCount: number;
    hasActiveFilters?: boolean;
    onClearFilters?: () => void;
    emptyActions?: ButtonWithCallback[];
    errorActions?: ButtonWithCallback[];
    emptyTitle?: string;
    emptyDescription?: ReactNode;
    stateClassName?: string;
    children: ReactNode;
}

export function TableStateBoundary({ resourceName, isLoading, error, itemCount, hasActiveFilters = false, onClearFilters, emptyActions, errorActions, emptyTitle, emptyDescription, stateClassName, children }: Readonly<TableStateBoundaryProps>) {
    let state: ReactNode = null;
    if (isLoading) {
        state = <EmptyState variant="loading" title="Loading..." className={stateClassName} />;
    } else if (error) {
        state = <EmptyState variant="error" title={`Couldn't Load ${resourceName}`} subtitle={error.message} buttons={errorActions} className={stateClassName} />;
    } else if (itemCount === 0 && hasActiveFilters) {
        state = <EmptyState variant="noImage" title={`No ${resourceName} Match Your Filters`} buttons={onClearFilters ? [{ text: "Clear Filters", variant: "outline", onClick: onClearFilters }] : undefined} className={stateClassName} />;
    } else if (itemCount === 0) {
        state = <EmptyState variant="noImage" title={emptyTitle ?? `No ${resourceName} Found`} subtitle={emptyDescription} buttons={emptyActions} className={stateClassName} />;
    }

    if (state) return <div className="flex min-h-0 flex-1">{state}</div>;
    return <>{children}</>;
}
