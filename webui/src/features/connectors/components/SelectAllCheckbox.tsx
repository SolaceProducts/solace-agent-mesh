// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CustomCheckbox } from "@/common/CustomCheckbox";

interface SelectAllCheckboxProps {
    totalCount: number;
    selectedCount: number;
    onSelectAll: () => void;
    onDeselectAll: () => void;
    id?: string;
}

export const SelectAllCheckbox = ({ totalCount, selectedCount, onSelectAll, onDeselectAll, id = "select-all" }: Readonly<SelectAllCheckboxProps>) => {
    const isAllSelected = totalCount > 0 && selectedCount === totalCount;
    const isIndeterminate = selectedCount > 0 && selectedCount < totalCount;

    const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        if (e.target.checked || isIndeterminate) {
            onSelectAll();
        } else {
            onDeselectAll();
        }
    };

    return (
        <div className="flex items-center gap-2">
            <CustomCheckbox id={id} checked={isAllSelected} indeterminate={isIndeterminate} onChange={handleChange} />
            <label htmlFor={id} className="cursor-pointer text-[14px] font-medium">
                Enable all
            </label>
        </div>
    );
};
