// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ResolvedAgentSkill, Skill } from "@/types";

import { AgentAttachedSkillCard } from "./AgentAbilityCard";
import { builtinSkillRefLabel, customSkillLabel } from "./skillLabels";

interface AgentAttachedSkillsSectionProps {
    skillIds?: string[];
    skillRefs?: string[];
    customSkills?: Skill[];
    builtinSkills?: Skill[];
    resolvedSkills?: ResolvedAgentSkill[];
}

export function AgentAttachedSkillsSection({ skillIds, skillRefs, customSkills, builtinSkills, resolvedSkills }: Readonly<AgentAttachedSkillsSectionProps>) {
    const refs = skillRefs ?? [];
    const ids = skillIds ?? [];
    const resolved = resolvedSkills ?? [];
    const total = refs.length + ids.length + resolved.length;
    return (
        <div>
            <div className="py-2 font-semibold">{`Skills (${total})`}</div>
            <div className="flex flex-col gap-2">
                {refs.map(ref => {
                    const builtin = builtinSkills?.find(s => s.name === ref);
                    return <AgentAttachedSkillCard key={ref} skill={{ name: builtinSkillRefLabel(ref, builtin), description: builtin?.description }} className="w-full" />;
                })}
                {ids.map(id => {
                    const skill = customSkills?.find(s => s.id === id);
                    return <AgentAttachedSkillCard key={id} skill={{ name: customSkillLabel(id, skill), description: skill?.description }} className="w-full" />;
                })}
                {resolved.map((skill, i) => (
                    <AgentAttachedSkillCard key={`${skill.name}-${i}`} skill={{ name: skill.name, description: skill.description }} className="w-full" />
                ))}
                {total === 0 && <div className="w-full bg-(--secondary-w10) py-2 text-center text-(--secondary-text-wMain)">No skills.</div>}
            </div>
        </div>
    );
}
