// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { X } from "lucide-react";

import { Button } from "@/ui/button";

import { componentTypeLabel } from "./builderUtils";
import { componentTypeIcon, StatusBadge, typeColor } from "./canvas/CardShell";
import { lookupConnectorIconByKey } from "./canvas/connectorIconInference";

export interface ComponentChipData {
    name: string;
    type: string;
    icon?: string;
    origin?: string;
}

export function ComponentChip({ component, onRemove }: Readonly<{ component: ComponentChipData; onRemove?: () => void }>) {
    const Icon = componentTypeIcon(component.type);
    const connectorIcon = component.type === "connector" && component.icon && component.icon !== "other" ? lookupConnectorIconByKey(component.icon) : undefined;
    const isNew = component.origin !== "platform";
    return (
        <div className="card-surface inline-flex max-w-full items-center gap-2 overflow-hidden rounded-md border px-3 py-2 text-sm">
            {connectorIcon ? <img src={connectorIcon} alt={component.icon!} className="h-4 w-4 shrink-0 object-contain" /> : <Icon className="h-4 w-4 shrink-0" style={{ color: typeColor(component.type) }} />}
            <span className="min-w-0 truncate font-medium">{component.name}</span>
            <span className="shrink-0 text-xs text-(--secondary-text-wMain)">{componentTypeLabel(component.type)}</span>
            {isNew && <StatusBadge status="new" />}
            {onRemove && (
                <Button variant="ghost" className="h-4 w-4 shrink-0" onClick={onRemove} tooltip="Remove">
                    <X />
                </Button>
            )}
        </div>
    );
}
