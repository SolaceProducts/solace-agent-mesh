// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { Plus, X } from "lucide-react";
import { Controller, useFieldArray, useFormContext } from "react-hook-form";

import { PageLabel } from "@/common/layout";
import { MultiSelect } from "@/common/MultiSelect";
import { ErrorLabel } from "@/common/status";

import { Button } from "@/ui/button";
import { Input } from "@/ui/input";
import { Textarea } from "@/ui/textarea";

import type { AgentSkillFE } from "@/types";

import { AgentAbilityCard } from "../AgentAbilityCard";
import { AgentEmptySection } from "../common";

import { FormField } from "./FormComponents";

interface AgentInfoSectionProps {
    nameFieldName?: string;
    descriptionFieldName?: string;
    onNameBlur?: () => void;
    nameWarning?: string;
}

export const AgentInfoSection = ({ nameFieldName = "name", descriptionFieldName = "description", onNameBlur, nameWarning }: AgentInfoSectionProps) => {
    const {
        register,
        formState: { errors },
        watch,
    } = useFormContext();

    const descriptionValue = watch(descriptionFieldName) || "";
    const descriptionLength = descriptionValue.length;
    const maxDescriptionLength = 1000;

    return (
        <div className="space-y-6">
            <FormField label="Name" required error={errors[nameFieldName] as any} warning={nameWarning}>
                <Input {...register(nameFieldName)} type="text" onBlur={onNameBlur} aria-invalid={!!errors[nameFieldName]} />
            </FormField>

            <div className="grid grid-cols-[200px_1fr] items-start gap-4">
                <PageLabel required>Description</PageLabel>
                <div className="flex flex-col">
                    <Textarea {...register(descriptionFieldName)} className="resize-none" aria-invalid={!!errors[descriptionFieldName]} rows={4} />
                    <div className="mt-1 flex items-center justify-between">
                        <div>{errors[descriptionFieldName] && <ErrorLabel message={(errors[descriptionFieldName] as any)?.message} />}</div>
                        <div className="text-xs text-(--secondary-text-wMain)">
                            {descriptionLength}/{maxDescriptionLength}
                        </div>
                    </div>
                </div>
            </div>
        </div>
    );
};

interface SkillsSectionProps {
    maxSkills?: number;
    fieldName?: string;
}

export const SkillsSection = ({ maxSkills = 10, fieldName = "skills" }: SkillsSectionProps) => {
    const [isAddingSkill, setIsAddingSkill] = useState(false);
    const {
        register,
        control,
        formState: { errors },
        getValues,
        setValue,
        clearErrors,
    } = useFormContext();

    const { fields, append, remove } = useFieldArray({
        control,
        name: fieldName,
    });

    const handleSaveSkill = () => {
        const name = getValues("skillName");
        const description = getValues("skillDescription");

        if (!name || !description) {
            return;
        }

        const newSkill: AgentSkillFE = {
            name: name.trim(),
            description: description.trim(),
        };

        append(newSkill);
        setValue("skillName", "");
        setValue("skillDescription", "");
        clearErrors(["skillName", "skillDescription"]);
        setIsAddingSkill(false);
    };

    const handleCancelAddSkill = () => {
        setValue("skillName", "");
        setValue("skillDescription", "");
        clearErrors(["skillName", "skillDescription"]);
        setIsAddingSkill(false);
    };

    const handleRemoveSkill = (index: number) => {
        remove(index);
    };

    return (
        <div>
            <div className="mb-2 flex items-center justify-between">
                <div>
                    <div className="mb-1 text-base font-semibold">Abilities</div>
                    <div className="mb-4">In your own words, give your agent up to ten abilities that clearly describe what it can do.</div>
                </div>
                {!isAddingSkill && fields.length < maxSkills && (
                    <Button type="button" variant="ghost" title="Add a new ability" onClick={() => setIsAddingSkill(true)} size="sm">
                        <Plus className="size-4" />
                        Add Ability
                    </Button>
                )}
            </div>

            {isAddingSkill && (
                <div className="pb-6">
                    <div className="space-y-4">
                        <div className="grid grid-cols-[200px_1fr] items-start gap-4">
                            <PageLabel required>Name</PageLabel>
                            <div className="flex flex-col">
                                <Input {...register("skillName")} type="text" aria-invalid={!!errors.skillName} />
                                {errors.skillName && <ErrorLabel message={(errors.skillName as any)?.message} />}
                            </div>
                        </div>

                        <div className="grid grid-cols-[200px_1fr] items-start gap-4">
                            <PageLabel required>Description</PageLabel>
                            <div className="flex flex-col">
                                <Textarea {...register("skillDescription")} className="resize-none" aria-invalid={!!errors.skillDescription} rows={2} />
                                {errors.skillDescription && <ErrorLabel message={(errors.skillDescription as any)?.message} />}
                            </div>
                        </div>

                        <div className="grid grid-cols-[200px_1fr] gap-4">
                            <div></div>
                            <div className="flex justify-end gap-2">
                                <Button variant="ghost" onClick={handleCancelAddSkill} type="button">
                                    Cancel
                                </Button>
                                <Button variant="default" type="button" onClick={handleSaveSkill}>
                                    Add
                                </Button>
                            </div>
                        </div>
                    </div>
                </div>
            )}

            {fields.length > 0 && (
                <div className="mb-4 space-y-2">
                    {fields
                        .map((field, index) => ({ ...field, index }))
                        .sort((a, b) => (a as any).name.localeCompare((b as any).name))
                        .map(({ index }) => {
                            const skill = fields[index] as any;
                            return (
                                <div key={skill.id} className="group relative">
                                    <AgentAbilityCard
                                        ability={skill}
                                        className="pr-10"
                                        actionButton={
                                            isAddingSkill ? null : (
                                                <Button type="button" variant="ghost" onClick={() => handleRemoveSkill(index)} tooltip="Remove ability">
                                                    <X className="size-4" />
                                                </Button>
                                            )
                                        }
                                    />
                                </div>
                            );
                        })}
                </div>
            )}
            {fields.length === 0 && !isAddingSkill && <AgentEmptySection message="No abilities" />}
        </div>
    );
};

interface CommunicationModesSectionProps {
    inputModeOptions?: string[];
    outputModeOptions?: string[];
    inputFieldName?: string;
    outputFieldName?: string;
}

export const CommunicationModesSection = ({ inputModeOptions = ["file", "text"], outputModeOptions = ["file", "text"], inputFieldName = "inputModes", outputFieldName = "outputModes" }: CommunicationModesSectionProps) => {
    const { control } = useFormContext();

    return (
        <div>
            <div className="mb-1 text-base font-semibold">Communication Modes</div>
            <div className="mb-4">Select the types of information your agent can work with.</div>
            <div className="space-y-4">
                <div className="grid grid-cols-[200px_1fr] items-start gap-4">
                    <PageLabel>Input Modes</PageLabel>
                    <div>
                        <Controller name={inputFieldName} control={control} render={({ field }) => <MultiSelect options={inputModeOptions} value={field.value || []} onChange={field.onChange} placeholder="" />} />
                    </div>
                </div>

                <div className="grid grid-cols-[200px_1fr] items-start gap-4">
                    <PageLabel>Output Modes</PageLabel>
                    <div>
                        <Controller name={outputFieldName} control={control} render={({ field }) => <MultiSelect options={outputModeOptions} value={field.value || []} onChange={field.onChange} placeholder="" />} />
                    </div>
                </div>
            </div>
        </div>
    );
};
