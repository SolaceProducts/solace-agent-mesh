// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ExternalLink } from "lucide-react";
import { Link } from "react-router-dom";

import { cn } from "@/utils/cnTailwind";
import { newTabProps } from "@/utils/openExternalLink";

type ExperimentLinkProps = {
    id: string;
    name: string;
    className?: string;
    iconClassName?: string;
};

export function ExperimentLink({ id, name, className, iconClassName = "size-3" }: Readonly<ExperimentLinkProps>) {
    return (
        <Link to={`/evaluations/experiments/${id}`} {...newTabProps()} className={cn("inline-flex items-center gap-1 font-semibold text-(--primary-wMain) hover:underline", className)}>
            {name}
            <ExternalLink className={iconClassName} />
        </Link>
    );
}
