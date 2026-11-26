// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useMemo, useState } from "react";
import DOMPurify from "dompurify";
import parse, { domToReact, Element, type HTMLReactParserOptions } from "html-react-parser";
import { Check, Copy } from "lucide-react";
import { Marked, marked } from "marked";

import { getThemeHtmlStyles } from "@/utils/themeHtmlStyles";

import { Button } from "@/ui";

interface MarkdownHTMLConverterProps {
    children?: string;
    className?: string;
    untrusted?: boolean;
}

const escapeHtml = (text: string): string => text.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;");

const untrustedMarked = new Marked({
    gfm: true,
    renderer: {
        html: ({ text }) => escapeHtml(text),
        image: ({ text }) => escapeHtml(text),
    },
});

const UNTRUSTED_SANITIZE = { USE_PROFILES: { html: true }, FORBID_TAGS: ["img", "style", "form"], FORBID_ATTR: ["style"] };

interface CodeBlockProps {
    code: string;
    language?: string;
}

const CodeBlock: React.FC<CodeBlockProps> = ({ code, language }) => {
    const [isCopied, setIsCopied] = useState(false);

    const handleCopy = useCallback(() => {
        navigator.clipboard
            .writeText(code)
            .then(() => {
                setIsCopied(true);
                setTimeout(() => setIsCopied(false), 2000);
            })
            .catch(err => {
                console.error("Failed to copy code:", err);
            });
    }, [code]);

    return (
        <div className="group relative">
            <pre className="mb-4 max-w-full overflow-x-auto rounded-lg border bg-transparent p-4 whitespace-pre-wrap">
                <code className={`bg-transparent p-0 text-sm break-words ${language ? `language-${language}` : ""}`}>{code}</code>
            </pre>
            <Button
                variant="ghost"
                size="icon"
                className="absolute top-2 right-2 h-8 w-8 bg-(--background-w10)/80 opacity-0 transition-opacity group-hover:opacity-100 hover:bg-(--background-w10)"
                onClick={handleCopy}
                tooltip={isCopied ? "Copied!" : "Copy code"}
            >
                {isCopied ? <Check className="h-4 w-4 text-(--success-wMain)" /> : <Copy className="h-4 w-4" />}
            </Button>
        </div>
    );
};

const liStartsWithCheckbox = (li: Element): boolean => {
    const first = li.children.find(c => (c as { type: string }).type !== "text" || ((c as unknown as { data: string }).data ?? "").trim() !== "");
    return first instanceof Element && first.name === "input" && first.attribs?.type === "checkbox";
};

const styleCheckboxInput = (domNode: Element) => {
    delete domNode.attribs.disabled;
    domNode.attribs.tabindex = "-1";
    domNode.attribs["aria-disabled"] = "true";
    domNode.attribs.class = `${domNode.attribs.class ?? ""} size-3.5 shrink-0 accent-(--primary-wMain) align-middle pointer-events-none`.trim();
};

const renderCodeBlockFromPre = (domNode: Element): React.ReactElement | undefined => {
    const codeElement = domNode.children.find(child => child instanceof Element && child.name === "code") as Element | undefined;
    if (!codeElement) return undefined;
    const codeText = codeElement.children.map(child => ("data" in child ? child.data : "")).join("");
    const languageClass = codeElement.attribs?.class || "";
    const languageMatch = languageClass.match(/language-(\w+)/);
    const language = languageMatch ? languageMatch[1] : undefined;
    return <CodeBlock code={codeText} language={language} />;
};

const parserOptions: HTMLReactParserOptions = {
    replace: domNode => {
        if (!(domNode instanceof Element) || !domNode.attribs) return undefined;

        if (domNode.name === "input" && domNode.attribs.type === "checkbox") {
            styleCheckboxInput(domNode);
            return undefined;
        }

        if (domNode.name === "ul" && domNode.children.some(c => c instanceof Element && c.name === "li" && liStartsWithCheckbox(c))) {
            return <ul style={{ listStyle: "none", paddingLeft: "0.25rem", marginBottom: "1rem" }}>{domToReact(domNode.children as Parameters<typeof domToReact>[0], parserOptions)}</ul>;
        }

        if (domNode.name === "li" && liStartsWithCheckbox(domNode)) {
            return <li style={{ display: "flex", alignItems: "baseline", gap: "0.5rem" }}>{domToReact(domNode.children as Parameters<typeof domToReact>[0], parserOptions)}</li>;
        }

        if (domNode.name === "a") {
            domNode.attribs.target = "_blank";
            domNode.attribs.rel = "noopener noreferrer";
            return undefined;
        }

        if (domNode.name === "pre") {
            return renderCodeBlockFromPre(domNode);
        }

        return undefined;
    },
};

export function MarkdownHTMLConverter({ children, className, untrusted = false }: Readonly<MarkdownHTMLConverterProps>) {
    const rendered = useMemo(() => {
        if (!children) {
            return null;
        }
        try {
            const preprocessed = children.replace(/&laquo;/g, "«").replace(/&raquo;/g, "»");

            const rawHtml = (untrusted ? untrustedMarked.parse(preprocessed) : marked.parse(preprocessed, { gfm: true })) as string;

            const cleanHtml = DOMPurify.sanitize(rawHtml, untrusted ? UNTRUSTED_SANITIZE : { USE_PROFILES: { html: true } });

            return parse(cleanHtml, parserOptions);
        } catch {
            return children;
        }
    }, [children, untrusted]);

    if (rendered === null) {
        return null;
    }

    return <div className={getThemeHtmlStyles(className)}>{rendered}</div>;
}
