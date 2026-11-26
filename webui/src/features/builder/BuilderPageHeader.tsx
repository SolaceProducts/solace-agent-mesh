// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { Menu, MessageCircle, Plus, Workflow } from "lucide-react";

import { api } from "@/api/client";

import { useChatContext } from "@/providers/contexts";

import { Button } from "@/ui/button";
import { LifecycleBadge } from "@/ui/lifecycleBadge";

import { useIsMobile } from "@/hooks";

import type { CanvasViewToggle } from "./hooks/useBuilderSplitLayout";
import type { BuilderPageState } from "./useBuilderPageState";

interface BuilderPageHeaderProps {
    state: BuilderPageState;
    canvasViewToggle?: CanvasViewToggle | null;
}

export function BuilderPageHeader({ state, canvasViewToggle = null }: Readonly<BuilderPageHeaderProps>) {
    const { handleNewSession, sessionId } = useChatContext();
    const isMobile = useIsMobile();
    const [sessionName, setSessionName] = useState<string | null>(null);

    useEffect(() => {
        if (!sessionId) {
            setSessionName(null);
            return;
        }
        setSessionName(null);
        const controller = new AbortController();
        api.webui
            .get(`/api/v1/sessions/${sessionId}`, { signal: controller.signal })
            .then((res: unknown) => {
                if (controller.signal.aborted) return;
                const name = (res as { name?: string } | null)?.name;
                setSessionName(name && name.trim() ? name.trim() : null);
            })
            .catch((err: unknown) => {
                if (controller.signal.aborted) return;
                const status = (err as { status?: number } | null)?.status;
                if (status !== 404) {
                    console.warn("[BuilderPageHeader] session lookup failed", { sessionId, err });
                }
            });
        return () => controller.abort();
    }, [sessionId]);

    const buildName = state.rootName && state.rootName !== "New Build" ? state.rootName : sessionName && sessionName !== "New Chat" ? sessionName : "New Build";

    return (
        <div className="flex h-[80px] min-h-[80px] w-full min-w-0 items-center border-b px-3">
            <div className="flex shrink-0 items-center gap-1">
                {isMobile && (
                    <Button variant="ghost" size="icon" onClick={() => globalThis.dispatchEvent(new CustomEvent("open-mobile-nav"))} tooltip="Menu" className="h-8 w-8">
                        <Menu className="h-5 w-5" />
                    </Button>
                )}
                <Button variant="ghost" size="icon" onClick={() => handleNewSession()} tooltip="New build session" className="h-8 w-8">
                    <Plus className="h-4 w-4" />
                </Button>
            </div>

            <div className="ml-2 flex min-w-0 flex-1 items-center gap-2 md:flex-initial">
                <span className="min-w-0 truncate text-xl md:max-w-lg">{buildName}</span>
                <LifecycleBadge>EXPERIMENTAL</LifecycleBadge>
            </div>

            {canvasViewToggle && (
                <Button variant="outline" size="sm" onClick={canvasViewToggle.onToggle} className="ml-auto shrink-0 gap-2">
                    {canvasViewToggle.showingCanvas ? (
                        <>
                            <MessageCircle className="size-4" />
                            Back to chat
                        </>
                    ) : (
                        <>
                            <Workflow className="size-4" />
                            View diagram
                        </>
                    )}
                </Button>
            )}
        </div>
    );
}
