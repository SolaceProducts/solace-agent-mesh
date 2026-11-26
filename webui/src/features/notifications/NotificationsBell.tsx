// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { Bell } from "lucide-react";
import { useNavigate } from "react-router-dom";

import type { NotificationItem } from "@/api/notifications";
import { useMarkAllNotificationsRead, useNotificationInbox } from "@/api/notifications";

import { cn } from "@/utils/cnTailwind";

import { iconStyles, iconWrapperStyles, navButtonStyles, navTextStyles } from "@/features/navigation/navigationStyles";

import { Button, Popover, PopoverContent, PopoverTrigger } from "@/ui";

import { isHilPending } from "./hilState";
import { actionLabel, navigableAction, type NotificationAction, useOpenNotificationAction } from "./notificationAction";
import { useNotificationCategories } from "./notificationCategory";
import { NotificationCategoryCaption } from "./NotificationCategoryCaption";
import { NEEDS_RESPONSE_LABEL, UnreadIndicator } from "./notificationRow";
import { formatNotificationTime } from "./notificationTime";
import { useLiveNotificationInbox } from "./useLiveNotificationInbox";

const PANEL_LIMIT = 12;

function unreadAnnouncement(count: number): string {
    if (count === 0) {
        return "";
    }
    return count === 1 ? "1 unread notification" : `${count} unread notifications`;
}

interface RecencyGroup {
    label: string;
    items: NotificationItem[];
}

function groupByRecency(items: NotificationItem[]): RecencyGroup[] {
    const now = Date.now();
    const today: NotificationItem[] = [];
    const week: NotificationItem[] = [];
    const older: NotificationItem[] = [];
    const todayStr = new Date(now).toDateString();
    for (const n of items) {
        const t = new Date(n.createdAt).getTime();
        if (new Date(t).toDateString() === todayStr) today.push(n);
        else if (now - t < 7 * 86_400_000) week.push(n);
        else older.push(n);
    }
    return [
        { label: "Today", items: today },
        { label: "This week", items: week },
        { label: "Older", items: older },
    ].filter(g => g.items.length > 0);
}

function FlyoutActionCTA({ n, onOpen }: Readonly<{ n: NotificationItem; onOpen: (n: NotificationItem, action: NotificationAction) => void }>) {
    const action = navigableAction(n);
    if (!action) {
        return null;
    }
    return (
        <div className="px-4 pb-2.5">
            <Button variant="link" size="sm" className="h-auto cursor-pointer p-0 font-normal" onClick={() => onOpen(n, action)}>
                {actionLabel(action, n)}
            </Button>
        </div>
    );
}

interface NotificationsBellProps {
    collapsed?: boolean;
    onOpenSettings?: () => void;
}

export function NotificationsBell({ collapsed = false, onOpenSettings }: Readonly<NotificationsBellProps>) {
    const navigate = useNavigate();
    const openNotificationAction = useOpenNotificationAction();
    const [open, setOpen] = useState(false);
    useLiveNotificationInbox();
    const { data: unread, refetch: refetchUnread } = useNotificationInbox(1, 1, true, true);
    const { data: recent, refetch: refetchRecent } = useNotificationInbox(1, PANEL_LIMIT, false, true);
    const markAll = useMarkAllNotificationsRead();
    const categories = useNotificationCategories();

    const onOpenChange = (next: boolean) => {
        setOpen(next);
        if (next) {
            refetchUnread();
            refetchRecent();
        }
    };

    const count = unread?.meta.pagination.count ?? 0;
    const items = recent?.data ?? [];
    const groups = groupByRecency(items);

    const goToPage = () => {
        setOpen(false);
        navigate("/notifications");
    };
    const openSettings = () => {
        setOpen(false);
        onOpenSettings?.();
    };

    const openNotification = (id: string) => {
        setOpen(false);
        navigate(`/notifications?focus=${encodeURIComponent(id)}`);
    };

    const openAction = (n: NotificationItem, action: NotificationAction) => {
        setOpen(false);
        openNotificationAction(n, action);
    };

    return (
        <Popover open={open} onOpenChange={onOpenChange}>
            <output aria-live="polite" className="sr-only">
                {unreadAnnouncement(count)}
            </output>
            <PopoverTrigger asChild>
                <button type="button" title={collapsed ? "Notifications" : undefined} aria-label={count > 0 ? `Notifications (${count} unread)` : "Notifications"} className={navButtonStyles({ active: open })}>
                    <div className={cn(iconWrapperStyles({ active: open, withMargin: true }), "relative")}>
                        <Bell className={iconStyles({ active: open })} />
                        {count > 0 && (
                            <span aria-hidden="true" className="absolute -top-1 -right-1 inline-flex min-w-4 items-center justify-center rounded-full bg-(--primary-wMain) px-1 text-[10px] leading-4 font-semibold text-(--primary-text-w10)">
                                {count > 99 ? "99+" : count}
                            </span>
                        )}
                    </div>
                    {!collapsed && <span className={navTextStyles({ active: open })}>Notifications</span>}
                </button>
            </PopoverTrigger>
            <PopoverContent align="end" side="right" sideOffset={8} className="w-96 p-0">
                <div className="flex items-center justify-between border-b border-(--secondary-w40) px-4 py-3">
                    <span className="text-sm font-semibold">Notifications</span>
                    <button type="button" onClick={openSettings} className="cursor-pointer text-xs font-medium text-(--accent-text-wMain) hover:underline">
                        Settings
                    </button>
                </div>

                <div className="max-h-[60vh] overflow-y-auto">
                    {items.length === 0 ? (
                        <p className="px-4 py-8 text-center text-sm text-(--secondary-text-wMain)">You're all caught up.</p>
                    ) : (
                        groups.map(group => (
                            <section key={group.label}>
                                <h4 className="px-4 pt-3 pb-1 text-xs font-medium tracking-wide text-(--secondary-text-wMain) uppercase">{group.label}</h4>
                                {group.items.map(n => (
                                    <div key={n.id} className="relative border-b border-(--secondary-w20)">
                                        {n.status === "unread" && <UnreadIndicator />}
                                        <button type="button" onClick={() => openNotification(n.id)} className="flex w-full cursor-pointer flex-col gap-0.5 px-4 py-2.5 text-left hover:bg-(--secondary-w10)">
                                            <NotificationCategoryCaption n={n} categories={categories} />
                                            <span className={cn("text-sm", n.status === "unread" ? "font-semibold" : "font-normal text-(--secondary-text-wMain)")}>{n.title}</span>
                                            <span className="flex items-center gap-2">
                                                <span className="text-xs text-(--secondary-text-wMain)">{formatNotificationTime(n.createdAt)}</span>
                                                {isHilPending(n) && <span className="rounded bg-(--warning-w90) px-1.5 py-0.5 text-[10px] font-medium text-(--warning-text-wMain)">{NEEDS_RESPONSE_LABEL}</span>}
                                            </span>
                                        </button>
                                        <FlyoutActionCTA n={n} onOpen={openAction} />
                                    </div>
                                ))}
                            </section>
                        ))
                    )}
                </div>

                <div className="flex items-center justify-between border-t border-(--secondary-w40) px-4 py-2.5">
                    <button
                        type="button"
                        onClick={() => markAll.mutate()}
                        disabled={count === 0 || markAll.isPending}
                        className="cursor-pointer text-xs font-medium text-(--accent-text-wMain) hover:underline disabled:cursor-not-allowed disabled:text-(--secondary-text-wMain) disabled:no-underline"
                    >
                        Mark all as read
                    </button>
                    <button type="button" onClick={() => goToPage()} className="cursor-pointer text-xs font-medium text-(--accent-text-wMain) hover:underline">
                        See all
                    </button>
                </div>
            </PopoverContent>
        </Popover>
    );
}
