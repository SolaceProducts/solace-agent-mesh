// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { type ReactNode, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ChevronLeft, ChevronRight, Plus } from "lucide-react";
import { useLocation, useNavigate } from "react-router-dom";

import { useChatContext, useConfigContext } from "@/providers/contexts";

import { resolveLogo } from "@/utils/resolveLogo";

import type { HeaderConfig, NavItemConfig, NewChatConfig } from "@/types/nav";

import { RecentChatsHeader, RecentChatsList } from "@/features/chat/recentChats";
import { MAX_RECENT_CHATS } from "@/features/navigation/constants";

import { LogoImage, SolaceIcon } from "@/common";
import { useSessionStorage } from "@/hooks";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui";
import { cn } from "@/utils";

import { iconStyles, iconWrapperStyles, navButtonStyles, navTextStyles } from "./navigationStyles";
import { NavItemButton } from "./NavItemButton";
import { RecentChatsFlyout } from "./RecentChatsFlyout";
import { routeMatchesPrefix } from "./routeMatch";
import type { NavItem } from "./types";

const ConditionalTooltip: React.FC<{ show: boolean; label: string; tooltip?: string; children: React.ReactElement }> = ({ show, label, tooltip, children }) => (
    <Tooltip>
        <TooltipTrigger asChild>{children}</TooltipTrigger>
        {(show || tooltip) && <TooltipContent side="right">{show ? label : tooltip}</TooltipContent>}
    </Tooltip>
);

const DefaultSidebarLogo: React.FC<{ isCollapsed: boolean }> = ({ isCollapsed }) => {
    const { configLogoUrl, configSmallLogoUrl } = useConfigContext();

    const logo = isCollapsed ? resolveLogo(configSmallLogoUrl, configLogoUrl) : resolveLogo(configLogoUrl, configSmallLogoUrl);

    const fallback = isCollapsed ? <SolaceIcon variant="short" className="h-8 w-8" /> : <SolaceIcon variant="full" className="h-8 w-24" />;
    if (logo.kind === "default") return fallback;

    const className = isCollapsed ? "h-8 w-8 object-contain" : "h-8 w-24 object-contain object-left";
    return <LogoImage key={logo.url} url={logo.url} className={className} fallback={fallback} />;
};

export interface CollapsibleNavigationSidebarProps {
    items: NavItemConfig[];

    header?: HeaderConfig | ReactNode;

    showNewChatButton?: boolean;
    newChatConfig?: NewChatConfig;
    showRecentChats?: boolean;

    onNavigate?: (itemId: string, route?: string) => void;
    onCollapseChange?: (isCollapsed: boolean) => void;

    activeItemId?: string | string[];
    isCollapsed?: boolean;
    defaultCollapsed?: boolean;

    hideCollapseButton?: boolean;

    footer?: (isCollapsed: boolean) => ReactNode;
}

export const CollapsibleNavigationSidebar = ({
    items,
    header,
    showNewChatButton = true,
    newChatConfig,
    showRecentChats = true,
    onNavigate,
    onCollapseChange,
    activeItemId: controlledActiveItemId,
    isCollapsed: controlledIsCollapsed,
    defaultCollapsed = false,
    hideCollapseButton: hideCollapseButtonProp = false,
    footer,
}: CollapsibleNavigationSidebarProps) => {
    const navigate = useNavigate();
    const location = useLocation();

    const [internalCollapsed, setInternalCollapsed] = useSessionStorage("nav-collapsed", defaultCollapsed);
    const isCollapsed = controlledIsCollapsed ?? internalCollapsed;
    const setIsCollapsed = (value: boolean) => {
        setInternalCollapsed(value);
        onCollapseChange?.(value);
    };

    const [internalActiveItem, setInternalActiveItem] = useState<string>("");
    const controlledActiveIds = useMemo(() => {
        if (controlledActiveItemId == null) return null;
        return new Set(Array.isArray(controlledActiveItemId) ? controlledActiveItemId : [controlledActiveItemId]);
    }, [controlledActiveItemId]);
    const isItemActive = useCallback(
        (id: string) => {
            if (controlledActiveIds) return controlledActiveIds.has(id);
            return internalActiveItem === id;
        },
        [controlledActiveIds, internalActiveItem]
    );
    const setActiveItem = (value: string) => {
        if (controlledActiveItemId === undefined) {
            setInternalActiveItem(value);
        }
    };

    const [expandedMenus, setExpandedMenus] = useState<Record<string, boolean>>(() => {
        const initial: Record<string, boolean> = { assets: false, systemManagement: false };
        items.forEach(item => {
            if (item.children && item.defaultExpanded !== undefined) {
                initial[item.id] = item.defaultExpanded;
            }
        });
        return initial;
    });
    const { handleNewSession } = useChatContext();

    const scrollRef = useRef<HTMLDivElement>(null);
    const itemRefs = useRef<Record<string, HTMLDivElement | null>>({});
    const pendingScrollId = useRef<string | null>(null);
    const [navScrolled, setNavScrolled] = useState(false);

    useLayoutEffect(() => {
        const id = pendingScrollId.current;
        if (!id) return;
        pendingScrollId.current = null;
        const container = scrollRef.current;
        const el = itemRefs.current[id];
        if (!container || !el) return;
        const c = container.getBoundingClientRect();
        const e = el.getBoundingClientRect();
        if (e.bottom > c.bottom) {
            container.scrollTop += e.top - c.top;
        }
    }, [expandedMenus]);

    const isItemActiveByRoute = useCallback((item: NavItemConfig): boolean => routeMatchesPrefix(location.pathname, item.routeMatch), [location.pathname]);

    const findActiveItemId = useCallback(
        (items: NavItemConfig[]): string | null => {
            for (const item of items) {
                if (isItemActiveByRoute(item)) return item.id;
                if (item.children) {
                    const childMatch = item.children.find(child => isItemActiveByRoute(child));
                    if (childMatch) return childMatch.id;
                }
            }
            return null;
        },
        [isItemActiveByRoute]
    );

    useEffect(() => {
        if (controlledActiveItemId !== undefined) return;

        const matchedId = findActiveItemId(items);
        if (matchedId) {
            setInternalActiveItem(matchedId);
            items.forEach(item => {
                if (item.children?.some(child => child.id === matchedId)) {
                    setExpandedMenus(prev => ({ ...prev, [item.id]: true }));
                }
            });
        } else {
            setInternalActiveItem("");
        }
    }, [location.pathname, items, controlledActiveItemId, findActiveItemId]);

    const handleItemClick = (itemId: string, item: NavItem) => {
        setActiveItem(itemId);

        if (item.onClick) {
            item.onClick();
            return;
        }

        onNavigate?.(itemId, item.route);
    };

    const toggleMenu = (menuId: string) => {
        setExpandedMenus(prev => {
            const willExpand = !prev[menuId];
            if (willExpand) pendingScrollId.current = menuId;
            return {
                ...prev,
                [menuId]: willExpand,
            };
        });
    };

    const handleToggle = () => {
        setIsCollapsed(!isCollapsed);
    };

    const toNavItem = useCallback((config: NavItemConfig): NavItem => {
        const convertItem = (item: NavItemConfig): NavItem => {
            const visibleChildren = item.children?.filter(c => !c.hidden);
            return {
                ...item,
                hasSubmenu: !!visibleChildren?.length,
                children: visibleChildren?.map(convertItem),
            };
        };
        return convertItem(config);
    }, []);

    const navItems: NavItem[] = useMemo(() => items.filter(item => !item.hidden && item.position !== "bottom").map(toNavItem), [items, toNavItem]);
    const bottomItems: NavItem[] = useMemo(() => items.filter(item => !item.hidden && item.position === "bottom").map(toNavItem), [items, toNavItem]);

    const handleNewChatClickResolved = useCallback(() => {
        if (newChatConfig?.onClick) {
            newChatConfig.onClick();
            return;
        }
        navigate("/chat");
        handleNewSession();
    }, [newChatConfig, navigate, handleNewSession]);

    const newChatLabel = newChatConfig?.label ?? "New Chat";
    const NewChatIcon = newChatConfig?.icon ?? Plus;

    const renderHeader = (): ReactNode => {
        if (header && typeof header === "object" && header !== null && "component" in header) {
            const headerConfig = header as HeaderConfig;
            if (headerConfig.component) return headerConfig.component;
        } else if (header !== undefined && header !== null) {
            return header as ReactNode;
        }
        return <DefaultSidebarLogo isCollapsed={isCollapsed} />;
    };

    const hideCollapseButton = hideCollapseButtonProp || (header && typeof header === "object" && "hideCollapseButton" in header && (header as HeaderConfig).hideCollapseButton);

    const handleBottomItemClick = (item: NavItem) => {
        item.onClick?.();
    };

    const textAnimClass = isCollapsed ? "max-w-0 opacity-0" : "max-w-[180px] opacity-100";
    const textAnimBase = "overflow-hidden whitespace-nowrap transition-[opacity,max-width] duration-200";

    return (
        <nav className={cn("navigation-sidebar flex h-full flex-col overflow-visible border-r bg-(--darkSurface-bg) transition-[width] duration-200 ease-out", isCollapsed ? "w-20" : "w-64")}>
            <div className="relative flex min-h-[80px] w-full items-center border-b border-(--secondary-w70) py-3 pr-4 pl-7">
                <div className="flex items-center gap-2">{renderHeader()}</div>
                {!hideCollapseButton &&
                    (isCollapsed ? (
                        <Tooltip>
                            <TooltipTrigger asChild>
                                <button onClick={handleToggle} className="absolute -right-3 z-30 flex h-8 w-7 cursor-pointer items-center justify-center rounded bg-(--darkSurface-bg) p-0.5 shadow-md hover:bg-(--darkSurface-bgHover)">
                                    <ChevronRight className="size-6 text-(--darkSurface-text)" />
                                </button>
                            </TooltipTrigger>
                            <TooltipContent side="right">Expand Navigation</TooltipContent>
                        </Tooltip>
                    ) : (
                        <Tooltip>
                            <TooltipTrigger asChild>
                                <button onClick={handleToggle} className="ml-auto flex h-8 w-8 cursor-pointer items-center justify-center p-1 text-(--darkSurface-text) hover:bg-(--darkSurface-bgHover)">
                                    <ChevronLeft className="size-6" />
                                </button>
                            </TooltipTrigger>
                            <TooltipContent side="right">Collapse Navigation</TooltipContent>
                        </Tooltip>
                    ))}
            </div>

            {showNewChatButton && (
                <div className={cn("border-b pt-3 transition-colors duration-200", navScrolled ? "border-(--secondary-w70)" : "border-transparent")}>
                    <ConditionalTooltip show={isCollapsed} label={newChatLabel}>
                        <button onClick={handleNewChatClickResolved} className={navButtonStyles()}>
                            <div className={iconWrapperStyles({ active: false, withMargin: true })}>
                                <NewChatIcon className={iconStyles({ active: false })} />
                            </div>
                            <span className={cn(navTextStyles({ active: false }), textAnimBase, textAnimClass)}>{newChatLabel}</span>
                        </button>
                    </ConditionalTooltip>
                </div>
            )}

            <div ref={scrollRef} onScroll={e => setNavScrolled(e.currentTarget.scrollTop > 0)} className={cn("min-h-0 flex-1 [scrollbar-width:none] overflow-y-auto [&::-webkit-scrollbar]:hidden", showNewChatButton ? "pb-3" : "py-3")}>
                <div>
                    {navItems.map(item => {
                        const hasActiveChild = item.children?.some(child => isItemActive(child.id)) ?? false;
                        return (
                            <div
                                key={item.id}
                                ref={el => {
                                    itemRefs.current[item.id] = el;
                                }}
                            >
                                <ConditionalTooltip show={isCollapsed} label={item.label} tooltip={item.tooltip}>
                                    <NavItemButton
                                        item={item}
                                        isActive={isItemActive(item.id)}
                                        isCollapsed={isCollapsed}
                                        onClick={() => {
                                            if (isCollapsed && item.hasSubmenu) {
                                                setActiveItem(item.id);
                                                setExpandedMenus(prev => ({ ...prev, [item.id]: true }));
                                                setIsCollapsed(false);
                                            } else {
                                                handleItemClick(item.id, item);
                                            }
                                        }}
                                        isExpanded={expandedMenus[item.id]}
                                        onToggleExpand={() => {
                                            if (isCollapsed && item.hasSubmenu) {
                                                setActiveItem(item.id);
                                                pendingScrollId.current = item.id;
                                                setExpandedMenus(prev => ({ ...prev, [item.id]: true }));
                                                setIsCollapsed(false);
                                            } else {
                                                toggleMenu(item.id);
                                            }
                                        }}
                                        hasActiveChild={hasActiveChild}
                                    />
                                </ConditionalTooltip>
                                {!isCollapsed && item.hasSubmenu && expandedMenus[item.id] && item.children && (
                                    <div className="pl-10">
                                        {item.children.map(child => {
                                            const isChildActive = isItemActive(child.id);
                                            return (
                                                <div key={child.id} className="group relative">
                                                    <div className={cn("absolute top-0 left-0 h-full bg-(--brand-w60) transition-all", isChildActive ? "w-[3px]" : "w-px opacity-30")} />
                                                    {child.tooltip ? (
                                                        <Tooltip>
                                                            <TooltipTrigger asChild>
                                                                <div>
                                                                    <NavItemButton item={child} isActive={isChildActive} onClick={() => handleItemClick(child.id, child)} indent />
                                                                </div>
                                                            </TooltipTrigger>
                                                            <TooltipContent side="right">{child.tooltip}</TooltipContent>
                                                        </Tooltip>
                                                    ) : (
                                                        <NavItemButton item={child} isActive={isChildActive} onClick={() => handleItemClick(child.id, child)} indent />
                                                    )}
                                                </div>
                                            );
                                        })}
                                    </div>
                                )}
                            </div>
                        );
                    })}
                </div>

                {showRecentChats && !isCollapsed && (
                    <div className="mt-6 flex flex-col">
                        <div className="border-t border-(--secondary-w70)" />
                        <RecentChatsHeader to="/recent-chats" className="pt-6 pr-6 pl-6" />
                        <RecentChatsList maxItems={MAX_RECENT_CHATS} />
                    </div>
                )}
            </div>

            {showRecentChats && isCollapsed && <RecentChatsFlyout />}

            <div className="relative z-10 mt-auto border-t border-(--secondary-w70) bg-(--darkSurface-bg) py-3">
                {footer?.(isCollapsed)}
                {bottomItems.map(item => (
                    <ConditionalTooltip key={item.id} show={isCollapsed} label={item.label}>
                        <NavItemButton item={item} isActive={isItemActive(item.id)} isCollapsed={isCollapsed} onClick={() => handleBottomItemClick(item)} />
                    </ConditionalTooltip>
                ))}
            </div>
        </nav>
    );
};
