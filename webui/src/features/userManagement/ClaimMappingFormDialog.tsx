// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { useNavigate } from "react-router-dom";
import { z } from "zod";

import { useAssignableRoles, useCreateClaimMappingMutation, useUpdateClaimMappingMutation } from "@/api/rbac";

import { useConfigContext } from "@/providers/contexts";

import { MessageBanner } from "@/common/banners/MessageBanner";
import { PageLabel } from "@/common/layout";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Input } from "@/ui/input";

import type { ClaimMapping } from "@/types";

import { NoAssignableRolesBanner } from "./NoAssignableRolesBanner";
import { RoleMultiSelect } from "./RoleMultiSelect";
import { USER_MANAGEMENT_ROUTES } from "./routes";

const schema = z.object({
    name: z.string().trim().min(1, "Name is required").max(255, "Use 255 characters or fewer"),
    claimValue: z.string().trim().min(1, "Claim value is required").max(256, "Use 256 characters or fewer"),
});
type FormValues = z.infer<typeof schema>;

interface ClaimMappingFormDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    mapping?: ClaimMapping | null;
}

export function ClaimMappingFormDialog({ open, onOpenChange, mapping }: Readonly<ClaimMappingFormDialogProps>) {
    const isEdit = Boolean(mapping);
    const navigate = useNavigate();
    const { claimKey } = useConfigContext();
    const { data: roles = [], isSuccess: rolesLoaded } = useAssignableRoles();
    const createMapping = useCreateClaimMappingMutation();
    const updateMapping = useUpdateClaimMappingMutation();
    const pending = createMapping.isPending || updateMapping.isPending;

    const [selectedRoleIds, setSelectedRoleIds] = useState<string[]>([]);

    const noAssignableRoles = rolesLoaded && roles.length === 0;
    const claimKeyUnconfigured = claimKey.trim() === "";

    const {
        register,
        handleSubmit,
        reset,
        setError,
        formState: { errors },
    } = useForm<FormValues>({
        resolver: zodResolver(schema),
        defaultValues: { name: "", claimValue: "" },
    });

    useEffect(() => {
        if (open) {
            reset({ name: mapping?.name ?? "", claimValue: mapping?.claimValue ?? "" });
            setSelectedRoleIds(mapping ? mapping.roleIds : []);
        }
    }, [open, mapping, reset]);

    const close = () => {
        createMapping.reset();
        updateMapping.reset();
        onOpenChange(false);
    };

    const onSubmit = handleSubmit(async values => {
        if (selectedRoleIds.length === 0) {
            setError("root", { message: "Select at least one role." });
            return;
        }
        const base = {
            name: values.name,
            claimKey: claimKey,
            claimValue: values.claimValue,
            roleIds: selectedRoleIds,
        };
        try {
            if (mapping) await updateMapping.mutateAsync({ id: mapping.id, changes: { ...base, oidcProvider: mapping.oidcProvider } });
            else await createMapping.mutateAsync(base);
            close();
        } catch (e) {
            setError("root", { message: e instanceof Error ? e.message : "Failed to save the claim mapping." });
        }
    });

    const body: ReactNode = (
        <>
            {errors.root && <MessageBanner variant="error" message={errors.root.message} />}
            {claimKeyUnconfigured && <MessageBanner variant="warning" message="No claim key is configured, so this mapping won't grant any roles until an administrator sets one." />}
            {noAssignableRoles && (
                <NoAssignableRolesBanner
                    message="No assignable roles exist yet. Create a role before you create a claim mapping. System roles cannot be used in a claim mapping."
                    actionLabel="Go to Roles"
                    onAction={() => {
                        close();
                        navigate(USER_MANAGEMENT_ROUTES.roles);
                    }}
                />
            )}
            {!noAssignableRoles && <MessageBanner variant="info" message="System roles cannot be used in a claim mapping. Roles granted another way, such as a direct assignment, stay in effect." />}

            <div className="flex flex-col gap-1">
                <PageLabel htmlFor="claim-mapping-name" required>
                    Name
                </PageLabel>
                <Input id="claim-mapping-name" {...register("name")} aria-invalid={Boolean(errors.name)} />
                {errors.name && <span className="text-xs text-(--error-wMain)">{errors.name.message}</span>}
            </div>

            <div className="flex flex-col gap-1">
                <PageLabel htmlFor="claim-mapping-value" required>
                    Claim Value
                </PageLabel>
                <Input id="claim-mapping-value" {...register("claimValue")} aria-invalid={Boolean(errors.claimValue)} />
                {errors.claimValue && <span className="text-xs text-(--error-wMain)">{errors.claimValue.message}</span>}
            </div>

            <div className="flex flex-col gap-1">
                <PageLabel required>Roles</PageLabel>
                <RoleMultiSelect roles={roles} value={selectedRoleIds} onChange={setSelectedRoleIds} disabled={noAssignableRoles} />
            </div>
        </>
    );

    return (
        <Dialog open={open} onOpenChange={next => (next ? onOpenChange(true) : close())}>
            <DialogContent className="flex max-h-[80vh] flex-col sm:max-w-2xl">
                <DialogHeader className="shrink-0">
                    <DialogTitle>{isEdit ? "Edit Claim Mapping" : "Create Claim Mapping"}</DialogTitle>
                    <DialogDescription>Grant roles to everyone whose identity-provider claim equals the claim value you enter.</DialogDescription>
                </DialogHeader>

                <form onSubmit={onSubmit} className="flex min-h-0 flex-1 flex-col">
                    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto py-2">{body}</div>

                    <DialogFooter className="shrink-0">
                        <Button type="button" variant="ghost" onClick={close} disabled={pending}>
                            Cancel
                        </Button>
                        <Button type="submit" variant="outline" disabled={pending || selectedRoleIds.length === 0}>
                            {isEdit ? "Save" : "Create Claim Mapping"}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}
