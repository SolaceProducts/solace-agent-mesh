// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface A2UIComponent {
    id: string;
    component: string;
    contentType?: string;
    [key: string]: unknown;
}

export interface A2UISurface {
    surfaceId: string;
    catalogId: string;
    components: A2UIComponent[];
    dataModel: Record<string, unknown>;
}

export const LayoutType = {
    HIERARCHICAL: "hierarchical",
    CARDS: "cards",
    TABLE: "table",
} as const;

export type LayoutType = (typeof LayoutType)[keyof typeof LayoutType];

export interface LayoutConfig {
    type: string | LayoutType;
    spacing: {
        horizontal: number;
        vertical: number;
    };
    viewport: {
        width: number;
        height: number;
    };
    padding: number;
}
