// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { LucideIcon } from "lucide-react";
import type { MouseEventHandler, ReactNode } from "react";

import { Button } from "@/ui/button";

export const getSectionTitle = (IconComponent: LucideIcon, title: string | ReactNode, onEdit?: MouseEventHandler<HTMLButtonElement>, actionButton?: ReactNode, required?: boolean): ReactNode => {
    const buttonTitle = "Edit " + title;
    return (
        <div className="flex items-center justify-between gap-4">
            <div className="flex items-center gap-4">
                <IconComponent className="size-6 stroke-(--brand-w100)" />
                <div className="w-30 text-base font-semibold text-nowrap">
                    {title}
                    {required && <span className="ml-1 text-(--brand-wMain)">*</span>}
                </div>
                {onEdit && (
                    <Button variant="ghost" title={buttonTitle} data-testid={buttonTitle} onClick={onEdit}>
                        Edit
                    </Button>
                )}
            </div>
            {actionButton}
        </div>
    );
};
