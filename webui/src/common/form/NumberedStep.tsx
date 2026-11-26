// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";

import { NewTabLink } from "@/common/NewTabLink";

interface NumberedStepProps {
    number: number;
    title: string;
    description: ReactNode;
    button?: {
        label: string;
        href: string;
    };
    children?: ReactNode;
}

export function NumberedStep({ number, title, description, button, children }: NumberedStepProps) {
    return (
        <div>
            <div className="mb-1 flex items-center gap-2">
                <div className="inline-flex h-6 w-6 items-center justify-center rounded bg-(--primary-wMain) text-xs font-bold text-(--primary-text-w10)">{number}</div>
                <div className="text-sm font-semibold text-(--primary-text-wMain)">{title}</div>
            </div>
            <div className="text-sm">{description}</div>
            {children && <div className="mt-2">{children}</div>}
            {button && <NewTabLink to={button.href} className="p-0 has-[>svg]:px-0" label={button.label} />}
        </div>
    );
}
