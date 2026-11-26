// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { ChevronRight, Menu } from "lucide-react";

import { useIsMobile } from "@/hooks";
import { Button, NavItem, type NavItemProps } from "@/ui";

export interface BreadcrumbItem {
    label: string;
    onClick?: () => void;
}

export interface HeaderProps {
    title: string | React.ReactNode;
    breadcrumbs?: BreadcrumbItem[];
    tabs?: NavItemProps[];
    buttons?: React.ReactNode[];
    leadingAction?: React.ReactNode;
}

interface BreadcrumbsProps {
    breadcrumbs: BreadcrumbItem[];
    isMobile: boolean;
}

const Breadcrumbs: React.FC<BreadcrumbsProps> = ({ breadcrumbs, isMobile }) => {
    if (isMobile) {
        return <MobileBreadcrumb breadcrumbs={breadcrumbs} />;
    }
    return <DesktopBreadcrumb breadcrumbs={breadcrumbs} />;
};

const MobileBreadcrumb: React.FC<{ breadcrumbs: BreadcrumbItem[] }> = ({ breadcrumbs }) => {
    const last = breadcrumbs[breadcrumbs.length - 1];
    const parent = breadcrumbs.length > 1 ? breadcrumbs[breadcrumbs.length - 2] : null;
    return (
        <div className="flex min-w-0 items-center">
            {parent && <MobileBreadcrumbParent parent={parent} />}
            <div className="min-w-0 truncate">{last.label}</div>
        </div>
    );
};

const MobileBreadcrumbParent: React.FC<{ parent: BreadcrumbItem }> = ({ parent }) => (
    <>
        {parent.onClick ? (
            <Button variant="link" className="m-0 shrink-0 p-0" onClick={parent.onClick} aria-label={`Back to ${parent.label}`}>
                …
            </Button>
        ) : (
            <span className="shrink-0">…</span>
        )}
        <span className="mx-1 shrink-0">
            <ChevronRight size={16} />
        </span>
    </>
);

const DesktopBreadcrumb: React.FC<{ breadcrumbs: BreadcrumbItem[] }> = ({ breadcrumbs }) => (
    <div className="flex items-center">
        {breadcrumbs.map((crumb, index) => (
            <React.Fragment key={index}>
                {index > 0 && (
                    <span className="mx-1">
                        <ChevronRight size={16} />
                    </span>
                )}
                {crumb.onClick ? (
                    <Button variant="link" className="m-0 p-0" onClick={crumb.onClick}>
                        {crumb.label}
                    </Button>
                ) : (
                    <div className="max-w-[min(50ch,50vw)] truncate">{crumb.label}</div>
                )}
            </React.Fragment>
        ))}
    </div>
);

const TabsRow: React.FC<{ tabs: NavItemProps[]; className: string }> = ({ tabs, className }) => (
    <div className={className} role="tablist">
        {tabs.map(item => (
            <div key={item.id} className="shrink-0">
                <NavItem {...item} />
            </div>
        ))}
    </div>
);

export const Header: React.FC<HeaderProps> = ({ title, breadcrumbs, tabs, buttons, leadingAction }) => {
    const hasTabs = tabs && tabs.length > 0;
    const hasBreadcrumbs = breadcrumbs && breadcrumbs.length > 0;
    const isMobile = useIsMobile();
    return (
        <div className="border-b">
            <div className="relative flex max-h-[80px] min-h-[80px] w-full items-center px-4 md:px-8">
                {hasBreadcrumbs && (
                    <div className="absolute top-1 right-2 left-4 flex h-8 items-center md:right-auto md:left-8">
                        <Breadcrumbs breadcrumbs={breadcrumbs} isMobile={isMobile} />
                    </div>
                )}

                {isMobile && (
                    <div className="mr-2 flex items-center pt-[35px]">
                        <Button variant="ghost" onClick={() => globalThis.dispatchEvent(new CustomEvent("open-mobile-nav"))} className="h-10 w-10 p-0" tooltip="Menu">
                            <Menu className="size-5" />
                        </Button>
                    </div>
                )}

                {leadingAction && <div className="mr-4 flex items-center pt-[35px]">{leadingAction}</div>}

                <div className="min-w-0 flex-1 truncate pt-[35px] text-xl md:max-w-lg md:flex-none">{title}</div>

                {hasTabs && !isMobile && <TabsRow tabs={tabs} className="scrollbar-hide ml-4 flex min-w-0 items-center gap-4 overflow-x-auto pt-[35px] sm:gap-6 md:ml-8" />}

                {!isMobile && <div className="flex-1" />}

                {buttons && buttons.length > 0 && (
                    <div className="ml-2 flex flex-shrink-0 items-center gap-2 pt-[35px]">
                        {buttons.map((button, index) => (
                            <React.Fragment key={index}>{button}</React.Fragment>
                        ))}
                    </div>
                )}
            </div>

            {hasTabs && isMobile && <TabsRow tabs={tabs} className="scrollbar-hide flex items-center gap-4 overflow-x-auto px-4 pb-2" />}
        </div>
    );
};
