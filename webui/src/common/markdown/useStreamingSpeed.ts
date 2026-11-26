// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef } from "react";

import * as StreamingConfig from "./streaming";

export interface StreamingState {
    cursor: number;
    speed: number;
    lastArrivalTime: number;
    avgInterval: number;
    lastLen: number;
}

export function useStreamingSpeed(content: string) {
    const state = useRef<StreamingState>({
        cursor: 0,
        speed: StreamingConfig.STREAMING_INITIAL_SPEED,
        lastArrivalTime: 0,
        avgInterval: StreamingConfig.STREAMING_INITIAL_AVG_INTERVAL,
        lastLen: 0,
    });

    const contentRef = useRef(content);

    useEffect(() => {
        contentRef.current = content;

        const now = Date.now();
        const s = state.current;
        const added = content.length - s.lastLen;

        if (added > 0) {
            if (s.lastArrivalTime === 0) {
                s.lastArrivalTime = now;
            } else {
                let dt = now - s.lastArrivalTime;

                dt = Math.max(StreamingConfig.STREAMING_DT_MIN_MS, Math.min(StreamingConfig.STREAMING_DT_MAX_MS, dt));

                const alpha = dt < StreamingConfig.STREAMING_ALPHA_THRESHOLD_MS ? StreamingConfig.STREAMING_ALPHA_FAST : StreamingConfig.STREAMING_ALPHA_SLOW;
                s.avgInterval = s.avgInterval * (1 - alpha) + dt * alpha;

                s.lastArrivalTime = now;
            }

            const backlog = content.length - s.cursor;
            const targetSpeed = backlog / (s.avgInterval * StreamingConfig.STREAMING_SAFETY_FACTOR);

            const momentum = targetSpeed > s.speed ? StreamingConfig.STREAMING_MOMENTUM_INCREASE : StreamingConfig.STREAMING_MOMENTUM_DECREASE;
            s.speed = s.speed * momentum + targetSpeed * (1 - momentum);

            s.speed = Math.max(StreamingConfig.STREAMING_SPEED_MIN, Math.min(StreamingConfig.STREAMING_SPEED_MAX, s.speed));

            s.lastLen = content.length;
        }
    }, [content]);

    return { state, contentRef };
}
