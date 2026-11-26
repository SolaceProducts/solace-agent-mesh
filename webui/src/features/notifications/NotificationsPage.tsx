// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, type Ref, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { BellOff, Check, ChevronDown, ChevronRight, Mail, MoreHorizontal, RefreshCw, Trash2 } from "lucide-react";
import { useNavigate, useOutletContext, useSearchParams } from "react-router-dom";

import type { NotificationItem } from "@/api/notifications";
import { useDeleteNotification, useMarkAllNotificationsRead, useMarkNotificationRead, useMarkNotificationUnread, useNotificationInbox, useSetNotificationCategoryPreference } from "@/api/notifications";

import { useChatContext } from "@/providers/contexts/ChatContext";
import { ConfigContext } from "@/providers/contexts/ConfigContext";

import { useIsNotificationsEnabled } from "@/hooks/useIsNotificationsEnabled";

import { Header, LinkifiedText, MarkdownHTMLConverter, PageLayout } from "@/common";
import {
    Badge,
    Button,
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
    LifecycleBadge,
    Pagination,
    PaginationContent,
    PaginationItem,
    PaginationLink,
    PaginationNext,
    PaginationPrevious,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
    Spinner,
    Switch,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/ui";
import { cn } from "@/utils";

import { HILInboxCard } from "./HILInboxCard";
import { HILRequestSummary } from "./HILRequestSummary";
import { isHilPending, isResolvedHil, resolvedHilLabel } from "./hilState";
import { actionLabel, navigableAction, type NotificationAction, useOpenNotificationAction } from "./notificationAction";
import { categoryOf, type NotificationCategories, turnOffableCategory, useNotificationCategories } from "./notificationCategory";
import { NEEDS_RESPONSE_LABEL, UnreadIndicator } from "./notificationRow";
import type { NotificationsOutletContext } from "./notificationsOutlet";
import { formatNotificationTime } from "./notificationTime";

const PAGE_SIZE = 25;

const ALL_CATEGORIES = "all";

const turnOffLabel = (label: string) => `Turn off “${label}” notifications`;
const turnedOffMessage = (label: string) => `“${label}” notifications are turned off. You can turn them back on in Notification Settings.`;
const turnOffFailedMessage = (label: string) => `Couldn't turn off “${label}” notifications. Try again from Notification Settings.`;

interface NotificationRowProps {
    n: NotificationItem;
    expanded: boolean;
    isFocus: boolean;
    focusRowRef: Ref<HTMLTableRowElement>;
    onToggleExpand: () => void;
    menuOpen: boolean;
    onMenuOpenChange: (id: string, open: boolean) => void;
    markRead: ReturnType<typeof useMarkNotificationRead>;
    markUnread: ReturnType<typeof useMarkNotificationUnread>;
    deleteOne: ReturnType<typeof useDeleteNotification>;
    onOpenAction: (n: NotificationItem, action: NotificationAction) => void;
    categories: NotificationCategories;
    turningOffCategory: boolean;
    onTurnOffCategory: (category: string, label: string) => void;
}

const CHAT_SHARE = "chat_share";

const openMenuIdAfterRowReport = (current: string | null, id: string, open: boolean) => {
    if (open) return id;
    return current === id ? null : current;
};

const NOTIFICATION_TABLE_CLASSES = "[&_th]:border-0! [&_th]:border-b! [&_th]:text-(--primary-text-wMain) [&_td]:border-0! [&_td]:border-b! [&_tr:last-child_td]:border-b-0!";

function NotificationBody({ n }: Readonly<{ n: NotificationItem }>) {
    if (n.category === CHAT_SHARE || n.source === CHAT_SHARE) {
        return <LinkifiedText text={n.body ?? ""} />;
    }
    return (
        <MarkdownHTMLConverter untrusted className={NOTIFICATION_TABLE_CLASSES}>
            {n.body}
        </MarkdownHTMLConverter>
    );
}

function NotificationDetails({ n }: Readonly<{ n: NotificationItem }>) {
    if (isHilPending(n)) {
        const chat = n.action?.sessionId;
        return chat ? <HILRequestSummary item={n} sessionId={chat} /> : <HILInboxCard item={n} hideTitle />;
    }
    const body = n.body?.trim() ? <NotificationBody n={n} /> : null;
    if (isResolvedHil(n)) {
        return (
            <div className="flex flex-col gap-1">
                {body}
                <span className="text-xs italic">{n.promptOutcome ? `This request is no longer awaiting input: ${resolvedHilLabel(n)}.` : "This request is no longer awaiting input — it expired or was already answered."}</span>
            </div>
        );
    }
    return body ?? <>No additional details.</>;
}

function NotificationRow({ n, expanded, isFocus, focusRowRef, onToggleExpand, menuOpen, onMenuOpenChange, markRead, markUnread, deleteOne, onOpenAction, categories, turningOffCategory, onTurnOffCategory }: Readonly<NotificationRowProps>) {
    const action = navigableAction(n);
    const turnOffable = turnOffableCategory(n, categories);
    useEffect(() => () => onMenuOpenChange(n.id, false), [n.id, onMenuOpenChange]);
    return (
        <>
            <TableRow ref={isFocus ? focusRowRef : undefined} className={n.status === "unread" ? "font-semibold" : undefined}>
                <TableCell className="relative w-8">
                    {n.status === "unread" && <UnreadIndicator />}
                    <Button variant="ghost" size="icon" className="size-7" aria-label={expanded ? "Collapse details" : "Expand details"} onClick={onToggleExpand}>
                        {expanded ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
                    </Button>
                </TableCell>
                <TableCell>
                    <span className="inline-flex items-center gap-2">
                        {n.title || "(untitled)"}
                        {isHilPending(n) && <Badge variant="outline">{NEEDS_RESPONSE_LABEL}</Badge>}
                        {isResolvedHil(n) && (
                            <Badge variant="secondary" className="text-(--secondary-text-wMain)">
                                {resolvedHilLabel(n)}
                            </Badge>
                        )}
                    </span>
                </TableCell>
                <TableCell className="font-normal">{categoryOf(n, categories)?.label}</TableCell>
                <TableCell className="whitespace-nowrap">{formatNotificationTime(n.createdAt)}</TableCell>
                <TableCell className="text-right whitespace-nowrap">
                    {action && (
                        <Button variant="ghost" size="sm" className="h-7" onClick={() => onOpenAction(n, action)}>
                            {actionLabel(action, n)}
                        </Button>
                    )}
                    <DropdownMenu modal={false} open={menuOpen} onOpenChange={open => onMenuOpenChange(n.id, open)}>
                        <DropdownMenuTrigger asChild>
                            <Button variant="ghost" size="icon" className="size-7" aria-label="Notification actions">
                                <MoreHorizontal className="size-4" />
                            </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                            {n.status === "unread" ? (
                                <DropdownMenuItem disabled={markRead.isPending} onClick={() => markRead.mutate(n.id)}>
                                    <Check className="mr-2 size-4" />
                                    Mark as read
                                </DropdownMenuItem>
                            ) : (
                                <DropdownMenuItem disabled={markUnread.isPending} onClick={() => markUnread.mutate(n.id)}>
                                    <Mail className="mr-2 size-4" />
                                    Mark as unread
                                </DropdownMenuItem>
                            )}
                            {turnOffable && (
                                <DropdownMenuItem disabled={turningOffCategory} onClick={() => onTurnOffCategory(turnOffable.category, turnOffable.label)}>
                                    <BellOff className="mr-2 size-4" />
                                    {turnOffLabel(turnOffable.label)}
                                </DropdownMenuItem>
                            )}
                            <DropdownMenuSeparator />
                            <DropdownMenuItem disabled={deleteOne.isPending} onClick={() => deleteOne.mutate(n.id)}>
                                <Trash2 className="mr-2 size-4" />
                                Delete
                            </DropdownMenuItem>
                        </DropdownMenuContent>
                    </DropdownMenu>
                </TableCell>
            </TableRow>
            {expanded && (
                <TableRow className="hover:bg-transparent">
                    <TableCell />
                    <TableCell colSpan={4} className="font-normal whitespace-normal text-(--secondary-text-wMain)">
                        <NotificationDetails n={n} />
                    </TableCell>
                </TableRow>
            )}
        </>
    );
}

interface InboxSectionProps {
    page: number;
    setPage: (page: number) => void;
    unreadOnly: boolean;
    setUnreadOnly: (unreadOnly: boolean) => void;
    category: string;
    setCategory: (category: string) => void;
    inbox: ReturnType<typeof useNotificationInbox>;
}

function InboxSection({ page, setPage, unreadOnly, setUnreadOnly, category, setCategory, inbox }: Readonly<InboxSectionProps>) {
    const [searchParams] = useSearchParams();
    const focusId = searchParams.get("focus");
    const [expandedId, setExpandedId] = useState<string | null>(null);
    const [openMenuId, setOpenMenuId] = useState<string | null>(null);
    const onMenuOpenChange = useCallback((id: string, open: boolean) => setOpenMenuId(current => openMenuIdAfterRowReport(current, id, open)), []);
    const { data, isLoading, isError } = inbox;
    const markRead = useMarkNotificationRead();
    const markUnread = useMarkNotificationUnread();
    const markAll = useMarkAllNotificationsRead();
    const deleteOne = useDeleteNotification();
    const openAction = useOpenNotificationAction();
    const categories = useNotificationCategories();
    const setCategoryPreference = useSetNotificationCategoryPreference();
    const { addNotification } = useChatContext();

    const turnOffCategory = (key: string, label: string) =>
        setCategoryPreference.mutate(
            { category: key, enabled: false },
            {
                onSuccess: () => addNotification(turnedOffMessage(label), "success"),
                onError: () => addNotification(turnOffFailedMessage(label), "warning"),
            }
        );

    const notifications = useMemo(() => data?.data ?? [], [data]);
    const pagination = data?.meta.pagination;
    const total = pagination?.count ?? 0;
    const totalPages = Math.max(1, pagination?.totalPages ?? 1);

    useEffect(() => {
        if (data && page > totalPages) setPage(totalPages);
    }, [data, page, totalPages, setPage]);

    const focusRowRef = useRef<HTMLTableRowElement>(null);
    const scrolledFor = useRef<string | null>(null);
    useEffect(() => {
        if (focusId) {
            setCategory("");
            setUnreadOnly(false);
            setPage(1);
            setExpandedId(focusId);
            scrolledFor.current = null;
        }
    }, [focusId, setPage, setCategory, setUnreadOnly]);
    useEffect(() => {
        if (focusId && focusId !== scrolledFor.current && focusRowRef.current) {
            focusRowRef.current.scrollIntoView({ behavior: "smooth", block: "center" });
            scrolledFor.current = focusId;
        }
    }, [focusId, notifications]);

    const autoReadFor = useRef<string | null>(null);
    useEffect(() => {
        if (!expandedId) {
            autoReadFor.current = null;
            return;
        }
        if (autoReadFor.current === expandedId) return;
        const n = notifications.find(x => x.id === expandedId);
        if (!n) return;
        autoReadFor.current = expandedId;
        if (n.status === "unread" && !isHilPending(n)) markRead.mutate(expandedId);
    }, [expandedId, notifications, markRead]);

    const onToggleUnread = (next: boolean) => {
        setUnreadOnly(next);
        setPage(1);
    };
    const onCategoryChange = (next: string) => {
        setCategory(next === ALL_CATEGORIES ? "" : next);
        setPage(1);
    };

    const rangeStart = total === 0 ? 0 : (page - 1) * PAGE_SIZE + 1;
    const rangeEnd = Math.min(page * PAGE_SIZE, total);

    let emptyMessage = "No notifications yet.";
    if (category) {
        emptyMessage = unreadOnly ? "No unread notifications of this type." : "No notifications of this type.";
    } else if (unreadOnly) {
        emptyMessage = "No unread notifications.";
    }

    let inboxBody: ReactNode;
    if (isLoading) {
        inboxBody = (
            <div className="flex items-center justify-center py-12">
                <Spinner size="large" variant="muted" />
            </div>
        );
    } else if (isError) {
        inboxBody = <p className="py-8 text-center text-sm text-(--error-text-wMain)">Failed to load notifications.</p>;
    } else if (notifications.length === 0) {
        inboxBody = <p className="py-8 text-center text-sm text-(--secondary-text-wMain)">{emptyMessage}</p>;
    } else {
        inboxBody = (
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead className="w-8" />
                        <TableHead>Title</TableHead>
                        <TableHead>Category</TableHead>
                        <TableHead>Time</TableHead>
                        <TableHead className="text-right" />
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {notifications.map(n => (
                        <NotificationRow
                            key={n.id}
                            n={n}
                            expanded={expandedId === n.id}
                            isFocus={n.id === focusId}
                            focusRowRef={focusRowRef}
                            onToggleExpand={() => setExpandedId(expandedId === n.id ? null : n.id)}
                            menuOpen={openMenuId === n.id}
                            onMenuOpenChange={onMenuOpenChange}
                            markRead={markRead}
                            markUnread={markUnread}
                            deleteOne={deleteOne}
                            onOpenAction={openAction}
                            categories={categories}
                            turningOffCategory={setCategoryPreference.isPending}
                            onTurnOffCategory={turnOffCategory}
                        />
                    ))}
                </TableBody>
            </Table>
        );
    }

    return (
        <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex flex-wrap items-center gap-6">
                    <label className="flex cursor-pointer items-center gap-3">
                        <Switch checked={unreadOnly} onCheckedChange={onToggleUnread} aria-label="Show only unread notifications" />
                        <span className="flex flex-col">
                            <span className="text-sm">Show only unread notifications.</span>
                            <span className="text-xs text-(--secondary-text-wMain)">{unreadOnly ? "Showing only unread notifications." : "Showing all of your notifications."}</span>
                        </span>
                    </label>
                    <Select value={category || ALL_CATEGORIES} onValueChange={onCategoryChange}>
                        <SelectTrigger className="w-64" aria-label="Category">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value={ALL_CATEGORIES}>All categories</SelectItem>
                            {[...categories.values()].map(c => (
                                <SelectItem key={c.category} value={c.category}>
                                    {c.label}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </div>
                <Button size="sm" onClick={() => markAll.mutate(category)} disabled={markAll.isPending}>
                    Mark all as read
                </Button>
            </div>

            {inboxBody}

            {total > 0 && (
                <div className="relative flex items-center justify-center pt-1">
                    {totalPages > 1 && (
                        <Pagination className="mx-0 w-auto">
                            <PaginationContent>
                                <PaginationItem>
                                    <PaginationPrevious className={cn("cursor-pointer", page <= 1 && "pointer-events-none opacity-50")} onClick={() => page > 1 && setPage(page - 1)} />
                                </PaginationItem>
                                {Array.from({ length: totalPages }, (_, i) => i + 1).map(p => (
                                    <PaginationItem key={p}>
                                        <PaginationLink className="cursor-pointer" isActive={p === page} onClick={() => setPage(p)}>
                                            {p}
                                        </PaginationLink>
                                    </PaginationItem>
                                ))}
                                <PaginationItem>
                                    <PaginationNext className={cn("cursor-pointer", page >= totalPages && "pointer-events-none opacity-50")} onClick={() => page < totalPages && setPage(page + 1)} />
                                </PaginationItem>
                            </PaginationContent>
                        </Pagination>
                    )}
                    <span className="absolute right-0 text-xs text-(--secondary-text-wMain)">
                        {rangeStart}-{rangeEnd} of {total} results
                    </span>
                </div>
            )}
        </div>
    );
}

export function NotificationsPage() {
    const navigate = useNavigate();
    const config = useContext(ConfigContext);
    const enabled = useIsNotificationsEnabled();
    const outlet = useOutletContext<NotificationsOutletContext | undefined>();
    const [page, setPage] = useState(1);
    const [unreadOnly, setUnreadOnly] = useState(false);
    const [category, setCategory] = useState("");
    const inbox = useNotificationInbox(page, PAGE_SIZE, unreadOnly, false, category);

    useEffect(() => {
        if (!enabled && config !== null) {
            navigate("/chat", { replace: true });
        }
    }, [enabled, config, navigate]);

    const buttons = [
        <Button key="refresh" variant="ghost" onClick={() => inbox.refetch()} disabled={inbox.isFetching}>
            <RefreshCw className={cn(inbox.isFetching && "animate-spin")} />
            Refresh
        </Button>,
    ];
    if (outlet?.openNotificationSettings) {
        buttons.push(
            <Button key="settings" variant="ghost" onClick={outlet.openNotificationSettings}>
                Notification Settings
            </Button>
        );
    }

    return (
        <PageLayout>
            <Header
                title={
                    <>
                        Notifications <LifecycleBadge variant="transparent">EARLY ACCESS</LifecycleBadge>
                    </>
                }
                buttons={buttons}
            />
            <div className="flex flex-1 flex-col overflow-y-auto px-4 py-6 sm:px-8">
                <InboxSection page={page} setPage={setPage} unreadOnly={unreadOnly} setUnreadOnly={setUnreadOnly} category={category} setCategory={setCategory} inbox={inbox} />
            </div>
        </PageLayout>
    );
}
