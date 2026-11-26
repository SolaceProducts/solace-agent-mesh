// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useLayoutEffect, useReducer, useRef } from "react";

import type { ChatMessageListRef } from "@/ui/chat/chat-message-list";

export const WAITING_DELAY_MS = 600;
export const SLIDE_OUT_DURATION_MS = 400;
export const FADE_OUT_DURATION_MS = 350;

interface AnimationState {
    isHistoryRevealed: boolean;
    isExitingHistory: boolean;
    isWaitingToExit: boolean;
}

type AnimationAction = { type: "DIVIDER_WAITING" } | { type: "DIVIDER_CHANGED" } | { type: "DIVIDER_REMOVED" } | { type: "SESSION_CHANGED" } | { type: "REVEAL_HISTORY" } | { type: "EXIT_COMPLETE" };

export function animationReducer(state: AnimationState, action: AnimationAction): AnimationState {
    switch (action.type) {
        case "DIVIDER_WAITING":
            return { isHistoryRevealed: false, isExitingHistory: false, isWaitingToExit: true };
        case "DIVIDER_CHANGED":
            return { isHistoryRevealed: false, isExitingHistory: true, isWaitingToExit: false };
        case "DIVIDER_REMOVED":
            return { ...state, isHistoryRevealed: false, isWaitingToExit: false };
        case "SESSION_CHANGED":
            return { isHistoryRevealed: false, isExitingHistory: false, isWaitingToExit: false };
        case "REVEAL_HISTORY":
            return { ...state, isHistoryRevealed: true };
        case "EXIT_COMPLETE":
            return { ...state, isExitingHistory: false };
        default:
            return state;
    }
}

interface UseTurnDividerAnimationOptions {
    turnDividerIndex: number | null;
    messagesLength: number;
    sessionId: string;
    chatMessageListRef: React.RefObject<ChatMessageListRef | null>;
}

export function useTurnDividerAnimation({ turnDividerIndex, messagesLength, sessionId, chatMessageListRef }: UseTurnDividerAnimationOptions) {
    const hasDivider = turnDividerIndex !== null && turnDividerIndex > 0 && turnDividerIndex < messagesLength;
    const [{ isHistoryRevealed, isExitingHistory, isWaitingToExit }, dispatch] = useReducer(animationReducer, {
        isHistoryRevealed: false,
        isExitingHistory: false,
        isWaitingToExit: false,
    });
    const newTurnAnchorRef = useRef<HTMLDivElement>(null);
    const lastDividerIndexRef = useRef<number | null>(null);
    const prevDividerIndexRef = useRef<number | null>(null);
    const oldScrollHeightRef = useRef<number | null>(null);
    const lastTouchYRef = useRef<number | null>(null);
    const prevSessionIdRef = useRef(sessionId);
    const needsScrollRef = useRef(false);

    const exitTimerRef = useRef<NodeJS.Timeout | null>(null);
    const exitDelayRef = useRef<NodeJS.Timeout | null>(null);
    const sessionJustSwitchedRef = useRef(false);

    const sessionChanged = sessionId !== prevSessionIdRef.current;
    const dividerJustAppeared = hasDivider && turnDividerIndex !== lastDividerIndexRef.current && !sessionChanged;
    const effectiveIsWaiting = isWaitingToExit || (dividerJustAppeared && !sessionJustSwitchedRef.current);

    const isHistoryCollapsed = hasDivider && !isHistoryRevealed;
    const isInWaitingPhase = effectiveIsWaiting;

    const collapsedUpToIndex = (effectiveIsWaiting || isExitingHistory) && prevDividerIndexRef.current !== null ? prevDividerIndexRef.current : turnDividerIndex;

    useLayoutEffect(() => {
        let dispatched = false;
        if (sessionId !== prevSessionIdRef.current) {
            prevSessionIdRef.current = sessionId;
            lastDividerIndexRef.current = hasDivider ? turnDividerIndex : null;
            dispatch({ type: "SESSION_CHANGED" });
            sessionJustSwitchedRef.current = !hasDivider;
            if (exitDelayRef.current) {
                clearTimeout(exitDelayRef.current);
                exitDelayRef.current = null;
            }
            dispatched = true;
        }
        if (!dispatched && hasDivider && turnDividerIndex !== lastDividerIndexRef.current) {
            prevDividerIndexRef.current = lastDividerIndexRef.current;
            lastDividerIndexRef.current = turnDividerIndex;
            if (sessionJustSwitchedRef.current) {
                sessionJustSwitchedRef.current = false;
                needsScrollRef.current = true;
            } else {
                dispatch({ type: "DIVIDER_WAITING" });
                if (exitDelayRef.current) clearTimeout(exitDelayRef.current);
                exitDelayRef.current = setTimeout(() => {
                    dispatch({ type: "DIVIDER_CHANGED" });
                    exitTimerRef.current = setTimeout(() => {
                        dispatch({ type: "EXIT_COMPLETE" });
                        chatMessageListRef.current?.pauseAutoScroll();
                        requestAnimationFrame(() => {
                            const container = chatMessageListRef.current?.scrollContainer;
                            const anchor = newTurnAnchorRef.current;
                            if (container && anchor) {
                                const prev = container.style.scrollBehavior;
                                container.style.scrollBehavior = "auto";
                                anchor.scrollIntoView({ block: "start" });
                                container.style.scrollBehavior = prev;
                            }
                            chatMessageListRef.current?.pauseAutoScroll();
                        });
                    }, SLIDE_OUT_DURATION_MS);
                }, WAITING_DELAY_MS);
            }
        }
        if (!dispatched && !hasDivider && lastDividerIndexRef.current !== null) {
            lastDividerIndexRef.current = null;
            dispatch({ type: "DIVIDER_REMOVED" });
        }
        return () => {
            if (exitDelayRef.current) {
                clearTimeout(exitDelayRef.current);
                exitDelayRef.current = null;
            }
        };
    }, [sessionId, turnDividerIndex, hasDivider]);

    useEffect(() => {
        const container = chatMessageListRef.current?.scrollContainer;
        if (!container) return;

        const revealHistory = () => {
            oldScrollHeightRef.current = container.scrollHeight;
            chatMessageListRef.current?.pauseAutoScroll();
            dispatch({ type: "REVEAL_HISTORY" });
        };

        const handleWheel = (e: WheelEvent) => {
            if (e.deltaY < 0 && container.scrollTop <= 0 && isHistoryCollapsed) {
                revealHistory();
            }
        };

        const handleTouchStart = (e: TouchEvent) => {
            lastTouchYRef.current = e.touches[0]?.clientY ?? null;
        };

        const handleTouchMove = (e: TouchEvent) => {
            if (lastTouchYRef.current === null) return;
            const currentY = e.touches[0]?.clientY ?? 0;
            if (currentY > lastTouchYRef.current && container.scrollTop <= 0 && isHistoryCollapsed) {
                revealHistory();
            }
            lastTouchYRef.current = currentY;
        };

        const handleKeyDown = (e: KeyboardEvent) => {
            const isScrollUpKey = e.key === "PageUp" || e.key === "Home";
            if (isScrollUpKey && container.scrollTop <= 0 && isHistoryCollapsed) {
                revealHistory();
            }
        };

        container.addEventListener("wheel", handleWheel, { passive: true });
        container.addEventListener("touchstart", handleTouchStart, { passive: true });
        container.addEventListener("touchmove", handleTouchMove, { passive: true });
        container.addEventListener("keydown", handleKeyDown);
        return () => {
            container.removeEventListener("wheel", handleWheel);
            container.removeEventListener("touchstart", handleTouchStart);
            container.removeEventListener("touchmove", handleTouchMove);
            container.removeEventListener("keydown", handleKeyDown);
        };
    }, [isHistoryCollapsed, chatMessageListRef]);

    useLayoutEffect(() => {
        if (isHistoryRevealed && oldScrollHeightRef.current !== null) {
            const container = chatMessageListRef.current?.scrollContainer;
            if (container) {
                const addedHeight = container.scrollHeight - oldScrollHeightRef.current;
                if (addedHeight > 0) {
                    const prev = container.style.scrollBehavior;
                    container.style.scrollBehavior = "auto";
                    container.scrollTop = addedHeight;
                    container.style.scrollBehavior = prev;
                    requestAnimationFrame(() => {
                        container.scrollTo({ top: Math.max(0, addedHeight - 300), behavior: "smooth" });
                    });
                }
            }
            oldScrollHeightRef.current = null;
        }
    }, [isHistoryRevealed, chatMessageListRef]);

    const scrollToAnchor = useCallback(() => {
        const container = chatMessageListRef.current?.scrollContainer;
        const anchor = newTurnAnchorRef.current;
        if (container && anchor) {
            const prev = container.style.scrollBehavior;
            container.style.scrollBehavior = "auto";
            anchor.scrollIntoView({ block: "start" });
            container.style.scrollBehavior = prev;
        }
    }, [chatMessageListRef]);

    useLayoutEffect(() => {
        if (needsScrollRef.current && isHistoryCollapsed && newTurnAnchorRef.current) {
            needsScrollRef.current = false;
            chatMessageListRef.current?.pauseAutoScroll();
            scrollToAnchor();
            requestAnimationFrame(() => {
                chatMessageListRef.current?.pauseAutoScroll();
                scrollToAnchor();
                requestAnimationFrame(() => {
                    chatMessageListRef.current?.pauseAutoScroll();
                    scrollToAnchor();
                });
            });
        }
    });

    return {
        hasDivider,
        isHistoryCollapsed,
        isExitingHistory,
        isHistoryRevealed,
        isInWaitingPhase,
        newTurnAnchorRef,
        collapsedUpToIndex,
    };
}
