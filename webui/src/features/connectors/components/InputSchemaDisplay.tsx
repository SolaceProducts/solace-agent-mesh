// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

interface InputSchemaDisplayProps {
    schema: Record<string, unknown>;
    viewMode: "table" | "json";
}

export const InputSchemaDisplay = ({ schema, viewMode }: InputSchemaDisplayProps) => {
    const properties = (schema.properties as Record<string, any>) || {};
    const required = (schema.required as string[]) || [];

    const getTypeDisplay = (property: any): string => {
        if (property.enum) {
            return `enum (${property.enum.join(", ")})`;
        }
        if (property.type === "array") {
            const itemType = property.items?.type || "any";
            return `array<${itemType}>`;
        }
        return property.type || "any";
    };

    const renderTableView = () => {
        const propertyEntries = Object.entries(properties);

        if (propertyEntries.length === 0) {
            return <div className="p-4 text-(--secondary-text-wMain)">No parameters defined</div>;
        }

        return (
            <div className="overflow-x-auto overflow-y-auto">
                <table className="w-full border-collapse">
                    <thead className="sticky top-0 z-10 bg-(--secondary-w10)/50">
                        <tr className="border-b">
                            <th className="p-3 text-left font-semibold">Key</th>
                            <th className="p-3 text-left font-semibold">Type</th>
                            <th className="p-3 text-left font-semibold">Required</th>
                            <th className="p-3 text-left font-semibold">Description</th>
                        </tr>
                    </thead>
                    <tbody>
                        {propertyEntries.map(([key, property]) => {
                            const isRequired = required.includes(key);
                            const typeDisplay = getTypeDisplay(property);
                            const description = property.description ?? "—";

                            return (
                                <tr key={key} className="border-b hover:bg-(--secondary-w10)/30">
                                    <td className="p-3 font-mono text-sm">{key}</td>
                                    <td className="p-3">
                                        <code className="rounded bg-(--secondary-w10) px-2 py-1 text-xs">{typeDisplay}</code>
                                    </td>
                                    <td className="p-3">
                                        {isRequired ? (
                                            <span className="inline-flex items-center rounded-full bg-(--error-w10) px-2 py-1 text-xs font-medium text-(--error-w100)">Required</span>
                                        ) : (
                                            <span className="text-sm text-(--secondary-text-wMain)">Optional</span>
                                        )}
                                    </td>
                                    <td className="p-3 text-sm">{description}</td>
                                </tr>
                            );
                        })}
                    </tbody>
                </table>
            </div>
        );
    };

    const renderJsonView = () => {
        return (
            <div className="overflow-x-auto overflow-y-auto bg-(--secondary-w10)/30 p-4">
                <pre className="text-sm">
                    <code>{JSON.stringify(schema, null, 2)}</code>
                </pre>
            </div>
        );
    };

    return <div className="max-h-[500px] overflow-hidden overflow-y-auto">{viewMode === "table" ? renderTableView() : renderJsonView()}</div>;
};
