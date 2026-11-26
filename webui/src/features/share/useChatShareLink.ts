// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { useCreateShareLink, useDeleteShareLink, useShareLink } from "@/api/share";

import { copyDeferredToClipboard, copyToClipboard } from "@/utils/clipboard";

import type { ShareLink } from "@/types/share";

interface UseChatShareLinkOptions {
    sessionId: string;
    isOpen: boolean;
    defaultShowPublicLink?: boolean;
    onError?: (error: { title: string; message: string }) => void;
    onSuccess?: (message: string) => void;
}

interface UseChatShareLinkResult {
    shareLink: ShareLink | null;
    isLoading: boolean;
    isNewlyCreatedLink: boolean;
    isCreatingLink: boolean;
    publicLinkCopied: boolean;
    footerLinkCopied: boolean;
    showPublicLink: boolean;
    setShowPublicLink: (open: boolean) => void;
    copyOrCreatePublicLink: () => Promise<void>;
    deletePublicLink: () => Promise<void>;
    ensureLinkForInvite: () => Promise<ShareLink | null>;
    discardIfNew: () => Promise<void>;
    clearNewlyCreated: () => void;
}

export function useChatShareLink({ sessionId, isOpen, defaultShowPublicLink = false, onError, onSuccess }: UseChatShareLinkOptions): UseChatShareLinkResult {
    const shareLinkQuery = useShareLink(sessionId);
    const createLink = useCreateShareLink();
    const deleteLink = useDeleteShareLink();

    const shareLink = shareLinkQuery.data ?? null;
    const isLoading = shareLinkQuery.isLoading;

    const [isNewlyCreatedLink, setIsNewlyCreatedLink] = useState(false);
    const [isCreatingLink, setIsCreatingLink] = useState(false);
    const [publicLinkCopied, setPublicLinkCopied] = useState(false);
    const [footerLinkCopied, setFooterLinkCopied] = useState(false);
    const [showPublicLink, setShowPublicLink] = useState(defaultShowPublicLink);

    const newlyCreatedIdRef = useRef<string | null>(null);
    const copiedTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    useEffect(() => {
        return () => {
            if (copiedTimerRef.current) clearTimeout(copiedTimerRef.current);
        };
    }, []);

    const shareLinkRefetch = shareLinkQuery.refetch;
    useEffect(() => {
        if (!isOpen) return;
        setIsNewlyCreatedLink(false);
        setShowPublicLink(defaultShowPublicLink);
        setFooterLinkCopied(false);
        setPublicLinkCopied(false);
        setIsCreatingLink(false);
        newlyCreatedIdRef.current = null;
        if (copiedTimerRef.current) {
            clearTimeout(copiedTimerRef.current);
            copiedTimerRef.current = null;
        }
        shareLinkRefetch();
    }, [isOpen, defaultShowPublicLink, shareLinkRefetch]);

    useEffect(() => {
        if (isOpen && shareLinkQuery.data && !isNewlyCreatedLink) {
            setShowPublicLink(true);
        }
    }, [isOpen, shareLinkQuery.data, isNewlyCreatedLink]);

    const copyOrCreatePublicLink = useCallback(async () => {
        if (shareLink) {
            const success = await copyToClipboard(shareLink.shareUrl);
            if (success) {
                setPublicLinkCopied(true);
                if (copiedTimerRef.current) clearTimeout(copiedTimerRef.current);
                copiedTimerRef.current = setTimeout(() => {
                    copiedTimerRef.current = null;
                    setPublicLinkCopied(false);
                }, 2000);
                setShowPublicLink(true);
                onSuccess?.("Link copied to clipboard");
            }
            return;
        }

        setIsCreatingLink(true);
        const linkPromise = createLink.mutateAsync({ sessionId, options: { requireAuthentication: false } });
        const clipboardOk = copyDeferredToClipboard(linkPromise.then(link => link.shareUrl));
        try {
            const newLink = await linkPromise;
            newlyCreatedIdRef.current = newLink.id;
            setIsNewlyCreatedLink(true);
            const success = await clipboardOk;
            if (success) {
                setFooterLinkCopied(true);
                setPublicLinkCopied(true);
                onSuccess?.("Public link created and copied to clipboard");
                copiedTimerRef.current = setTimeout(() => {
                    copiedTimerRef.current = null;
                    setFooterLinkCopied(false);
                    setPublicLinkCopied(false);
                    setShowPublicLink(true);
                }, 1500);
            } else {
                setShowPublicLink(true);
                onSuccess?.("Public link created");
            }
        } catch (error) {
            onError?.({ title: "Failed to Create Public Link", message: error instanceof Error ? error.message : "Unknown error" });
        } finally {
            setIsCreatingLink(false);
        }
    }, [shareLink, createLink, sessionId, onError, onSuccess]);

    const deletePublicLink = useCallback(async () => {
        if (shareLink?.id) {
            try {
                await deleteLink.mutateAsync(shareLink.id);
                setIsNewlyCreatedLink(false);
                newlyCreatedIdRef.current = null;
            } catch (error) {
                onError?.({ title: "Failed to Delete Link", message: error instanceof Error ? error.message : "Unknown error" });
                return;
            }
        }
        setShowPublicLink(false);
        onSuccess?.("Link removed");
    }, [shareLink, deleteLink, onError, onSuccess]);

    const ensureLinkForInvite = useCallback(async (): Promise<ShareLink | null> => {
        if (shareLink?.id) return shareLink;
        try {
            const newLink = await createLink.mutateAsync({ sessionId, options: { requireAuthentication: true } });
            newlyCreatedIdRef.current = newLink.id;
            return newLink;
        } catch (error) {
            onError?.({ title: "Failed to Save", message: error instanceof Error ? error.message : "Could not create share link" });
            return null;
        }
    }, [shareLink, createLink, sessionId, onError]);

    const discardIfNew = useCallback(async () => {
        if (copiedTimerRef.current) {
            clearTimeout(copiedTimerRef.current);
            copiedTimerRef.current = null;
        }
        const shareIdToDelete = newlyCreatedIdRef.current;
        if (!shareIdToDelete) return;
        try {
            await deleteLink.mutateAsync(shareIdToDelete);
            setIsNewlyCreatedLink(false);
            setShowPublicLink(false);
            newlyCreatedIdRef.current = null;
            onSuccess?.("Share link removed");
        } catch (error) {
            onError?.({ title: "Failed to Delete Link", message: error instanceof Error ? error.message : "Unknown error" });
        }
    }, [deleteLink, onError, onSuccess]);

    const clearNewlyCreated = useCallback(() => {
        setIsNewlyCreatedLink(false);
        newlyCreatedIdRef.current = null;
    }, []);

    return {
        shareLink,
        isLoading,
        isNewlyCreatedLink,
        isCreatingLink,
        publicLinkCopied,
        footerLinkCopied,
        showPublicLink,
        setShowPublicLink,
        copyOrCreatePublicLink,
        deletePublicLink,
        ensureLinkForInvite,
        discardIfNew,
        clearNewlyCreated,
    };
}
