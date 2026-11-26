// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Fragment, type ReactNode } from "react";

import type { ClickwrapTerms, TermsDocument } from "@/api/activation";

import { CustomCheckbox } from "@/common/CustomCheckbox";

import { Skeleton } from "@/ui/skeleton";

import { type AgreementFallback, listSeparator, splitCheckboxText } from "./agreementText";

interface AgreementCheckboxProps {
    id: string;
    terms: ClickwrapTerms | undefined;
    fallback: AgreementFallback;
    checked: boolean;
    disabled: boolean;
    onCheckedChange: (checked: boolean) => void;
    onOpen: (documentId: string) => void;
}

export function AgreementCheckbox({ id, terms, fallback, checked, disabled, onCheckedChange, onOpen }: Readonly<AgreementCheckboxProps>) {
    return (
        <div className="flex items-start gap-3">
            <CustomCheckbox id={id} checked={checked} disabled={disabled || !terms} onChange={event => onCheckedChange(event.target.checked)} />
            {terms ? (
                <label htmlFor={id} className="pt-0.5 text-sm text-(--primary-text-wMain)">
                    <AgreementLabel terms={terms} fallback={fallback} onOpen={onOpen} />
                </label>
            ) : (
                <Skeleton className="mt-1 h-4 w-4/5" aria-label="Loading the agreement" />
            )}
        </div>
    );
}

function AgreementLabel({ terms, fallback, onOpen }: Readonly<{ terms: ClickwrapTerms; fallback: AgreementFallback; onOpen: (documentId: string) => void }>) {
    const parts = splitCheckboxText(terms.checkboxText);
    if (parts) {
        const [before, after] = parts;
        return (
            <>
                {before}
                {terms.documents.map((document, index) => (
                    <Fragment key={document.id}>
                        {listSeparator(index, terms.documents.length)}
                        <DocumentLink document={document} onOpen={onOpen}>
                            {document.name}
                        </DocumentLink>
                    </Fragment>
                ))}
                {after}
            </>
        );
    }
    const [first] = terms.documents;
    return (
        <>
            {fallback.before}
            {first ? (
                <DocumentLink document={first} onOpen={onOpen}>
                    {fallback.linkText}
                </DocumentLink>
            ) : (
                fallback.linkText
            )}
            {fallback.after}
        </>
    );
}

function DocumentLink({ document, onOpen, children }: Readonly<{ document: TermsDocument; onOpen: (documentId: string) => void; children: ReactNode }>) {
    return (
        <a href={document.url} target="_blank" rel="noopener noreferrer" onClick={() => onOpen(document.id)} className="font-medium text-(--primary-wMain) underline underline-offset-2">
            {children}
        </a>
    );
}
