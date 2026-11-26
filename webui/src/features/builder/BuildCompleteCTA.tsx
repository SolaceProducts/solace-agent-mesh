// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { BookOpen, FlaskConical, Globe, Pause, Play, Plug, Plus, TriangleAlert } from "lucide-react";

import { useConnectors } from "@/api/connectors";
import { useGateways } from "@/api/gateways";

import { Button } from "@/ui/button";

import type { Gateway } from "@/types";

import { type BlockedComponent, blockedSessionComponents } from "./blockedComponents";
import {
    type DeployEditsContext,
    humanizeComponentName,
    isBuildReadyToTest,
    isConnectorOnlyBuildComplete,
    isEntrypointOnlyBuildComplete,
    isSkillOnlyBuildComplete,
    normalizeComponentName,
    resolveByIdentity,
    SAM_REQUIRED_PLACEHOLDER,
    testableSessionComponents,
} from "./builderUtils";
import type { BuildManifest } from "./hooks/useBuilderManifest";
import { useToolSourceFailures } from "./hooks/useToolSourceFailures";
import { describeToolSourceFailures, type ToolSourceFailure } from "./toolSourceFailures";

interface BuildCompleteCTAProps {
    manifest: BuildManifest;
    deployEdits: DeployEditsContext;
    onSwitchToTest: () => void;
    onAddToAgent: (text: string) => void;
}

interface TestableBuildCardProps {
    componentLabel: string;
    toolSourceProblems: readonly ToolSourceFailure[];
    onSwitchToTest: () => void;
}

function TestableBuildCard({ componentLabel, toolSourceProblems, onSwitchToTest }: Readonly<TestableBuildCardProps>) {
    const hasToolSourceProblems = toolSourceProblems.length > 0;
    const iconTone = hasToolSourceProblems ? "bg-(--warning-w10)" : "bg-(--primary-w10)";
    return (
        <div className="mt-3 flex flex-col gap-3 rounded-md border bg-(--background-w10) p-4">
            <div className="flex items-start gap-3">
                <div className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-full ${iconTone}`}>
                    {hasToolSourceProblems ? <TriangleAlert className="h-4 w-4 text-(--warning-wMain)" /> : <FlaskConical className="h-4 w-4 text-(--primary-wMain)" />}
                </div>
                {hasToolSourceProblems ? (
                    <p className="text-sm leading-relaxed" role="alert">
                        Your {componentLabel} is deployed, but some tools are missing: {describeToolSourceFailures(toolSourceProblems)}
                    </p>
                ) : (
                    <p className="text-sm leading-relaxed">Your {componentLabel} is created and deployed! Would you like to test it? I can generate some sample input and show you what you would receive at the end.</p>
                )}
            </div>
            <div className="flex justify-end">
                <Button size="sm" onClick={onSwitchToTest} className="gap-1.5">
                    <Play className="h-3.5 w-3.5" />
                    Switch to Test Mode
                </Button>
            </div>
        </div>
    );
}

interface EntrypointComponent {
    name: string;
    id?: string;
}

function entrypointSettingsOf(platformEntrypoints: readonly Gateway[] | undefined, loadFailed: boolean, components: readonly EntrypointComponent[]) {
    const withPlaceholders: string[] = [];
    const unchecked: string[] = [];
    if (!loadFailed && !platformEntrypoints) return { withPlaceholders, unchecked };
    for (const component of components) {
        const record = loadFailed ? undefined : resolveByIdentity(component, platformEntrypoints ?? []);
        if (!record) unchecked.push(component.name);
        else if (Object.values(record.deployed?.values ?? record.values ?? {}).includes(SAM_REQUIRED_PLACEHOLDER)) withPlaceholders.push(component.name);
    }
    return { withPlaceholders, unchecked };
}

const quotedList = (names: readonly string[]) => names.map(name => `“${name}”`).join(", ");

function EntrypointBuildCard({ components }: Readonly<{ components: readonly EntrypointComponent[] }>) {
    const { data: platformEntrypoints, isError } = useGateways();
    const { withPlaceholders, unchecked } = entrypointSettingsOf(platformEntrypoints, isError, components);
    const deployed = components.length === 1 ? `Your “${components[0].name}” entrypoint is deployed — find it under Entrypoints.` : `Your ${components.length} entrypoints are deployed — find them under Entrypoints.`;
    const runs = withPlaceholders.length === 1 ? "the entrypoint runs" : "the entrypoints run";
    const placeholderNote =
        withPlaceholders.length > 0
            ? ` The broker connection settings of ${quotedList(withPlaceholders)} still hold placeholders. With no system broker configured they are ignored and ${runs} on the built-in broker; set real values on the Entrypoints page to connect your own.`
            : "";
    const uncheckedNote = unchecked.length > 0 ? ` Could not check the broker connection settings of ${quotedList(unchecked)}; open Entrypoints to confirm they are filled in.` : "";
    return (
        <div className="mt-3 flex flex-col gap-3 rounded-md border bg-(--background-w10) p-4">
            <div className="flex items-start gap-3">
                <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-(--primary-w10)">
                    <Globe className="h-4 w-4 text-(--primary-wMain)" />
                </div>
                <p className="text-sm leading-relaxed">
                    {deployed}
                    {placeholderNote}
                    {uncheckedNote}
                </p>
            </div>
        </div>
    );
}

function BlockedBuildCard({ blocked }: Readonly<{ blocked: readonly BlockedComponent[] }>) {
    return (
        <div className="mt-3 flex flex-col gap-3 rounded-md border bg-(--background-w10) p-4" role="alert">
            <div className="flex items-start gap-3">
                <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-(--warning-w10)">
                    <Pause className="h-4 w-4 text-(--warning-wMain)" />
                </div>
                <div className="flex min-w-0 flex-col gap-2 text-sm leading-relaxed">
                    <p className="font-medium">{blocked.length === 1 ? "This build is waiting on you: a planned component cannot be created yet." : `This build is waiting on you: ${blocked.length} planned components cannot be created yet.`}</p>
                    <ul className="flex list-disc flex-col gap-1 pl-5">
                        {blocked.map(component => (
                            <li key={component.name}>
                                <span className="font-medium">{humanizeComponentName(component.name)}</span> needs {component.reason}.
                                {component.heldBack.length > 0 && <> Held back because they use it: {component.heldBack.map(humanizeComponentName).join(", ")}.</>}
                            </li>
                        ))}
                    </ul>
                    <p>Nothing new has been deployed. Tell me this in the chat and I will finish the build.</p>
                </div>
            </div>
        </div>
    );
}

export function BuildCompleteCTA(props: Readonly<BuildCompleteCTAProps>) {
    const blocked = blockedSessionComponents(props.manifest.components);
    if (blocked.length > 0) return <BlockedBuildCard blocked={blocked} />;
    if (isEntrypointOnlyBuildComplete(props.manifest.components, props.deployEdits)) {
        return <EntrypointBuildCard components={(props.manifest.components ?? []).filter(c => c.origin !== "platform" && c.type === "gateway")} />;
    }
    return <CompletedBuildCTA {...props} />;
}

function CompletedBuildCTA({ manifest, deployEdits, onSwitchToTest, onAddToAgent }: Readonly<BuildCompleteCTAProps>) {
    const sessionComps = manifest.components?.filter(c => c.origin !== "platform") ?? [];
    const { data: platformConnectors } = useConnectors();
    const toolSourceProblems = useToolSourceFailures(manifest.components);

    const testable = testableSessionComponents(manifest.components);
    if (testable.length > 0) {
        if (!isBuildReadyToTest(manifest.components, deployEdits)) return null;
        let componentLabel = "build";
        if (testable.some(c => c.type === "workflow")) componentLabel = "workflow";
        else if (testable.some(c => c.type === "agent")) componentLabel = "agent";
        else if (testable.some(c => c.type === "a2a_agent")) componentLabel = "remote agent";
        return <TestableBuildCard componentLabel={componentLabel} toolSourceProblems={toolSourceProblems} onSwitchToTest={onSwitchToTest} />;
    }

    if (isSkillOnlyBuildComplete(manifest.components)) {
        const skills = sessionComps.filter(c => c.type === "skill");
        const one = skills.length === 1;
        const message = one
            ? `Your “${skills[0].name}” skill is built and saved — find it under Skills. A skill doesn’t run on its own; add it to an agent to start using it.`
            : `Your ${skills.length} skills are built and saved — find them under Skills. Skills don’t run on their own; add them to an agent to start using them.`;
        const addToAgent = () => {
            const text = one ? `Add the ${skills[0].name} skill to an agent.` : `Add these skills to an agent: ${skills.map(s => s.name).join(", ")}.`;
            onAddToAgent(text);
        };

        return (
            <div className="mt-3 flex flex-col gap-3 rounded-md border bg-(--background-w10) p-4">
                <div className="flex items-start gap-3">
                    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-(--primary-w10)">
                        <BookOpen className="h-4 w-4 text-(--primary-wMain)" />
                    </div>
                    <p className="text-sm leading-relaxed">{message}</p>
                </div>
                <div className="flex justify-end">
                    <Button size="sm" onClick={addToAgent} className="gap-1.5">
                        <Plus className="h-3.5 w-3.5" />
                        Add to an agent
                    </Button>
                </div>
            </div>
        );
    }

    if (!isConnectorOnlyBuildComplete(manifest.components)) return null;
    const connectors = sessionComps.filter(c => c.type === "connector");
    if (platformConnectors) {
        const savedNames = new Set(platformConnectors.map(c => normalizeComponentName(c.name)));
        if (!connectors.every(c => savedNames.has(normalizeComponentName(c.name)))) return null;
    }
    const oneConn = connectors.length === 1;
    const connMessage = oneConn
        ? `Your “${connectors[0].name}” connector is built and saved — find it under Connectors. A connector doesn’t run on its own; add it to an agent to start using it.`
        : `Your ${connectors.length} connectors are built and saved — find them under Connectors. Connectors don’t run on their own; add them to an agent to start using them.`;
    const addConnectorToAgent = () => {
        const text = oneConn ? `Add the ${connectors[0].name} connector to an agent.` : `Add these connectors to an agent: ${connectors.map(c => c.name).join(", ")}.`;
        onAddToAgent(text);
    };

    return (
        <div className="mt-3 flex flex-col gap-3 rounded-md border bg-(--background-w10) p-4">
            <div className="flex items-start gap-3">
                <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-(--primary-w10)">
                    <Plug className="h-4 w-4 text-(--primary-wMain)" />
                </div>
                <p className="text-sm leading-relaxed">{connMessage}</p>
            </div>
            <div className="flex justify-end">
                <Button size="sm" onClick={addConnectorToAgent} className="gap-1.5">
                    <Plus className="h-3.5 w-3.5" />
                    Add to an agent
                </Button>
            </div>
        </div>
    );
}
