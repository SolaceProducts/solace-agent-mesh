// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { evaluationsApi } from "@/api/evaluations";

import { PageSection } from "@/common/layout";

import type { EvalArtifact, EvalArtifactRole } from "@/types/eval";

import { type ExampleArtifactEntry } from "../utils";

import { ArtifactsSection } from "./ArtifactsSection";

interface ExampleFilesSectionProps {
    readonly entries: ReadonlyMap<string, ExampleArtifactEntry>;
    readonly onPreview: (entry: ExampleArtifactEntry) => void;
}

const ROLE_SECTIONS: readonly { role: EvalArtifactRole; title: string; caption?: string }[] = [
    { role: "input", title: "Input File" },
    { role: "expected", title: "Expected File", caption: "The reference evaluators grade against — never sent to the agent." },
];

export function ExampleFilesSection({ entries, onPreview }: ExampleFilesSectionProps) {
    return (
        <>
            {ROLE_SECTIONS.map(({ role, title, caption }) => {
                const roleEntries = Array.from(entries.values()).filter(e => e.role === role);
                if (roleEntries.length === 0) return null;
                const byArtifact = new Map(roleEntries.map(e => [e.artifact, e] as const));
                const entryFor = (artifact: EvalArtifact) => byArtifact.get(artifact);
                return (
                    <PageSection key={role}>
                        <h3 className="text-sm font-bold">{title}</h3>
                        {caption && <p className="text-xs text-(--secondary-text-wMain)">{caption}</p>}
                        <ArtifactsSection
                            artifacts={Array.from(byArtifact.keys())}
                            fetchBlob={a => {
                                const entry = entryFor(a);
                                if (!entry) throw new Error(`no recorded trial for ${role} file ${a.filename}`);
                                return evaluationsApi.getRunResultExampleArtifactBlob(entry.resultId, role, a.filename, a.version);
                            }}
                            onPreview={a => {
                                const entry = entryFor(a);
                                if (entry) onPreview(entry);
                            }}
                        />
                    </PageSection>
                );
            })}
        </>
    );
}
