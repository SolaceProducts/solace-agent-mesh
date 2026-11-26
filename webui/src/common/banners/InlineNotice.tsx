// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { AlertCircle, AlertTriangle } from "lucide-react";

import { cn } from "@/utils";

const noticeStyles = {
    warning: { Icon: AlertTriangle, icon: "text-(--warning-wMain)", box: "border-(--warning-wMain) bg-(--warning-w10)" },
    error: { Icon: AlertCircle, icon: "text-(--error-wMain)", box: "border-(--error-wMain) bg-(--error-w10)" },
};

export interface InlineNoticeProps {
    variant: keyof typeof noticeStyles;
    children: React.ReactNode;
    boxed?: boolean;
    className?: string;
}

function InlineNotice({ variant, children, boxed = false, className }: Readonly<InlineNoticeProps>) {
    const { Icon, icon, box } = noticeStyles[variant];
    const noticeProps = {
        "data-testid": `inlineNotice-${variant}`,
        className: cn("flex items-center", boxed && cn("rounded-2xl border px-4 py-3", box), className),
    };
    const content = (
        <>
            <Icon className={cn("mr-2 size-6 shrink-0 self-start", icon)} />
            <div className="min-w-0 leading-relaxed">{children}</div>
        </>
    );
    return variant === "error" ? (
        <div role="alert" {...noticeProps}>
            {content}
        </div>
    ) : (
        <output {...noticeProps}>{content}</output>
    );
}

export { InlineNotice };
