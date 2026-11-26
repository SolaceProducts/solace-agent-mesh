// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Grid, Layers, Table } from "lucide-react";

import { Button } from "@/ui/button";

import { LayoutType } from "@/types";

interface LayoutSelectorProps {
    currentLayout: string;
    onLayoutChange: (layout: string) => void;
    className?: string;
    availableLayouts?: readonly LayoutType[];
}

const LAYOUTS = [
    { id: LayoutType.CARDS, label: "Cards", icon: Grid },
    { id: LayoutType.TABLE, label: "Table", icon: Table },
    { id: LayoutType.HIERARCHICAL, label: "Hierarchical", icon: Layers },
] as const;

export const LayoutSelector = ({ currentLayout, onLayoutChange, className = "", availableLayouts }: Readonly<LayoutSelectorProps>) => {
    const layouts = availableLayouts ? LAYOUTS.filter(layout => availableLayouts.includes(layout.id)) : LAYOUTS;

    return (
        <div className={`flex items-center space-x-1 ${className}`}>
            <span className="hidden text-sm font-semibold sm:inline">Layout:</span>
            <div className="flex gap-1 rounded-sm p-1">
                {layouts.map(layout => {
                    const Icon = layout.icon;
                    const isActive = currentLayout === layout.id;

                    return (
                        <Button variant="ghost" size="sm" key={layout.id} onClick={() => onLayoutChange(layout.id)} title={layout.label} className={isActive ? "bg-(--secondary-w20)" : ""}>
                            <Icon size={14} />
                            <span className="hidden sm:inline">{layout.label}</span>
                        </Button>
                    );
                })}
            </div>
        </div>
    );
};
