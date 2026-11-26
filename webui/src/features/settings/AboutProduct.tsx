// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useVersionInfo } from "@/api/version";

import { getErrorMessage } from "@/utils/api";

import { MessageBanner } from "@/common";
import { Spinner, Table, TableBody, TableCell, TableRow } from "@/ui";

export const AboutProduct = () => {
    const { data: versionData, isLoading, error } = useVersionInfo();

    const renderVersionTable = () => {
        if (isLoading) {
            return <Spinner className="mt-8" />;
        }
        if (error) {
            return <MessageBanner variant="error" message={`Error loading application information. ${getErrorMessage(error)}`} />;
        }
        if (!versionData) {
            return null;
        }
        return (
            <Table>
                <TableBody>
                    {versionData.products
                        .toSorted((a, b) => a.name.localeCompare(b.name))
                        .map(product => (
                            <TableRow key={product.id} className="border-none hover:bg-transparent">
                                <TableCell className="px-0 font-medium">{product.name}</TableCell>
                                <TableCell>{product.version}</TableCell>
                            </TableRow>
                        ))}
                </TableBody>
            </Table>
        );
    };

    return (
        <div>
            <h5 className="pb-4 font-semibold">Application Versions</h5>
            {renderVersionTable()}
        </div>
    );
};
