// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, ExternalLink, Link2, MoreVertical, RefreshCw } from "lucide-react";

import { useAddShareUsers, useDeleteShareUsers, useShareUsers, useUpdateShareSnapshot } from "@/api/share";

import { useConfigContext } from "@/providers/contexts";

import { type AccessLevelOption, ShareResourceDialog, type ShareResourceEntry, type ShareRowColumn } from "@/common/dialogs/ShareResourceDialog";

import { formatDateYMD } from "@/utils/format";

import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui/dropdown-menu";
import { Spinner } from "@/ui/spinner";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import { api } from "@/api";
import { cn } from "@/utils";

import { ChatPublicLinkSection } from "./ChatPublicLinkSection";
import { useChatShareLink } from "./useChatShareLink";

type ChatAccessLevel = "resource_viewer";

const CHAT_ACCESS_LEVELS: AccessLevelOption<ChatAccessLevel>[] = [{ value: "resource_viewer", label: "Viewer" }];

interface ShareChatDialogProps {
    sessionId: string;
    sessionTitle: string;
    sessionUpdatedTime?: string;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onError?: (error: { title: string; message: string }) => void;
    onSuccess?: (message: string) => void;
    defaultShowAddRow?: boolean;
    defaultShowPublicLink?: boolean;
}

export function ShareChatDialog({ sessionId, sessionTitle, sessionUpdatedTime, open, onOpenChange, onError, onSuccess, defaultShowAddRow = false, defaultShowPublicLink = false }: Readonly<ShareChatDialogProps>) {
    const { identityServiceType } = useConfigContext();
    const link = useChatShareLink({ sessionId, isOpen: open, defaultShowPublicLink, onError, onSuccess });

    const shareUsersQuery = useShareUsers(link.shareLink?.id);
    const addShareUsers = useAddShareUsers();
    const deleteShareUsers = useDeleteShareUsers();
    const updateSnapshot = useUpdateShareSnapshot();

    const sharedUsers = useMemo(() => shareUsersQuery.data?.users ?? [], [shareUsersQuery.data?.users]);

    const sessionDetailQuery = useQuery({
        queryKey: ["sessions", "detail", sessionId],
        queryFn: () => api.webui.get(`/api/v1/sessions/${sessionId}`),
        enabled: open && !!sessionId,
    });
    const sessionData = sessionDetailQuery.data as { updatedAt?: string; ownerEmail?: string | null; userId?: string | null } | undefined;
    const sessionUpdatedAtIso: string | null = sessionData?.updatedAt ?? null;
    const ownerEmail = sessionData?.ownerEmail ?? sessionData?.userId ?? shareUsersQuery.data?.ownerEmail ?? "";
    const effectiveSessionIso = sessionUpdatedAtIso ?? sessionUpdatedTime ?? null;
    const sessionLastUpdateMs: number | null = effectiveSessionIso ? new Date(effectiveSessionIso).getTime() : null;

    const shares: ShareResourceEntry<ChatAccessLevel>[] = useMemo(() => sharedUsers.map(u => ({ id: u.userEmail, userEmail: u.userEmail, accessLevel: "resource_viewer" })), [sharedUsers]);

    const userByEmail = useMemo(() => new Map(sharedUsers.map(u => [u.userEmail, u])), [sharedUsers]);

    const handleUpdateUserSnapshot = useCallback(
        async (userEmail: string) => {
            if (!link.shareLink?.id) return;
            try {
                await updateSnapshot.mutateAsync({ shareId: link.shareLink.id, userEmail });
                onSuccess?.(`Snapshot updated for ${userEmail}`);
            } catch (error) {
                onError?.({ title: "Failed to Update Snapshot", message: error instanceof Error ? error.message : "Unknown error" });
            }
        },
        [link.shareLink, updateSnapshot, onError, onSuccess]
    );

    const handleClose = useCallback(() => {
        link.discardIfNew().finally(() => onOpenChange(false));
    }, [link, onOpenChange]);

    const handleCreate = useCallback(
        async (entries: Array<{ userEmail: string; accessLevel: ChatAccessLevel }>) => {
            const activeLink = await link.ensureLinkForInvite();
            if (!activeLink?.id) throw new Error("Could not create share link");
            await addShareUsers.mutateAsync({
                shareId: activeLink.id,
                data: { shares: entries.map(e => ({ userEmail: e.userEmail, accessLevel: e.accessLevel })) },
            });
            link.clearNewlyCreated();
            const userText = entries.length === 1 ? "user" : "users";
            onSuccess?.(`${entries.length} ${userText} added`);
        },
        [link, addShareUsers, onSuccess]
    );

    const handleReplace = useCallback(
        async (desired: Array<{ userEmail: string; accessLevel: ChatAccessLevel }>) => {
            const activeLink = await link.ensureLinkForInvite();
            if (!activeLink?.id) throw new Error("Could not create share link");
            const desiredByEmail = new Map(desired.map(e => [e.userEmail, e.accessLevel]));
            const toRemove = sharedUsers.filter(u => !desiredByEmail.has(u.userEmail)).map(u => u.userEmail);
            const toAddOrUpdate = desired.filter(e => userByEmail.get(e.userEmail)?.accessLevel !== e.accessLevel);

            if (toAddOrUpdate.length > 0) {
                await addShareUsers.mutateAsync({
                    shareId: activeLink.id,
                    data: { shares: toAddOrUpdate.map(e => ({ userEmail: e.userEmail, accessLevel: e.accessLevel })) },
                });
            }
            if (toRemove.length > 0) {
                await deleteShareUsers.mutateAsync({
                    shareId: activeLink.id,
                    data: { userEmails: toRemove },
                });
            }
            link.clearNewlyCreated();
        },
        [link, sharedUsers, userByEmail, addShareUsers, deleteShareUsers]
    );

    const extraColumns = useMemo<ShareRowColumn<ChatAccessLevel>[]>(
        () => [
            {
                key: "shared-on",
                position: "before-access",
                header: "Shared On",
                width: "200px",
                render: entry => {
                    const user = userByEmail.get(entry.userEmail);
                    if (!user) return null;
                    const formattedDate = formatDateYMD(user.addedAt);
                    const isSnapshotOutdated = sessionLastUpdateMs !== null && sessionLastUpdateMs > (Date.parse(user.addedAt) || 0);
                    const isUpdatingThisUser = updateSnapshot.isPending && updateSnapshot.variables?.userEmail === user.userEmail;
                    return (
                        <div className="flex items-center gap-2">
                            <span className="text-sm whitespace-nowrap text-(--secondary-text-wMain)">{formattedDate}</span>
                            {isSnapshotOutdated && (
                                <Tooltip>
                                    <TooltipTrigger asChild>
                                        <Button variant="ghost" size="icon" className="h-6 w-6 shrink-0" aria-label="Update snapshot" disabled={updateSnapshot.isPending} onClick={() => handleUpdateUserSnapshot(user.userEmail)}>
                                            <RefreshCw className={cn("h-3.5 w-3.5", isUpdatingThisUser && "animate-spin")} />
                                        </Button>
                                    </TooltipTrigger>
                                    <TooltipContent>
                                        <p>Update Snapshot</p>
                                    </TooltipContent>
                                </Tooltip>
                            )}
                        </div>
                    );
                },
            },
            {
                key: "row-actions",
                position: "after-access",
                header: "",
                width: "32px",
                render: () => {
                    const shareUrl = link.shareLink?.shareUrl;
                    if (!shareUrl) return null;
                    return (
                        <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                                <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0" aria-label="User actions">
                                    <MoreVertical className="h-4 w-4" />
                                </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                                <DropdownMenuItem onClick={() => window.open(shareUrl, "_blank")}>
                                    <ExternalLink className="mr-2 h-4 w-4" />
                                    Preview Chat
                                </DropdownMenuItem>
                            </DropdownMenuContent>
                        </DropdownMenu>
                    );
                },
            },
        ],
        [userByEmail, sessionLastUpdateMs, updateSnapshot.isPending, updateSnapshot.variables?.userEmail, handleUpdateUserSnapshot, link.shareLink?.shareUrl]
    );

    const publicLinkSectionVisible = link.showPublicLink && link.shareLink != null;

    const belowSharesSlot =
        publicLinkSectionVisible && link.shareLink ? (
            <ChatPublicLinkSection shareLink={link.shareLink} publicLinkCopied={link.publicLinkCopied} onCopy={link.copyOrCreatePublicLink} onDelete={link.deletePublicLink} onError={onError} onSuccess={onSuccess} />
        ) : null;

    const renderCopyButtonContent = () => {
        if (link.footerLinkCopied) {
            return (
                <>
                    <Check className="mr-2 h-4 w-4 text-(--success-wMain)" />
                    Copied!
                </>
            );
        }
        if (link.isCreatingLink) {
            return (
                <>
                    <Spinner size="small" variant="muted" className="mr-2" />
                    Creating Link...
                </>
            );
        }
        return (
            <>
                <Link2 className="mr-2 h-4 w-4" />
                Copy Sharing Link
            </>
        );
    };

    const footerStartSlot = publicLinkSectionVisible ? null : (
        <Button variant="ghost" size="sm" onClick={link.copyOrCreatePublicLink} disabled={link.isCreatingLink}>
            {renderCopyButtonContent()}
        </Button>
    );

    return (
        <ShareResourceDialog<ChatAccessLevel>
            isOpen={open}
            onClose={handleClose}
            resourceLabel="Chat"
            resourceName={sessionTitle}
            ownerEmail={ownerEmail || undefined}
            shares={shares}
            isLoading={shareUsersQuery.isLoading || link.isLoading}
            identityServiceType={identityServiceType}
            accessLevels={CHAT_ACCESS_LEVELS}
            onCreate={handleCreate}
            onReplace={handleReplace}
            extraHasChanges={link.isNewlyCreatedLink}
            onSave={link.clearNewlyCreated}
            description={
                <>
                    Share <strong>{sessionTitle}</strong> with other users. Users will see a snapshot of the shared chat, including files and conversation history.
                </>
            }
            extraColumns={extraColumns}
            belowSharesSlot={belowSharesSlot}
            footerStartSlot={footerStartSlot}
            defaultOpenAddRow={defaultShowAddRow}
        />
    );
}
