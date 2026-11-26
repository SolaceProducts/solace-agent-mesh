// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useState } from "react";
import { UserRound } from "lucide-react";

import { type ComponentAccessTarget, type ComponentAccessUser, useUsersWithComponentAccess } from "@/api/rbac";

import { Avatar, AvatarFallback } from "@/ui/avatar";
import { Button } from "@/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { SearchInput } from "@/ui/search-input";
import { Spinner } from "@/ui/spinner";

import { MessageBanner } from "@/common";
import { useDebounce } from "@/hooks";
import { getErrorMessage } from "@/utils";

import { SystemUserBadge } from "./SystemUserBadge";

const SEARCH_DEBOUNCE_MS = 300;

interface UserAccessDialogProps {
    readonly target: ComponentAccessTarget | null;
    readonly name: string;
    readonly onOpenChange: (open: boolean) => void;
}

export function UserAccessDialog({ target, name, onOpenChange }: Readonly<UserAccessDialogProps>) {
    const [search, setSearch] = useState("");
    const debouncedSearch = useDebounce(search, SEARCH_DEBOUNCE_MS);
    const isSearching = debouncedSearch.trim().length > 0;

    const { users, total, isLoading, isError, error, fetchNextPage, hasNextPage, isFetchingNextPage } = useUsersWithComponentAccess(target, { search: debouncedSearch });

    const handleOpenChange = (open: boolean) => {
        if (!open) setSearch("");
        onOpenChange(open);
    };

    let content: ReactNode;
    if (isError) {
        content = (
            <div className="mt-4">
                <MessageBanner variant="error" message={`Failed to load user access: ${getErrorMessage(error, "unknown error.")}`} />
            </div>
        );
    } else if (isLoading) {
        content = (
            <div className="flex flex-1 items-center justify-center">
                <Spinner size="large" />
            </div>
        );
    } else if (users.length === 0) {
        content = (
            <div className="my-4 rounded-sm bg-(--secondary-w10) p-8 text-center text-(--secondary-text-wMain)">{isSearching ? `No users match "${debouncedSearch}".` : "No known user has access. People who have never logged in are not listed."}</div>
        );
    } else {
        content = (
            <>
                <div className="flex justify-end text-(--secondary-text-wMain)">{`Users (${total})`}</div>
                <ul className="flex min-h-0 flex-1 flex-col overflow-y-auto border-t">
                    {users.map(user => (
                        <AccessUserRow key={user.id} user={user} />
                    ))}
                    {hasNextPage && (
                        <li>
                            <Button variant="ghost" className="w-full" onClick={() => fetchNextPage()} disabled={isFetchingNextPage}>
                                Load more
                            </Button>
                        </li>
                    )}
                </ul>
            </>
        );
    }

    return (
        <Dialog open={target !== null} onOpenChange={handleOpenChange}>
            <DialogContent className="flex max-h-[80vh] flex-col sm:max-w-3xl">
                <DialogHeader>
                    <DialogTitle className="truncate">User Access</DialogTitle>
                    <DialogDescription>
                        Users known to Solace Agent Mesh who can access <span className="font-semibold">{name}</span>. People who have never logged in are not listed. Access is granted through a user's roles, assigned by claim mapping or direct
                        association.
                    </DialogDescription>
                </DialogHeader>

                <div className="mt-4 flex h-80 flex-col">
                    <SearchInput value={search} onChange={setSearch} placeholder="Search by name or email" testid="userAccessSearch" />
                    {content}
                </div>

                <DialogFooter>
                    <DialogClose asChild>
                        <Button variant="outline">Close</Button>
                    </DialogClose>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

function AccessUserRow({ user }: Readonly<{ user: ComponentAccessUser }>) {
    const primary = user.displayName || user.userName || user.email || "Unknown user";
    const secondary = user.email && user.email !== primary ? user.email : undefined;
    return (
        <li className="flex items-center gap-3 border-x border-b p-2">
            <Avatar>
                <AvatarFallback>
                    <UserRound className="size-4 text-(--secondary-text-wMain)" />
                </AvatarFallback>
            </Avatar>
            <div className="flex min-w-0 flex-col">
                <div className="flex h-6 min-w-0 items-center gap-2">
                    <span className="truncate font-medium">{primary}</span>
                    {user.isSystemUser && <SystemUserBadge />}
                </div>
                {secondary ? <span className="truncate text-sm text-(--secondary-text-wMain)">{secondary}</span> : null}
            </div>
        </li>
    );
}
