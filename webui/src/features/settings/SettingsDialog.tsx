// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { Info } from "lucide-react";
import type { ReactNode } from "react";

import { useAuthContext } from "@/providers/contexts";

import { useIsDesktop } from "@/hooks/useIsDesktop";
import { useIsNotificationsEnabled } from "@/hooks/useIsNotificationsEnabled";
import { useIsProductAnalyticsEnabled } from "@/hooks/useIsProductAnalyticsEnabled";

import { ExternalApplications } from "@/features/notifications/ExternalApplications";
import { NotificationTypes } from "@/features/notifications/NotificationTypes";
import { AboutProduct } from "@/features/settings/AboutProduct";
import { SupportSettings } from "@/features/settings/SupportSettings";

import { Dialog, DialogContent, DialogDescription, DialogTitle, LifecycleBadge, VisuallyHidden } from "@/ui";
import { cn } from "@/utils";

import { GeneralSettings } from "./GeneralSettings";
import { PrivacySettings } from "./PrivacySettings";
import { ProfileSettings } from "./ProfileSettings";
import { UsageSettings } from "./UsageSettings";

export type SettingsSection = "general" | "profile" | "usage" | "notifications" | "privacy" | "support" | "about";

interface SidebarItemProps {
    icon?: ReactNode;
    label: string;
    active: boolean;
    onClick: () => void;
}

const SidebarItem = ({ icon = null, label, active, onClick }: SidebarItemProps) => {
    return (
        <button onClick={onClick} className={cn("flex w-full cursor-pointer items-center justify-center gap-3 p-4 transition-colors sm:justify-start", active ? "bg-(--brand-w10)" : "text-(--secondary-text-wMain) hover:bg-(--secondary-w10)")}>
            {icon}
            <span>{label}</span>
        </button>
    );
};

const getSection = (activeSection: SettingsSection, isDesktop: boolean, productAnalyticsEnabled: boolean, useAuthorization: boolean) => {
    if (activeSection === "about") {
        return { title: "About", content: <AboutProduct /> };
    } else if (activeSection === "support" && isDesktop) {
        return { title: "Support", content: <SupportSettings /> };
    } else if (activeSection === "profile" && useAuthorization) {
        return { title: "Profile", content: <ProfileSettings /> };
    } else if (activeSection === "usage") {
        return { title: "Usage", content: <UsageSettings /> };
    } else if (activeSection === "notifications") {
        return {
            title: (
                <>
                    Notifications <LifecycleBadge variant="transparent">EARLY ACCESS</LifecycleBadge>
                </>
            ),
            content: (
                <div className="flex flex-col gap-8">
                    <NotificationTypes />
                    <ExternalApplications />
                </div>
            ),
        };
    } else if (activeSection === "privacy" && __SAM_PRODUCT_ANALYTICS__ && productAnalyticsEnabled) {
        return { title: "Privacy", content: <PrivacySettings /> };
    } else {
        return { title: "General", content: <GeneralSettings /> };
    }
};

interface SettingsDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    initialSection?: SettingsSection;
}

export function SettingsDialog({ open, onOpenChange, initialSection = "general" }: Readonly<SettingsDialogProps>) {
    const notificationsEnabled = useIsNotificationsEnabled();
    const isDesktop = useIsDesktop();
    const productAnalyticsEnabled = useIsProductAnalyticsEnabled();
    const { useAuthorization } = useAuthContext();
    const [activeSection, setActiveSection] = useState<SettingsSection>(initialSection);

    useEffect(() => {
        if (open) setActiveSection(initialSection);
    }, [open, initialSection]);

    const { title, content } = getSection(activeSection, isDesktop, productAnalyticsEnabled, useAuthorization);

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="w-[90vw] gap-0 p-0 sm:max-w-250" showCloseButton={true}>
                <VisuallyHidden>
                    <DialogDescription>Configure user account settings</DialogDescription>
                </VisuallyHidden>
                <div className="flex h-[min(80vh,650px)] flex-col overflow-hidden sm:flex-row">
                    <div className="flex w-full shrink-0 flex-col border-b sm:min-h-0 sm:w-64 sm:border-r sm:border-b-0">
                        <DialogTitle asChild>
                            <h2 className="flex h-12 items-center border-b px-4 text-sm font-semibold">User Account Settings</h2>
                        </DialogTitle>

                        <nav className="flex flex-1 sm:min-h-0">
                            <div className="flex flex-1 sm:min-h-0 sm:flex-col">
                                <div className="flex-1 sm:overflow-y-auto">
                                    <SidebarItem label="General" active={activeSection === "general"} onClick={() => setActiveSection("general")} />
                                    {useAuthorization && <SidebarItem label="Profile" active={activeSection === "profile"} onClick={() => setActiveSection("profile")} />}
                                    <SidebarItem label="Usage" active={activeSection === "usage"} onClick={() => setActiveSection("usage")} />
                                    {notificationsEnabled && <SidebarItem label="Notifications" active={activeSection === "notifications"} onClick={() => setActiveSection("notifications")} />}
                                    {productAnalyticsEnabled && <SidebarItem label="Privacy" active={activeSection === "privacy"} onClick={() => setActiveSection("privacy")} />}
                                    {isDesktop && <SidebarItem label="Support" active={activeSection === "support"} onClick={() => setActiveSection("support")} />}
                                </div>
                                <div className="flex-1 border-l sm:flex-none sm:border-l-0">
                                    <hr className="hidden border-t sm:mt-4 sm:block" />
                                    <SidebarItem icon={<Info className="size-5" />} label="About" active={activeSection === "about"} onClick={() => setActiveSection("about")} />
                                </div>
                            </div>
                        </nav>
                    </div>

                    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
                        <h3 className="hidden h-12 items-center gap-2 border-b px-6 text-base font-semibold sm:flex">{title}</h3>

                        <div className="flex-1 overflow-y-auto p-4 sm:p-8">{content}</div>
                    </div>
                </div>
            </DialogContent>
        </Dialog>
    );
}
