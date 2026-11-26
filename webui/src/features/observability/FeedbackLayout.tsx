// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Outlet, useLocation, useNavigate } from "react-router-dom";

import { Header } from "@/common/layout/Header";
import { PageLayout } from "@/common/layout/PageLayout";

import type { NavItemProps } from "@/ui";

import { sharedFilterSearch } from "./dashboardFilters";
import { FeedbackFilterMemoryProvider } from "./feedbackFilterMemory";
import { OBSERVABILITY_ROUTES } from "./routes";

type ActiveTab = "dashboard" | "history";

function resolveActiveTab(pathname: string): ActiveTab {
    return pathname.startsWith(OBSERVABILITY_ROUTES.feedbackHistory) ? "history" : "dashboard";
}

export function FeedbackLayout() {
    const navigate = useNavigate();
    const { pathname, search } = useLocation();
    const activeTab = resolveActiveTab(pathname);
    const forwardedSearch = sharedFilterSearch(search);

    const tabs: NavItemProps[] = [
        { id: "feedback-dashboard", label: "Dashboard", isActive: activeTab === "dashboard", onClick: () => navigate({ pathname: OBSERVABILITY_ROUTES.feedback, search: forwardedSearch }) },
        { id: "feedback-history", label: "Feedback History", isActive: activeTab === "history", onClick: () => navigate({ pathname: OBSERVABILITY_ROUTES.feedbackHistory, search: forwardedSearch }) },
    ];

    return (
        <FeedbackFilterMemoryProvider>
            <PageLayout>
                <Header title="Feedback" tabs={tabs} />
                <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
                    <Outlet />
                </div>
            </PageLayout>
        </FeedbackFilterMemoryProvider>
    );
}
