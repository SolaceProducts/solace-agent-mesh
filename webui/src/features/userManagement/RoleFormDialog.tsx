// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useRef, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { useAgents } from "@/api/agents";
import { useCreateRoleMutation, useUpdateRoleMutation } from "@/api/rbac";
import { useWorkflowsQuery } from "@/api/workflows";

import { MessageBanner } from "@/common/banners/MessageBanner";
import { OnboardingBanner } from "@/common/banners/OnboardingBanner";
import { PageLabel } from "@/common/layout";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Input } from "@/ui/input";
import { Textarea } from "@/ui/textarea";

import { useIsFeedbackManagementEnabled } from "@/hooks";
import type { RBACRole } from "@/types";
import { DOC_LINKS } from "@/utils";

import { type ComponentOption, ensureInvokeForCards } from "./scopeBundles/bundles";
import { ScopeBundleEditor } from "./scopeBundles/ScopeBundleEditor";

const schema = z.object({
    name: z.string().trim().min(1, "Name is required").max(64, "Use 64 characters or fewer"),
    description: z.string().trim().max(500, "Use 500 characters or fewer"),
});
type FormValues = z.infer<typeof schema>;

interface RoleFormDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    role?: RBACRole | null;
}

export function RoleFormDialog({ open, onOpenChange, role }: Readonly<RoleFormDialogProps>) {
    const isEdit = Boolean(role);
    const createRole = useCreateRoleMutation();
    const updateRole = useUpdateRoleMutation();
    const pending = createRole.isPending || updateRole.isPending;
    const feedbackGrants = useIsFeedbackManagementEnabled();

    const [scopes, setScopes] = useState<string[]>([]);

    const { data: agentData } = useAgents({ enabled: open });
    const { data: workflowData } = useWorkflowsQuery({ enabled: open });
    const agents = useMemo<ComponentOption[]>(() => (agentData ?? []).filter(a => a.id).map(a => ({ id: a.id!, name: a.name, description: a.description })), [agentData]);
    const workflows = useMemo<ComponentOption[]>(() => (workflowData ?? []).map(w => ({ id: w.id, name: w.displayName || w.name, description: w.description })), [workflowData]);

    const {
        register,
        handleSubmit,
        reset,
        setError,
        formState: { errors },
    } = useForm<FormValues>({
        resolver: zodResolver(schema),
        defaultValues: { name: "", description: "" },
    });

    const feedbackGrantsRef = useRef(feedbackGrants);
    feedbackGrantsRef.current = feedbackGrants;

    useEffect(() => {
        if (!open) return;
        reset({ name: role?.name ?? "", description: role?.description ?? "" });
        const initialScopes = role?.scopes ?? [];
        setScopes(feedbackGrantsRef.current ? ensureInvokeForCards(initialScopes) : initialScopes);
    }, [open, role, reset]);

    const close = () => {
        createRole.reset();
        updateRole.reset();
        onOpenChange(false);
    };

    const onSubmit = handleSubmit(async values => {
        const scopesToSave = feedbackGrants ? ensureInvokeForCards(scopes) : scopes;
        try {
            if (role) {
                await updateRole.mutateAsync({ id: role.id, changes: { name: values.name, description: values.description, scopes: scopesToSave, inherits: role.inherits ?? [] } });
            } else {
                await createRole.mutateAsync({ name: values.name, description: values.description, scopes: scopesToSave });
            }
            close();
        } catch (e) {
            const message = e instanceof Error ? e.message : "Failed to save the role.";
            setError(/already exists/i.test(message) ? "name" : "root", { message });
        }
    });

    const submitLabel: string = isEdit ? "Save" : "Create Role";

    return (
        <Dialog open={open} onOpenChange={next => (next ? onOpenChange(true) : close())}>
            <DialogContent className="flex h-[80vh] flex-col sm:max-w-3xl">
                <DialogHeader className="shrink-0">
                    <DialogTitle>{isEdit ? "Edit Role" : "Create Role"}</DialogTitle>
                    <DialogDescription>{isEdit ? "Update the role and the scopes it grants." : "Name the role and add the scopes it grants."}</DialogDescription>
                </DialogHeader>

                <form onSubmit={onSubmit} className="flex min-h-0 flex-1 flex-col">
                    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto py-4">
                        <OnboardingBanner
                            className="shrink-0"
                            storageKey="sam-role-scopes-onboarding-dismissed"
                            header="Grant access to agents and workflows."
                            description="On the Access tab, select the agents and workflows that users with this role can use. The Scopes tab shows every scope the role grants."
                            learnMoreText="Learn more about user management"
                            learnMoreUrl={DOC_LINKS.userManagementRoles}
                        />
                        {errors.root && <MessageBanner variant="error" className="shrink-0" message={errors.root.message} />}
                        <div className="flex shrink-0 flex-col gap-1">
                            <PageLabel htmlFor="role-name" required>
                                Name
                            </PageLabel>
                            <Input id="role-name" {...register("name")} aria-invalid={Boolean(errors.name)} />
                            {errors.name && <span className="text-xs text-(--error-wMain)">{errors.name.message}</span>}
                        </div>

                        <div className="flex shrink-0 flex-col gap-1">
                            <PageLabel htmlFor="role-description">Description</PageLabel>
                            <Textarea id="role-description" {...register("description")} />
                            {errors.description && <span className="text-xs text-(--error-wMain)">{errors.description.message}</span>}
                        </div>

                        <div className="my-2 flex flex-col">
                            <ScopeBundleEditor value={scopes} onChange={setScopes} agents={agents} workflows={workflows} />
                        </div>
                    </div>

                    <DialogFooter className="shrink-0">
                        <Button type="button" variant="ghost" onClick={close} disabled={pending}>
                            Cancel
                        </Button>
                        <Button type="submit" variant="outline" disabled={pending}>
                            {submitLabel}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}
