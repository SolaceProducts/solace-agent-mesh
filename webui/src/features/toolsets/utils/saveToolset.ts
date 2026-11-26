// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ToolsetsApi } from "@/api/toolsets";

export async function saveToolset(toolsets: ToolsetsApi, name: string, description: string | undefined, file: File): Promise<string> {
    let toolsetId: string;
    try {
        const created = await toolsets.create({ name, description });
        toolsetId = created?.id;
        if (!toolsetId) {
            throw new Error("Failed to create toolset: No ID returned");
        }
    } catch (error) {
        throw new Error(`Failed to create toolset: ${error instanceof Error ? error.message : String(error)}`, { cause: error });
    }

    try {
        await uploadToolsetFile(toolsets, toolsetId, file);
    } catch (error) {
        try {
            await toolsets.delete(toolsetId);
        } catch (deleteError) {
            console.error(`Failed to rollback toolset '${toolsetId}' after file upload failure:`, deleteError);
        }
        throw error;
    }

    return toolsetId;
}

export async function uploadToolsetFile(toolsets: ToolsetsApi, toolsetId: string, file: File): Promise<void> {
    try {
        await toolsets.uploadBundle(toolsetId, file);
    } catch (error) {
        throw new Error(`Failed to upload toolset file: ${error instanceof Error ? error.message : String(error)}`, { cause: error });
    }
}
