// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useRef } from "react";
import { Search, X } from "lucide-react";

import { cn } from "@/utils";

import { Button } from "./button";
import { Input } from "./input";

interface SearchInputProps {
    value: string;
    onChange: (value: string) => void;
    placeholder?: string;
    testid?: string;
    className?: string;
}

export const SearchInput: React.FC<SearchInputProps> = ({ value, onChange, placeholder = "Filter by name", testid, className = "" }) => {
    const inputRef = useRef<HTMLInputElement>(null);
    const clear = () => {
        onChange("");
        inputRef.current?.focus();
    };
    return (
        <div className={cn("relative w-full max-w-xs", className)}>
            {value ? (
                <Button type="button" variant="ghost" size="icon" aria-label="Clear search" onClick={clear} className="absolute top-1/2 right-1.5 h-6 w-6 -translate-y-1/2 p-0 hover:bg-(--secondary-w20)">
                    <X className="h-4 w-4 text-(--secondary-text-wMain)" />
                </Button>
            ) : (
                <Search className="absolute top-1/2 right-3 h-4 w-4 -translate-y-1/2 text-(--secondary-text-wMain)" />
            )}
            <Input ref={inputRef} type="text" data-testid={testid} placeholder={placeholder} value={value} onChange={e => onChange(e.target.value)} className="bg-(--background-w10) py-2 pr-9 pl-3" />
        </div>
    );
};
