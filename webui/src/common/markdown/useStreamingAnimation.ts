// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type MutableRefObject, useEffect, useState } from "react";

import * as StreamingConfig from "./streaming";
import type { StreamingState } from "./useStreamingSpeed";

export function useStreamingAnimation(state: MutableRefObject<StreamingState>, contentRef: MutableRefObject<string>): string {
    const [displayedContent, setDisplayedContent] = useState("");

    useEffect(() => {
        let animationFrameId: number;
        let lastFrameTime = Date.now();
        let lastEmitTime = 0;

        const animate = () => {
            const now = Date.now();
            const dt = now - lastFrameTime;
            lastFrameTime = now;

            const s = state.current;
            const target = contentRef.current;

            if (target.length < s.cursor) {
                s.cursor = target.length;
            }

            const backlog = target.length - s.cursor;

            if (backlog > 0) {
                s.cursor += s.speed * dt;

                if (s.cursor > target.length) {
                    s.cursor = target.length;
                }

                if (now - lastEmitTime >= StreamingConfig.STREAMING_EMIT_INTERVAL_MS) {
                    lastEmitTime = now;
                    setDisplayedContent(target.slice(0, Math.floor(s.cursor)));
                }
            } else if (target.length > 0) {
                setDisplayedContent(prev => (prev !== target ? target : prev));
            }

            animationFrameId = requestAnimationFrame(animate);
        };

        animationFrameId = requestAnimationFrame(animate);

        return () => {
            cancelAnimationFrame(animationFrameId);
        };
    }, [state, contentRef]);

    return displayedContent;
}
