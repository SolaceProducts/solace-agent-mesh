// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Curve, type CurveProps, Rectangle, type RectangleProps, Text, type TextProps } from "recharts";

const AXIS_TEXT = "var(--secondary-text-wMain)";
const FUTURE_AXIS_TEXT = "var(--secondary-text-w50)";
const BAND_FILL = "var(--secondary-w10)";

interface TickPayload {
    value: unknown;
    index: number;
}

export function DayAxisTick({ payload, firstFutureIndex, tickFormatter, ...text }: Readonly<TextProps & { payload?: TickPayload; firstFutureIndex: number; tickFormatter?: (value: unknown, index: number) => string }>) {
    if (!payload) return null;
    return (
        <Text {...text} fontSize={11} fill={payload.index >= firstFutureIndex ? FUTURE_AXIS_TEXT : AXIS_TEXT}>
            {tickFormatter ? tickFormatter(payload.value, payload.index) : String(payload.value)}
        </Text>
    );
}

interface HoveredIndex {
    payloadIndex?: string | number;
    firstFutureIndex: number;
}

const isFuture = ({ payloadIndex, firstFutureIndex }: HoveredIndex) => Number(payloadIndex) >= firstFutureIndex;

export function DayBandCursor({ payloadIndex, firstFutureIndex, ...band }: Readonly<RectangleProps & HoveredIndex>) {
    return isFuture({ payloadIndex, firstFutureIndex }) ? null : <Rectangle {...band} fill={BAND_FILL} />;
}

export function DayLineCursor({ payloadIndex, firstFutureIndex, ...line }: Readonly<CurveProps & HoveredIndex>) {
    return isFuture({ payloadIndex, firstFutureIndex }) ? null : <Curve {...line} />;
}
