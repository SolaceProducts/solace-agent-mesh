// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { EmptyState } from "@/common";

interface AgentListPlaceholderProps {
    isLoading: boolean;
    searchTerm: string;
    onClearFilter: () => void;
    searchEmptyVariant?: "notFound" | "noImage";
    className?: string;
    noun?: string;
}

export function AgentListPlaceholder({ isLoading, searchTerm, onClearFilter, searchEmptyVariant = "notFound", className = "flex h-full items-center justify-center", noun = "Agents" }: Readonly<AgentListPlaceholderProps>) {
    if (isLoading) {
        return (
            <div className={className}>
                <EmptyState variant="loading" title="Loading" />
            </div>
        );
    }
    if (searchTerm) {
        return (
            <div className={className}>
                <EmptyState variant={searchEmptyVariant} title={`No ${noun} Found`} subtitle="Try adjusting your search terms" buttons={[{ text: "Clear Filter", variant: "default", onClick: onClearFilter }]} />
            </div>
        );
    }
    return (
        <div className={className}>
            <EmptyState variant="notFound" title={`No ${noun} Found`} />
        </div>
    );
}
