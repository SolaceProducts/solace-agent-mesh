// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ChipList, CollapsibleSection, DetailKeyValue, formatKeyLabel } from "./DetailPrimitives";

interface YamlSectionRendererProps {
    data: Record<string, unknown>;
    keys?: string[];
    depth?: number;
}

export function YamlSectionRenderer({ data, keys, depth = 0 }: YamlSectionRendererProps) {
    const keysToRender = keys ? keys.filter(k => k in data) : Object.keys(data);

    if (keysToRender.length === 0) {
        return <p className="text-xs italic">No configuration in this section</p>;
    }

    return (
        <div className="space-y-0.5">
            {keysToRender.map(key => (
                <ValueRenderer key={key} label={formatKeyLabel(key)} rawKey={key} value={data[key]} depth={depth} />
            ))}
        </div>
    );
}

function ValueRenderer({ label, rawKey, value, depth }: { label: string; rawKey: string; value: unknown; depth: number }) {
    if (value === null || value === undefined || typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
        return <DetailKeyValue label={label} value={value} rawKey={rawKey} />;
    }

    if (Array.isArray(value)) {
        return <ArrayRenderer label={label} items={value} depth={depth} />;
    }

    if (typeof value === "object") {
        const obj = value as Record<string, unknown>;
        const objKeys = Object.keys(obj);
        if (objKeys.length === 0) {
            return <DetailKeyValue label={label} value={null} rawKey={rawKey} />;
        }
        return (
            <CollapsibleSection label={label} defaultOpen={depth < 2}>
                <YamlSectionRenderer data={obj} depth={depth + 1} />
            </CollapsibleSection>
        );
    }

    return <DetailKeyValue label={label} value={value} rawKey={rawKey} />;
}

function ArrayRenderer({ label, items, depth }: { label: string; items: unknown[]; depth: number }) {
    if (items.length === 0) {
        return (
            <div className="mb-1.5">
                <span className="text-[10px] font-medium">{label}</span>
                <div className="text-xs italic">empty</div>
            </div>
        );
    }

    if (items.every(item => typeof item === "string" || typeof item === "number" || typeof item === "boolean")) {
        return (
            <div className="mb-1.5">
                <span className="text-[10px] font-medium">{label}</span>
                <div className="mt-0.5">
                    <ChipList items={items.map(String)} />
                </div>
            </div>
        );
    }

    return (
        <CollapsibleSection label={`${label} (${items.length})`} defaultOpen={depth < 1}>
            <div className="space-y-1">
                {items.map((item, i) => {
                    if (item && typeof item === "object" && !Array.isArray(item)) {
                        const obj = item as Record<string, unknown>;
                        const itemLabel = inferItemLabel(obj, i);
                        return (
                            <CollapsibleSection key={i} label={itemLabel} defaultOpen={depth < 1 && items.length <= 3}>
                                <YamlSectionRenderer data={obj} depth={depth + 2} />
                            </CollapsibleSection>
                        );
                    }
                    return <DetailKeyValue key={i} label={`[${i}]`} value={item} />;
                })}
            </div>
        </CollapsibleSection>
    );
}

function inferItemLabel(obj: Record<string, unknown>, index: number): string {
    for (const key of ["name", "id", "tool_name", "group_name", "agent_name", "workflow_name", "subtype", "type"]) {
        if (typeof obj[key] === "string" && obj[key]) {
            return obj[key] as string;
        }
    }
    return `Item ${index + 1}`;
}
