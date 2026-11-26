// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { dump as yamlDump } from "js-yaml";

import { listManifestVersions, uploadArtifact } from "@/api/builder/service";
import { api } from "@/api/client";

import { workflowBody } from "@/utils/agentUtils";

import type { Workflow } from "@/types/workflow";

import { BUILD_MANIFEST_ARTIFACT_FILENAME } from "@/features/builder/builderArtifacts";
import { SAM_WORKFLOW_CONFIG_MIME_TYPE } from "@/features/builder/builderUtils";
import { SAM_BUILD_MANIFEST_MIME_TYPE } from "@/features/builder/hooks/useBuilderManifest";

import type { Agent } from "@/types";

async function writeArtifact(sessionId: string, filename: string, content: string, mimeType: string): Promise<void> {
    const { ok } = await uploadArtifact(sessionId, filename, content, mimeType);
    if (!ok) throw new Error(`failed to upload builder artifact ${filename}`);
}

async function writeArtifactAndReadVersion(sessionId: string, filename: string, content: string, mimeType: string): Promise<number | null> {
    await writeArtifact(sessionId, filename, content, mimeType);
    try {
        const versions = await listManifestVersions(sessionId, filename);
        return versions.length > 0 ? Math.max(...versions) : null;
    } catch (err) {
        console.warn(`[builder] could not read back the version of ${filename}; the Builder will validate it again.`, err);
        return null;
    }
}

interface SeededManifestComponent {
    name: string;
    type: string;
    filename: string;
    origin: "session" | "platform";
    status: "deployed";
    deployed_version?: number;
    nodes?: unknown[];
    depends_on?: string[];
}

function workflowNodes(appConfig: Record<string, unknown>): Array<Record<string, unknown>> {
    const nodes = workflowBody(appConfig).nodes;
    return Array.isArray(nodes) ? (nodes as Array<Record<string, unknown>>) : [];
}

function agentNodeNames(appConfig: Record<string, unknown>): string[] {
    const names = new Set<string>();
    for (const node of workflowNodes(appConfig)) {
        if (node?.type === "agent" && typeof node.agent_name === "string") {
            names.add(node.agent_name);
        }
    }
    return [...names];
}

function aweWrap(name: string, appConfig: Record<string, unknown>): string {
    return yamlDump({ apps: [{ name, app_config: appConfig }] });
}

async function createSessionBeforeArtifactUploads(sessionId: string): Promise<void> {
    await api.webui.post("/api/v1/sessions", { id: sessionId });
}

export async function seedWorkflowIntoBuilder(sessionId: string, workflow: Workflow, agents: Agent[]): Promise<void> {
    const appConfig: Record<string, unknown> = workflow.appConfig ?? {};
    const components: SeededManifestComponent[] = [];

    await createSessionBeforeArtifactUploads(sessionId);

    const referencedAgents = agentNodeNames(appConfig);
    const knownAgents = referencedAgents.filter(name => agents.some(a => a.name === name));
    for (const name of referencedAgents) {
        if (!knownAgents.includes(name)) {
            console.warn(`[builder] workflow "${workflow.name}" references agent "${name}" not found in the agent list; it will render as a step without a component card.`);
        }
    }

    const workflowFilename = `${workflow.name}.yaml`;
    const workflowVersion = await writeArtifactAndReadVersion(sessionId, workflowFilename, aweWrap(workflow.name, appConfig), SAM_WORKFLOW_CONFIG_MIME_TYPE);
    components.push({
        name: workflow.name,
        type: "workflow",
        filename: workflowFilename,
        origin: "session",
        status: "deployed",
        ...(workflowVersion === null ? {} : { deployed_version: workflowVersion }),
        nodes: workflowNodes(appConfig),
        depends_on: referencedAgents,
    });

    for (const name of knownAgents) {
        components.push({ name, type: "agent", filename: "", origin: "platform", status: "deployed" });
    }

    const manifest = {
        name: workflow.name,
        display_name: workflow.displayName || workflow.name,
        description: (workflowBody(appConfig).description as string) || workflow.description || "",
        full_validation_passed: true,
        ...(workflowVersion === null ? {} : { full_validation_versions: { [workflowFilename]: workflowVersion } }),
        loaded_for_edit: true,
        components,
    };
    await writeArtifact(sessionId, BUILD_MANIFEST_ARTIFACT_FILENAME, yamlDump(manifest), SAM_BUILD_MANIFEST_MIME_TYPE);
}
