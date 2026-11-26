// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ExternalLink } from "lucide-react";
import { Link } from "react-router-dom";

import { newTabProps } from "@/utils/openExternalLink";

import { Button, type ButtonProps } from "@/ui/button";

type NewTabLinkProps = Omit<ButtonProps, "asChild" | "children"> & {
    to: string;
    label: string;
};

function isExternalHref(href: string) {
    try {
        return new URL(href, globalThis.location.href).origin !== globalThis.location.origin;
    } catch {
        return false;
    }
}

export function NewTabLink({ to, label, variant = "link", ...buttonProps }: Readonly<NewTabLinkProps>) {
    const content = (
        <>
            <div className="truncate">{label}</div>
            <ExternalLink />
        </>
    );
    return (
        <Button asChild variant={variant} title={label} {...buttonProps}>
            {isExternalHref(to) ? (
                <a href={to} target="_blank" rel="noopener noreferrer">
                    {content}
                </a>
            ) : (
                <Link to={to} {...newTabProps()}>
                    {content}
                </Link>
            )}
        </Button>
    );
}
