// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ComponentProps } from "react";

import { Card } from "@/ui/card";

import { cn } from "@/utils";

interface GridCardProps extends ComponentProps<typeof Card> {
    isSelected?: boolean;
    onClick?: () => void;
}

export const GridCard = ({ children, className, isSelected, onClick, ...props }: GridCardProps) => {
    return (
        <Card noPadding onCardSelect={onClick} isCardSelected={isSelected} className={cn("flex h-50 w-full shrink-0 gap-3 py-3 sm:gap-5 sm:py-4", className)} {...props}>
            {children}
        </Card>
    );
};
