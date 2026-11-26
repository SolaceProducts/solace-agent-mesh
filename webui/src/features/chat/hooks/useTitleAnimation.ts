// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef, useState } from "react";

export const useTitleAnimation = (finalText: string, sessionId?: string): { text: string; isAnimating: boolean; isGenerating: boolean } => {
    const [displayedText, setDisplayedText] = useState(finalText);
    const [isAnimating, setIsAnimating] = useState(false);
    const [isGenerating, setIsGenerating] = useState(false);
    const previousTextRef = useRef(finalText);
    const previousSessionIdRef = useRef(sessionId);
    const wasGeneratingRef = useRef(false);

    useEffect(() => {
        if (!sessionId) return;

        const handleTitleGenerating = (event: Event) => {
            const customEvent = event as CustomEvent;
            const { sessionId: eventSessionId, isGenerating: generating } = customEvent.detail;

            if (eventSessionId === sessionId) {
                setIsGenerating(generating);
                if (generating) {
                    wasGeneratingRef.current = true;
                }
            }
        };

        window.addEventListener("session-title-generating", handleTitleGenerating);
        return () => {
            window.removeEventListener("session-title-generating", handleTitleGenerating);
        };
    }, [sessionId]);

    useEffect(() => {
        const sessionChanged = sessionId !== previousSessionIdRef.current;
        const textChanged = finalText !== previousTextRef.current;

        previousSessionIdRef.current = sessionId;

        if (sessionChanged) {
            previousTextRef.current = finalText;
            setDisplayedText(finalText);
            setIsAnimating(false);
            setIsGenerating(false);
            wasGeneratingRef.current = false;
            return;
        }

        if (!textChanged) {
            return;
        }

        const shouldAnimate = wasGeneratingRef.current;

        if (isGenerating) {
            setIsGenerating(false);
        }

        if (!finalText) {
            previousTextRef.current = finalText;
            setDisplayedText("");
            setIsAnimating(false);
            wasGeneratingRef.current = false;
            return;
        }

        if (shouldAnimate) {
            setIsAnimating(true);

            const timer = setTimeout(() => {
                previousTextRef.current = finalText;
                setDisplayedText(finalText);
                setTimeout(() => {
                    setIsAnimating(false);
                    wasGeneratingRef.current = false;
                }, 300);
            }, 200);

            return () => clearTimeout(timer);
        } else {
            previousTextRef.current = finalText;
            setDisplayedText(finalText);
        }
    }, [finalText, sessionId, isGenerating]);

    return { text: displayedText, isAnimating, isGenerating };
};
