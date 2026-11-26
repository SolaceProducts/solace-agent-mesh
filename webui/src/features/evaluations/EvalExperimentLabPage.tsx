// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { ChevronDown, Plus, Sparkles, Upload } from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";

import { evaluationsApi } from "@/api/evaluations";

import { useUserContext } from "@/providers/contexts";

import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui/dropdown-menu";
import { Tabs, TabsContent } from "@/ui/tabs";

import { Header, PageLayout } from "@/common";

import { AiGenerationDialog, DatasetImportDialog } from "./components";
import { EVAL_ROUTES, isLabTab, type LabTab } from "./constants";
import { EvalDatasetsPage } from "./EvalDatasetsPage";
import { EvalEvaluatorsPage } from "./EvalEvaluatorsPage";
import { EvalExperimentsPage } from "./EvalExperimentsPage";
import { createUntitledDataset } from "./utils";

export function EvalExperimentLabPage() {
    const navigate = useNavigate();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canCreate = userHasCapability(CAPABILITIES.createEvaluations);
    const [searchParams, setSearchParams] = useSearchParams();
    const tabParam = searchParams.get("tab");
    const activeTab: LabTab = isLabTab(tabParam) ? tabParam : "experiments";

    const [importOpen, setImportOpen] = useState(false);
    const [aiGenerateOpen, setAiGenerateOpen] = useState(false);

    const setActiveTab = (tab: LabTab) => {
        const next = new URLSearchParams(searchParams);
        next.set("tab", tab);
        setSearchParams(next, { replace: true });
    };

    const handleCreateDataset = async () => {
        const created = await createUntitledDataset(evaluationsApi);
        navigate(`/evaluations/datasets/${created.id}?edit=true`);
    };

    const renderHeaderButton = () => {
        if (activeTab === "experiments") {
            return (
                <Button variant="ghost" className="no-underline hover:no-underline" onClick={() => navigate(EVAL_ROUTES.EXPERIMENT_NEW)}>
                    <Plus className="mr-1 size-4" /> Create Experiment
                </Button>
            );
        }
        if (activeTab === "datasets") {
            return (
                <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                        <Button variant="ghost" className="no-underline hover:no-underline">
                            Create New Dataset <ChevronDown className="ml-1 size-4" />
                        </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => setAiGenerateOpen(true)}>
                            <Sparkles className="mr-2 size-4" /> Generate with AI
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={() => setImportOpen(true)}>
                            <Upload className="mr-2 size-4" /> Import From File
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={() => void handleCreateDataset()}>
                            <Plus className="mr-2 size-4" /> Create New
                        </DropdownMenuItem>
                    </DropdownMenuContent>
                </DropdownMenu>
            );
        }
        return (
            <Button variant="ghost" className="no-underline hover:no-underline" onClick={() => navigate(EVAL_ROUTES.EVALUATOR_NEW)}>
                <Plus className="mr-1 size-4" /> Create Evaluator
            </Button>
        );
    };

    return (
        <PageLayout>
            <Header
                title="Experiment Lab"
                tabs={[
                    { id: "experiments", label: "Experiments", isActive: activeTab === "experiments", onClick: () => setActiveTab("experiments") },
                    { id: "datasets", label: "Datasets", isActive: activeTab === "datasets", onClick: () => setActiveTab("datasets") },
                    { id: "evaluators", label: "Evaluators", isActive: activeTab === "evaluators", onClick: () => setActiveTab("evaluators") },
                ]}
                buttons={canCreate ? [renderHeaderButton()] : []}
            />
            <Tabs value={activeTab} onValueChange={v => setActiveTab(v as LabTab)} className="flex min-w-0 flex-1 flex-col overflow-hidden">
                <TabsContent value="experiments" className="mt-0 flex-1 overflow-hidden">
                    <EvalExperimentsPage />
                </TabsContent>
                <TabsContent value="datasets" className="mt-0 flex-1 overflow-hidden">
                    <EvalDatasetsPage embedded />
                </TabsContent>
                <TabsContent value="evaluators" className="mt-0 flex-1 overflow-hidden">
                    <EvalEvaluatorsPage embedded />
                </TabsContent>
            </Tabs>
            <DatasetImportDialog open={importOpen} onClose={() => setImportOpen(false)} onImported={datasetId => navigate(`/evaluations/datasets/${datasetId}?edit=true&source=import`)} />
            <AiGenerationDialog mode="dataset" open={aiGenerateOpen} onClose={() => setAiGenerateOpen(false)} onGenerated={dataset => navigate(`/evaluations/datasets/${dataset.id}?edit=true&source=ai`)} />
        </PageLayout>
    );
}
