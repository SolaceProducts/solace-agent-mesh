// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";

import { Header } from "@/common/layout/Header";
import { PageLayout } from "@/common/layout/PageLayout";

import { Button } from "@/ui/button";

import type { NavItemProps } from "@/ui";

import { RoleDefaultDialog } from "./RoleDefaultDialog";
import { USER_MANAGEMENT_ROUTES } from "./routes";

type ActiveTab = "claimMapping" | "users" | "roles";

function resolveActiveTab(pathname: string): ActiveTab {
    if (pathname.startsWith(USER_MANAGEMENT_ROUTES.roles)) return "roles";
    if (pathname.startsWith(USER_MANAGEMENT_ROUTES.claimMapping)) return "claimMapping";
    return "users";
}

export function UserManagementLayout() {
    const navigate = useNavigate();
    const { pathname } = useLocation();
    const activeTab = resolveActiveTab(pathname);
    const [manageDefaultOpen, setManageDefaultOpen] = useState(false);

    const tabs: NavItemProps[] = [
        { id: "claim-mapping", label: "Claim Mappings", isActive: activeTab === "claimMapping", onClick: () => navigate(USER_MANAGEMENT_ROUTES.claimMapping) },
        { id: "users", label: "Users", isActive: activeTab === "users", onClick: () => navigate(USER_MANAGEMENT_ROUTES.users) },
        { id: "roles", label: "Roles", isActive: activeTab === "roles", onClick: () => navigate(USER_MANAGEMENT_ROUTES.roles) },
    ];

    let buttons: ReactNode[] = [];
    if (activeTab === "roles") {
        buttons = [
            <Button key="manage-default-role" variant="ghost" onClick={() => setManageDefaultOpen(true)}>
                Manage Default Roles
            </Button>,
        ];
    }

    return (
        <PageLayout>
            <Header title="User Management" tabs={tabs} buttons={buttons} />
            <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
                <Outlet />
            </div>
            <RoleDefaultDialog open={manageDefaultOpen} onOpenChange={setManageDefaultOpen} />
        </PageLayout>
    );
}
