// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

interface ScrollState {
    isAtBottom: boolean;
    autoScrollEnabled: boolean;
}

interface UseAutoScrollOptions {
    offset?: number;
    smooth?: boolean;
    content?: React.ReactNode;
    autoScrollOnNewContent?: boolean;
    contentRef?: React.RefObject<HTMLElement | null>;
}

export function useAutoScroll(options: UseAutoScrollOptions = {}) {
    const { offset = 20, smooth = false, content, autoScrollOnNewContent = false, contentRef } = options;
    const scrollRef = useRef<HTMLDivElement>(null);
    const lastContentHeight = useRef(0);
    const userHasScrolled = useRef(false);
    const lastScrollTop = useRef(0);
    const recentUpwardScroll = useRef(false);
    const isProgrammaticScroll = useRef(false);
    const programmaticScrollCount = useRef(0);
    const unmountedRef = useRef(false);
    const autoScrollEnabledRef = useRef(true);
    const scrollToBottomRef = useRef<(instant?: boolean) => void>(() => {});

    const [scrollState, setScrollState] = useState<ScrollState>({
        isAtBottom: true,
        autoScrollEnabled: true,
    });

    useEffect(() => {
        autoScrollEnabledRef.current = scrollState.autoScrollEnabled;
    }, [scrollState.autoScrollEnabled]);

    useEffect(() => {
        unmountedRef.current = false;
        return () => {
            unmountedRef.current = true;
            programmaticScrollCount.current = 0;
            isProgrammaticScroll.current = false;
        };
    }, []);

    const checkIsAtBottom = useCallback(
        (element: HTMLElement) => {
            const { scrollTop, scrollHeight, clientHeight } = element;
            const distanceToBottom = Math.abs(scrollHeight - scrollTop - clientHeight);
            return distanceToBottom <= offset;
        },
        [offset]
    );

    const scrollToBottom = useCallback(
        (instant?: boolean) => {
            if (!scrollRef.current) return;

            isProgrammaticScroll.current = true;

            const targetScrollTop = scrollRef.current.scrollHeight - scrollRef.current.clientHeight;
            const maxScrollTop = () => (scrollRef.current ? scrollRef.current.scrollHeight - scrollRef.current.clientHeight : targetScrollTop);

            if (instant) {
                scrollRef.current.scrollTop = targetScrollTop;
            } else {
                scrollRef.current.scrollTo({
                    top: targetScrollTop,
                    behavior: smooth ? "smooth" : "auto",
                });
            }

            recentUpwardScroll.current = false;

            setScrollState({
                isAtBottom: true,
                autoScrollEnabled: true,
            });
            userHasScrolled.current = false;

            setTimeout(
                () => {
                    if (scrollRef.current) {
                        lastScrollTop.current = scrollRef.current.scrollTop;
                    }
                    isProgrammaticScroll.current = false;
                    const grewDuringScroll = maxScrollTop() - targetScrollTop > offset;
                    const mayFollow = autoScrollEnabledRef.current && !unmountedRef.current;
                    if (grewDuringScroll && mayFollow) scrollToBottomRef.current(true);
                },
                instant ? 50 : 500
            );
        },
        [smooth, offset]
    );
    useEffect(() => {
        scrollToBottomRef.current = scrollToBottom;
    }, [scrollToBottom]);

    const handleScroll = useCallback(() => {
        if (!scrollRef.current) return;

        if (isProgrammaticScroll.current) {
            return;
        }

        const currentScrollTop = scrollRef.current.scrollTop;
        const atBottom = checkIsAtBottom(scrollRef.current);

        const isScrollingUp = lastScrollTop.current > 0 && currentScrollTop < lastScrollTop.current;

        if (isScrollingUp) {
            recentUpwardScroll.current = true;
        }

        lastScrollTop.current = currentScrollTop;

        if (atBottom) {
            recentUpwardScroll.current = false;
            setScrollState(() => ({
                isAtBottom: true,
                autoScrollEnabled: true,
            }));
        } else if (recentUpwardScroll.current) {
            setScrollState(() => ({
                isAtBottom: false,
                autoScrollEnabled: false,
            }));
        } else {
            setScrollState(prev => ({
                ...prev,
                isAtBottom: false,
            }));
        }
    }, [checkIsAtBottom]);

    useEffect(() => {
        const element = scrollRef.current;
        if (!element) return;

        element.addEventListener("scroll", handleScroll, { passive: true });
        return () => element.removeEventListener("scroll", handleScroll);
    }, [handleScroll]);

    useEffect(() => {
        const scrollElement = scrollRef.current;
        if (!scrollElement) return;

        const currentHeight = scrollElement.scrollHeight;
        const hasNewContent = currentHeight !== lastContentHeight.current;

        if (hasNewContent) {
            if ((scrollState.autoScrollEnabled || autoScrollOnNewContent) && !isProgrammaticScroll.current) {
                requestAnimationFrame(() => {
                    scrollToBottom(lastContentHeight.current === 0);
                });
            }
            lastContentHeight.current = currentHeight;
        }
    }, [content, scrollState.autoScrollEnabled, scrollToBottom, autoScrollOnNewContent]);

    useEffect(() => {
        const element = contentRef?.current || scrollRef.current;
        if (!element) return;

        const resizeObserver = new ResizeObserver(() => {
            if (scrollState.autoScrollEnabled && !isProgrammaticScroll.current) {
                scrollToBottom(true);
            }
        });

        resizeObserver.observe(element);
        return () => resizeObserver.disconnect();
    }, [scrollState.autoScrollEnabled, scrollToBottom, contentRef]);

    const disableAutoScroll = useCallback(() => {
        const atBottom = scrollRef.current ? checkIsAtBottom(scrollRef.current) : false;

        if (!atBottom) {
            userHasScrolled.current = true;
            setScrollState(prev => ({
                ...prev,
                autoScrollEnabled: false,
            }));
        }
    }, [checkIsAtBottom]);

    const pauseAutoScroll = useCallback((): Promise<void> => {
        programmaticScrollCount.current += 1;
        isProgrammaticScroll.current = true;
        setScrollState({ isAtBottom: false, autoScrollEnabled: false });
        userHasScrolled.current = true;
        return new Promise<void>(resolve => {
            requestAnimationFrame(() => {
                requestAnimationFrame(() => {
                    if (unmountedRef.current) {
                        resolve();
                        return;
                    }
                    if (scrollRef.current) {
                        lastScrollTop.current = scrollRef.current.scrollTop;
                        lastContentHeight.current = scrollRef.current.scrollHeight;
                    }
                    programmaticScrollCount.current -= 1;
                    if (programmaticScrollCount.current <= 0) {
                        programmaticScrollCount.current = 0;
                        isProgrammaticScroll.current = false;
                    }
                    resolve();
                });
            });
        });
    }, []);

    return {
        scrollRef,
        isAtBottom: scrollState.isAtBottom,
        autoScrollEnabled: scrollState.autoScrollEnabled,
        scrollToBottom: () => scrollToBottom(),
        disableAutoScroll,
        pauseAutoScroll,
        userHasScrolled: userHasScrolled.current,
    };
}
