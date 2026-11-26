// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Toolset, ToolsetConfigResponse } from "@/types";

import type { CreateToolsetRequest, ToolsetBuildTarget, UpdateToolsetRequest } from "./service";
import { createToolsetV2, deleteToolsetV2, downloadToolsetBundleV2, getBuiltinToolsetsV2, getCustomToolsetsV2, getToolsetBuildTargetV2, getToolsetConfigV2, patchToolsetConfigV2, updateToolsetV2, uploadToolsetBundleV2 } from "./v2";

export interface ToolsetsApi {
    getBuiltin: () => Promise<Toolset[]>;
    getCustom: () => Promise<Toolset[]>;
    getConfig: (toolsetId: string, signal?: AbortSignal) => Promise<ToolsetConfigResponse>;
    patchConfig: (toolsetId: string, values: Record<string, Record<string, unknown>>) => Promise<ToolsetConfigResponse>;
    getBuildTarget: () => Promise<ToolsetBuildTarget>;
    create: (request: CreateToolsetRequest) => Promise<{ id: string }>;
    update: (toolsetId: string, request: UpdateToolsetRequest) => Promise<void>;
    delete: (toolsetId: string) => Promise<void>;
    uploadBundle: (toolsetId: string, file: File) => Promise<void>;
    downloadBundle: (toolsetId: string) => Promise<Blob>;
}

export const toolsetsApi: ToolsetsApi = {
    getBuiltin: getBuiltinToolsetsV2,
    getCustom: getCustomToolsetsV2,
    getConfig: getToolsetConfigV2,
    patchConfig: patchToolsetConfigV2,
    getBuildTarget: getToolsetBuildTargetV2,
    create: createToolsetV2,
    update: updateToolsetV2,
    delete: deleteToolsetV2,
    uploadBundle: uploadToolsetBundleV2,
    downloadBundle: downloadToolsetBundleV2,
};
