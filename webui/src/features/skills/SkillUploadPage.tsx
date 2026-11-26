// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { Navigate, useLoaderData, useLocation, useNavigate } from "react-router-dom";

import { skillKeys, skillsApi, useSkill } from "@/api/skills";

import { useUserContext } from "@/providers/contexts";

import { CharacterCountLabel } from "@/common/form";
import { PageContentWrapper, PageFooter, PageLabel, PageLabelWithValue, PageSection } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { isZipFile, MAX_UPLOAD_SIZE_BYTES, validateFileSizes, ZIP_INVALID_ERROR, ZIP_ONLY_ERROR } from "@/utils/fileValidation";

import { Button } from "@/ui/button";
import { Textarea } from "@/ui/textarea";

import { ConfirmationDialog, FileUpload, Header, MessageBanner } from "@/common";

import { SkillBuildGuide } from "./SkillBuildGuide";
import { reuploadSkill, saveSkill } from "./utils/saveSkill";

const DESCRIPTION_MAX_LENGTH = 1000;

export const SkillUploadPage: React.FC = () => {
    const loaderData = useLoaderData() as { skillId?: string } | null;
    const skillId = loaderData?.skillId;
    const navigate = useNavigate();
    const location = useLocation();
    const queryClient = useQueryClient();
    const { userHasCapability, CAPABILITIES } = useUserContext();

    const { data: existingSkill } = useSkill(skillId ?? "");

    const droppedFile = skillId ? null : ((location.state as { droppedFile?: File } | null)?.droppedFile ?? null);
    const initialFiles = useMemo(() => {
        if (!droppedFile) return null;
        const dataTransfer = new DataTransfer();
        dataTransfer.items.add(droppedFile);
        return dataTransfer.files;
    }, [droppedFile]);

    const [description, setDescription] = useState(existingSkill?.description ?? "");
    const [skillFiles, setSkillFiles] = useState<FileList | null>(initialFiles);
    const file = skillFiles?.[0] ?? null;

    useEffect(() => {
        if (existingSkill) {
            setDescription(existingSkill.description);
        }
    }, [existingSkill]);
    const [fileError, setFileError] = useState<string | null>(null);
    const [submitError, setSubmitError] = useState<string | null>(null);
    const [loading, setLoading] = useState(false);
    const [reuploadConfirm, setReuploadConfirm] = useState(false);

    const isEdit = !!skillId;

    if (!userHasCapability(CAPABILITIES.createSkills) && !userHasCapability(CAPABILITIES.updateSkills)) {
        return <Navigate to="/skills" replace />;
    }

    const handleFileChange = (fileList: FileList | null) => {
        setSkillFiles(fileList);
        setFileError(null);
    };

    const handleValidateFile = async (fileList: FileList): Promise<{ valid: boolean; error?: string }> => {
        const f = fileList[0];
        if (!f.name.toLowerCase().endsWith(".zip")) {
            return { valid: false, error: ZIP_ONLY_ERROR };
        }
        const sizeCheck = validateFileSizes([f], { maxSizeBytes: MAX_UPLOAD_SIZE_BYTES });
        if (!sizeCheck.valid) {
            return { valid: false, error: sizeCheck.error };
        }
        if (!(await isZipFile(f))) {
            return { valid: false, error: ZIP_INVALID_ERROR };
        }
        return { valid: true };
    };

    const performUpload = async () => {
        if (!file) return;
        setLoading(true);
        try {
            const skill = isEdit ? await reuploadSkill(skillsApi, skillId, file) : await saveSkill(skillsApi, { description, file });
            queryClient.invalidateQueries({ queryKey: skillKeys.lists() });
            if (isEdit) queryClient.invalidateQueries({ queryKey: skillKeys.details() });
            navigate(`/skills/${skill.id}`);
        } catch (err: unknown) {
            const msg = err instanceof Error ? err.message : "Upload failed";
            setSubmitError(msg);
        } finally {
            setLoading(false);
        }
    };

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setSubmitError(null);

        if (!file) {
            setFileError("Please select a skill ZIP file");
            return;
        }

        if (!file.name.toLowerCase().endsWith(".zip")) {
            setFileError(ZIP_ONLY_ERROR);
            return;
        }

        const sizeCheck = validateFileSizes([file], { maxSizeBytes: MAX_UPLOAD_SIZE_BYTES });
        if (!sizeCheck.valid) {
            setFileError(sizeCheck.error ?? "File too large");
            return;
        }

        if (!(await isZipFile(file))) {
            setFileError(ZIP_INVALID_ERROR);
            return;
        }

        if (isEdit && (existingSkill?.agents?.length ?? 0) > 0) {
            setReuploadConfirm(true);
            return;
        }

        await performUpload();
    };

    const title = isEdit ? `Re-upload Skill${existingSkill ? `: ${existingSkill.name}` : ""}` : "Upload Skill";
    const breadcrumbs = isEdit
        ? [{ label: "Skills", onClick: () => navigate("/skills") }, { label: existingSkill?.name ?? "Skill", onClick: () => navigate(`/skills/${skillId}`) }, { label: "Re-upload" }]
        : [{ label: "Skills", onClick: () => navigate("/skills") }, { label: "Upload Skill" }];

    return (
        <div className="flex h-full w-full flex-col overflow-hidden">
            <Header title={title} breadcrumbs={breadcrumbs} />

            <PageContentWrapper>
                <div className={isEdit ? "" : "grid grid-cols-1 gap-6 md:grid-cols-[1fr_352px] md:items-start"}>
                    <form id="skill-upload-form" onSubmit={handleSubmit} noValidate className={isEdit ? "" : "col-start-1"}>
                        {!isEdit && (
                            <PageSection className="mb-6 gap-6">
                                <PageLabelWithValue>
                                    <PageLabel required>Description</PageLabel>
                                    <div>Explain what your skill does in 1-2 short sentences for other users.</div>
                                    <Textarea id="skill-description" value={description} onChange={e => setDescription(e.target.value)} className="resize-none" rows={3} maxLength={DESCRIPTION_MAX_LENGTH} data-testid="skillDescriptionInput" />
                                    <div className="mt-1 flex items-center">
                                        <div className="ml-auto">
                                            <CharacterCountLabel currentLength={description.length} maxLength={DESCRIPTION_MAX_LENGTH} />
                                        </div>
                                    </div>
                                </PageLabelWithValue>
                            </PageSection>
                        )}

                        <PageSection>
                            <PageLabelWithValue>
                                <PageLabel required>Skill</PageLabel>
                                <div>Upload a packaged skill file containing a SKILL.md at the root. The skill name is taken from the SKILL.md frontmatter. Bundled tools are detected and registered once the skill is created.</div>
                                <FileUpload name="skillZip" accept=".zip" value={skillFiles} onChange={handleFileChange} onValidate={handleValidateFile} testid="skillFileUpload" />
                                {fileError && <ErrorLabel message={fileError} />}
                            </PageLabelWithValue>
                        </PageSection>

                        {submitError && (
                            <div className="mt-4">
                                <MessageBanner variant="error" message={submitError} />
                            </div>
                        )}
                    </form>
                    {!isEdit && (
                        <div className="md:col-start-2">
                            <SkillBuildGuide />
                        </div>
                    )}
                </div>
            </PageContentWrapper>

            <PageFooter>
                <Button type="button" variant="ghost" onClick={() => navigate(-1)} disabled={loading}>
                    Cancel
                </Button>
                <Button type="submit" form="skill-upload-form" disabled={loading} data-testid="uploadSkillButton">
                    {loading ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                    {isEdit ? "Re-upload Skill" : "Upload Skill"}
                </Button>
            </PageFooter>

            <ConfirmationDialog
                open={reuploadConfirm}
                title="Re-upload skill?"
                content={
                    <p>
                        This skill is used by <span className="font-semibold">{existingSkill?.agents?.length} agent(s)</span>. Re-uploading will replace its tools and redeploy those agents. Continue?
                    </p>
                }
                onOpenChange={open => !open && setReuploadConfirm(false)}
                onCancel={() => setReuploadConfirm(false)}
                onConfirm={() => {
                    setReuploadConfirm(false);
                    void performUpload();
                }}
                actionLabels={{ confirm: "Re-upload" }}
            />
        </div>
    );
};
