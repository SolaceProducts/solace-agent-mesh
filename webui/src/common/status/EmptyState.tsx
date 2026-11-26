// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactElement } from "react";

import { Button, type ButtonVariant } from "@/ui/button";
import { Spinner } from "@/ui/spinner";

import { ErrorIllustration, NotFoundIllustration } from "@/assets";
import { cn } from "@/utils";

export interface ButtonWithCallback {
    icon?: ReactElement;
    text: string;
    variant: ButtonVariant;
    disabled?: boolean;
    onClick: (event: React.MouseEvent<HTMLButtonElement, MouseEvent>) => void;
}

interface EmptyStateProps {
    title: string;
    subtitle?: string | React.ReactNode;
    variant?: "error" | "notFound" | "loading" | "noImage";
    image?: ReactElement;
    buttons?: ButtonWithCallback[];
    className?: string;
}

function EmptyState({ title, subtitle, image, variant = "error", buttons, className }: EmptyStateProps) {
    const illustrations = {
        error: <ErrorIllustration width={150} height={150} />,
        notFound: <NotFoundIllustration width={150} height={150} />,
        loading: <Spinner size="large" />,
        noImage: null,
    };

    return (
        <div className={cn("flex h-full w-full flex-col items-center justify-center gap-3", className)}>
            {image || illustrations[variant] || null}

            <p className="mt-4 text-lg">{title}</p>
            {subtitle ? <p className="max-w-xl text-center text-sm">{subtitle}</p> : null}

            <div className="mt-3 flex min-w-50 flex-col gap-2">
                {buttons?.map(({ icon, text, variant, disabled, onClick }, index) => (
                    <Button key={`button-${text}-${index}`} testid={text} title={text} variant={variant} disabled={disabled} onClick={onClick}>
                        {icon ? icon : null}
                        {text}
                    </Button>
                ))}
            </div>
        </div>
    );
}

export { EmptyState };
