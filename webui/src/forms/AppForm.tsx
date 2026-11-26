// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect, useRef, useState } from "react";
import { type FieldValues, FormProvider, useFormContext, type UseFormReturn } from "react-hook-form";

import { Button, type ButtonProps } from "@/ui/button";

import { MessageBanner } from "@/common";
import { cn } from "@/utils";

import { applyServerErrors, type ServerErrorBanner } from "./applyServerErrors";

export interface AppFormProps<TInput extends FieldValues, TOutput> {
    form: UseFormReturn<TInput, unknown, TOutput>;
    onSubmit: (values: TOutput) => Promise<unknown> | void;
    id?: string;
    className?: string;
    children: ReactNode;
}

export function AppForm<TInput extends FieldValues, TOutput>({ form, onSubmit, id, className, children }: Readonly<AppFormProps<TInput, TOutput>>) {
    const [banner, setBanner] = useState<ServerErrorBanner | null>(null);
    const bannerRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        if (!banner) return;
        bannerRef.current?.scrollIntoView?.({ block: "nearest" });
        bannerRef.current?.focus({ preventScroll: true });
    }, [banner]);

    const submit = form.handleSubmit(
        async values => {
            setBanner(null);
            try {
                await onSubmit(values);
            } catch (error) {
                setBanner(applyServerErrors(form, error));
            }
        },
        () => setBanner(null)
    );

    return (
        <FormProvider {...form}>
            <form id={id} noValidate onSubmit={submit} className={cn("flex flex-col gap-4", className)}>
                {banner && (
                    <div ref={bannerRef} tabIndex={-1} data-testid="form-error-banner" className="outline-none">
                        <MessageBanner
                            variant="error"
                            message={
                                <div className="flex flex-col gap-1">
                                    <span>{banner.message}</span>
                                    {banner.errorId && <span className="text-xs">Error ID: {banner.errorId}</span>}
                                </div>
                            }
                        />
                    </div>
                )}
                {children}
            </form>
        </FormProvider>
    );
}

export type SubmitButtonProps = Omit<ButtonProps, "type">;

export function SubmitButton({ disabled, ...props }: SubmitButtonProps) {
    const { formState } = useFormContext();
    return <Button type="submit" disabled={disabled || formState.isSubmitting} {...props} />;
}
