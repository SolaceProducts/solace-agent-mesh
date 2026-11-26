// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { AlertTriangle, CheckCircle2, Search } from "lucide-react";

import { gatewaysApi } from "@/api/gateways";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Input } from "@/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import type { WhatsAppMessageTemplate, WhatsAppTemplateBlocker } from "@/types";

const BLOCKER_TEXT: Record<WhatsAppTemplateBlocker, string> = {
    no_contact_button: "No Share Contact Info button. Add one in WhatsApp Manager, or pick another template.",
    has_variables: "Expects a parameter. This entrypoint sends the template with no values, so Meta rejects it — delete the variable from the template body.",
    not_approved: "Not approved by Meta. Only an approved template can be sent.",
};

const CONTENT_PREVIEW_CHARS = 75;

function contentPreview(body: string): string {
    const oneLine = body.replaceAll(/\s+/g, " ").trim();
    const chars = Array.from(oneLine);
    return chars.length > CONTENT_PREVIEW_CHARS ? `${chars.slice(0, CONTENT_PREVIEW_CHARS).join("")}…` : oneLine;
}

interface WhatsAppTemplateDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    values: Record<string, unknown>;
    gatewayId?: string;
    onSelect: (name: string, language: string) => void;
}

export const WhatsAppTemplateDialog = ({ open, onOpenChange, values, gatewayId, onSelect }: WhatsAppTemplateDialogProps) => {
    const [businessAccountId, setBusinessAccountId] = useState("");
    const [templates, setTemplates] = useState<WhatsAppMessageTemplate[] | null>(null);
    const [message, setMessage] = useState("");
    const [hasMore, setHasMore] = useState(false);
    const [isLoading, setIsLoading] = useState(false);

    const trimmedId = businessAccountId.trim();

    const handleLookup = async () => {
        if (!trimmedId || isLoading) return;
        setIsLoading(true);
        setMessage("");
        setTemplates(null);
        try {
            const response = await gatewaysApi.getWhatsAppMessageTemplates({ businessAccountId: trimmedId, values, gatewayId });
            setTemplates(response.data);
            setHasMore(Boolean(response.hasMore));
        } catch (error) {
            setMessage(error instanceof Error ? error.message : "Could not reach the platform to look up templates.");
        } finally {
            setIsLoading(false);
        }
    };

    const handlePick = (template: WhatsAppMessageTemplate) => {
        onSelect(template.name, template.language);
        onOpenChange(false);
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-5xl" data-testid="whatsappTemplateDialog">
                <DialogHeader>
                    <DialogTitle>Find a Contact Request Template</DialogTitle>
                    <DialogDescription>Lists the message templates on your WhatsApp Business Account and shows which can be used to ask a sender for their phone number. Selecting one fills in the template name and language.</DialogDescription>
                </DialogHeader>

                <div>
                    <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
                        <div className="min-w-0 flex-1">
                            <label className="mb-1 block text-sm font-medium" htmlFor="whatsappBusinessAccountId">
                                WhatsApp Business Account ID
                            </label>
                            <Input
                                id="whatsappBusinessAccountId"
                                data-testid="whatsappBusinessAccountId"
                                value={businessAccountId}
                                placeholder="123456789012345"
                                onChange={event => setBusinessAccountId(event.target.value)}
                                onKeyDown={event => {
                                    if (event.key === "Enter") {
                                        event.preventDefault();
                                        void handleLookup();
                                    }
                                }}
                            />
                        </div>
                        <Button className="w-full sm:w-auto" testid="lookUpWhatsappTemplates" onClick={() => void handleLookup()} disabled={!trimmedId || isLoading} loading={isLoading}>
                            <Search />
                            Look Up
                        </Button>
                    </div>
                    <p className="mt-1 text-xs text-(--secondary-text-wMain)">Found in WhatsApp Manager under your account settings. This is not the Phone Number ID.</p>
                </div>

                {message && (
                    <p className="text-sm text-(--error-wMain)" data-testid="whatsappTemplateLookupMessage">
                        {message}
                    </p>
                )}

                {templates?.length === 0 && <p className="text-sm text-(--secondary-text-wMain)">This account has no message templates yet. Create one in WhatsApp Manager, then look up again.</p>}

                {templates && templates.length > 0 && (
                    <div className="flex min-h-0 flex-1 flex-col">
                        <div className="min-h-0 flex-1 overflow-y-auto">
                            <Table>
                                <TableHeader sticky>
                                    <TableRow>
                                        <TableHead className="w-24">Usable</TableHead>
                                        <TableHead>Template</TableHead>
                                        <TableHead className="w-24">Language</TableHead>
                                        <TableHead>Content</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {templates.map(template => (
                                        <TableRow
                                            key={`${template.name}:${template.language}`}
                                            data-testid={`whatsappTemplateRow-${template.name}`}
                                            role={template.usable ? "button" : undefined}
                                            tabIndex={template.usable ? 0 : undefined}
                                            aria-label={template.usable ? `Use template ${template.name}` : undefined}
                                            aria-disabled={template.usable ? undefined : true}
                                            className={template.usable ? "cursor-pointer hover:bg-(--secondary-w10)" : undefined}
                                            onClick={template.usable ? () => handlePick(template) : undefined}
                                            onKeyDown={
                                                template.usable
                                                    ? event => {
                                                          if (event.key === "Enter" || event.key === " ") {
                                                              event.preventDefault();
                                                              handlePick(template);
                                                          }
                                                      }
                                                    : undefined
                                            }
                                        >
                                            <TableCell>
                                                {template.usable ? (
                                                    <span className="flex items-center gap-1 text-(--success-wMain)">
                                                        <CheckCircle2 className="h-4 w-4" />
                                                        Yes
                                                    </span>
                                                ) : (
                                                    <Tooltip>
                                                        <TooltipTrigger asChild>
                                                            <span className="flex cursor-help items-center gap-1 text-(--secondary-text-wMain)" data-testid={`whatsappTemplateBlocked-${template.name}`}>
                                                                <AlertTriangle className="h-4 w-4" />
                                                                No
                                                            </span>
                                                        </TooltipTrigger>
                                                        <TooltipContent className="max-w-80">
                                                            <ul className="list-inside list-disc space-y-1">
                                                                {template.blockers.map(blocker => (
                                                                    <li key={blocker}>{BLOCKER_TEXT[blocker] ?? blocker}</li>
                                                                ))}
                                                            </ul>
                                                        </TooltipContent>
                                                    </Tooltip>
                                                )}
                                            </TableCell>
                                            <TableCell className="font-medium break-all">{template.name}</TableCell>
                                            <TableCell>{template.language}</TableCell>
                                            <TableCell>
                                                <Tooltip>
                                                    <TooltipTrigger asChild>
                                                        <span className="text-xs text-(--secondary-text-wMain)" data-testid={`whatsappTemplateBody-${template.name}`}>
                                                            {contentPreview(template.body)}
                                                        </span>
                                                    </TooltipTrigger>
                                                    <TooltipContent className="max-w-96 whitespace-pre-wrap">{template.body}</TooltipContent>
                                                </Tooltip>
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </div>
                        {hasMore && <p className="mt-2 text-xs text-(--secondary-text-wMain)">This account has more templates than are shown. Narrow the list down in WhatsApp Manager, or type the name in directly.</p>}
                    </div>
                )}

                <DialogFooter>
                    <Button variant="outline" testid="closeWhatsappTemplateDialog" onClick={() => onOpenChange(false)}>
                        Cancel
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};
