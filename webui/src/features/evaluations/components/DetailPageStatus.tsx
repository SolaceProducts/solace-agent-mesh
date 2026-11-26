// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { BreadcrumbItem } from "@/common/layout";

import { EmptyState, Header } from "@/common";

interface DetailPageLoadingProps {
    title: string;
    breadcrumbs: BreadcrumbItem[];
}

export function DetailPageLoading({ title, breadcrumbs }: DetailPageLoadingProps) {
    return (
        <div className="flex h-full flex-col">
            <Header title={title} breadcrumbs={breadcrumbs} />
            <EmptyState variant="loading" title="Loading..." />
        </div>
    );
}

interface DetailPageNotFoundProps {
    title: string;
    breadcrumbs: BreadcrumbItem[];
    notFoundTitle: string;
    backLabel: string;
    onBack: () => void;
}

export function DetailPageNotFound({ title, breadcrumbs, notFoundTitle, backLabel, onBack }: DetailPageNotFoundProps) {
    return (
        <div className="flex h-full flex-col">
            <Header title={title} breadcrumbs={breadcrumbs} />
            <EmptyState variant="error" title={notFoundTitle} buttons={[{ text: backLabel, variant: "default", onClick: onBack }]} />
        </div>
    );
}
