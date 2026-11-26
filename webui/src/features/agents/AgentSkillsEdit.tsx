// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";
import { BookOpen, Loader2 } from "lucide-react";

import { useBuiltinSkills, useSkills } from "@/api/skills";

import { CustomCheckbox } from "@/common/CustomCheckbox";
import { getSectionTitle } from "@/common/layout";

import type { Agent, Skill } from "@/types";

import { AgentEditSection, AgentEditWrapper, AgentEmptySection } from "./common";

interface AgentSkillsEditProps {
    agent: Agent;
    onApply: (agent: Agent) => void;
    onDiscard: () => void;
}

const SkillRow = ({ skill, checked, onToggle }: { skill: Skill; checked: boolean; onToggle: () => void }) => (
    <div className="flex items-start gap-3 rounded-sm border p-4" data-testid={`skillRow-${skill.id}`}>
        <CustomCheckbox checked={checked} onChange={onToggle} />
        <div className="min-w-0 flex-1">
            <div className="font-medium">{skill.label ?? skill.name}</div>
            {skill.description && <div className="mt-0.5 text-sm text-(--secondary-text-wMain)">{skill.description}</div>}
        </div>
    </div>
);

export const AgentSkillsEdit = ({ agent, onApply, onDiscard }: AgentSkillsEditProps) => {
    const { data: customSkills, isLoading: customLoading } = useSkills();
    const { data: builtinSkills, isLoading: builtinLoading } = useBuiltinSkills();

    const builtinSkillList = useMemo(() => builtinSkills ?? [], [builtinSkills]);
    const builtinNameSet = useMemo(() => new Set(builtinSkillList.map(s => s.name)), [builtinSkillList]);

    const [selectedSkillIds, setSelectedSkillIds] = useState<string[]>(agent.skillIds ?? []);
    const [selectedBuiltinRefs, setSelectedBuiltinRefs] = useState<string[]>([]);

    useEffect(() => {
        setSelectedSkillIds(agent.skillIds ?? []);
    }, [agent.skillIds]);

    useEffect(() => {
        const refs = agent.skillRefs ?? [];
        setSelectedBuiltinRefs(refs.filter(r => builtinNameSet.has(r)));
    }, [agent.skillRefs, builtinNameSet]);

    const toggleSkill = useCallback((id: string) => {
        setSelectedSkillIds(prev => (prev.includes(id) ? prev.filter(s => s !== id) : [...prev, id]));
    }, []);

    const toggleBuiltin = useCallback((name: string) => {
        setSelectedBuiltinRefs(prev => (prev.includes(name) ? prev.filter(r => r !== name) : [...prev, name]));
    }, []);

    const onSubmit = () => {
        const refsOutsideBuiltinCatalog = (agent.skillRefs ?? []).filter(r => !builtinNameSet.has(r));
        const skillRefs = Array.from(new Set([...refsOutsideBuiltinCatalog, ...selectedBuiltinRefs]));
        onApply({
            ...agent,
            skillIds: selectedSkillIds,
            skillRefs,
        });
    };

    if (customLoading || builtinLoading) {
        return (
            <div className="flex flex-1 items-center justify-center">
                <Loader2 className="size-8 animate-spin text-(--brand-wMain)" />
            </div>
        );
    }

    return (
        <AgentEditWrapper sectionTitle={getSectionTitle(BookOpen, "Skills")} agent={agent} onSubmit={onSubmit} onDiscard={onDiscard}>
            <AgentEditSection>
                <div>Select skills to give your agent specialized capabilities and instructions at runtime.</div>

                <div className="mt-8 min-w-[600px] pr-8">
                    <div className="mb-8">
                        <h3 className="mb-2 text-sm font-semibold">Built-in Skills</h3>
                        <p className="mb-4 text-sm">Select skills from the catalog to extend an agent with curated capabilities.</p>
                        <div className="space-y-2">
                            {builtinSkillList.length > 0 ? (
                                builtinSkillList.map(skill => <SkillRow key={skill.id} skill={skill} checked={selectedBuiltinRefs.includes(skill.name)} onToggle={() => toggleBuiltin(skill.name)} />)
                            ) : (
                                <AgentEmptySection message="No built-in skills available" />
                            )}
                        </div>
                    </div>

                    <div>
                        <h3 className="mb-2 text-sm font-semibold">Custom Skills</h3>
                        <p className="mb-4 text-sm">Select skills uploaded by organization members to give an agent specialized instructions.</p>
                        <div className="space-y-2">
                            {customSkills && customSkills.length > 0 ? (
                                customSkills.map(skill => <SkillRow key={skill.id} skill={skill} checked={selectedSkillIds.includes(skill.id)} onToggle={() => toggleSkill(skill.id)} />)
                            ) : (
                                <AgentEmptySection message="No custom skills available" />
                            )}
                        </div>
                    </div>
                </div>
            </AgentEditSection>
        </AgentEditWrapper>
    );
};
