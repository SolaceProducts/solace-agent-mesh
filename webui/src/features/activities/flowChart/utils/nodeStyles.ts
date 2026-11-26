// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const ACTIVITY_NODE_BASE_STYLES = {
    RECTANGULAR: "card-surface card-surface-hover group relative flex cursor-pointer items-center justify-between rounded bg-(--background-w10) px-4 py-2 transition-all duration-200 ease-in-out",

    RECTANGULAR_COMPACT: "card-surface card-surface-hover group relative flex cursor-pointer items-center justify-center rounded bg-(--background-w10) px-3 py-2 transition-all duration-200",

    PILL: "card-surface flex items-center justify-center gap-2 rounded-full bg-(--primary-w10) px-4 py-2",

    CONTAINER_HEADER: "card-surface card-surface-hover group relative mx-auto w-fit cursor-pointer rounded bg-(--background-w10) transition-all duration-200",
} as const;

export const ACTIVITY_NODE_SELECTED_CLASS = "!border-(--accent-n2-wMain)";

export const ACTIVITY_NODE_PROCESSING_CLASS = "processing-halo";

export const ACTIVITY_NODE_ERROR_CLASS = "border border-(--error-wMain)";

export const ACTIVITY_NODE_SKIPPED_CLASS = "border border-dashed opacity-50";

export const CONNECTOR_LINE_CLASSES = "bg-(--stateLayer-w20)";

export const CONNECTOR_STROKE_CLASSES = "stroke-(--stateLayer-w20)";

export const CONNECTOR_SIZES = {
    MAIN: "h-4 w-0.5",
    BRANCH: "h-3 w-0.5",
} as const;

export const CONTAINER_CHILDREN_CLASSES = "rounded border border-dashed bg-(--stateLayer-w10)";

export const ACTIVITY_NODE_LAYOUT = {
    HEADER_HEIGHT: 44,
    CONTAINER_WIDTH: 280,
    LEAF_NODE_MIN_HEIGHT: 50,
} as const;

export const PARALLEL_WRAP_COLUMNS = 4;
