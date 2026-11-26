// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useState } from "react";
import { CalendarDays, Pencil, Sparkles } from "lucide-react";

import { useCreateScheduledTask, useUpdateScheduledTask, useValidateTaskConflict } from "@/api/scheduled-tasks";

import { useNavigationBlocker } from "@/common/dialogs";

import { browserTimezone } from "@/utils/browserTimezone";

import type { CreateScheduledTaskRequest, ScheduledTask, TargetType } from "@/types/scheduled-tasks";

import { Header, MessageBanner } from "@/common";
import { useIsMobile } from "@/hooks";
import { Button, Tabs, TabsList, TabsTrigger } from "@/ui";
import { cn } from "@/utils";

import { type ConflictState, ManualTaskForm } from "./ManualTaskForm";
import { parseInterval, type TaskConfig } from "./task-config";
import { TaskBuilderChat } from "./TaskBuilderChat";
import { validateTaskConfig } from "./taskConfigValidation";
import { TaskFormFooter } from "./TaskFormFooter";
import { TaskPreviewPanel } from "./TaskPreviewPanel";
import { useSchedulableTargets } from "./useSchedulableTargets";
import { messageAfterTargetChange, structuredInputSchema } from "./workflowInput";

interface TaskTemplateBuilderProps {
    onBack: () => void;
    onSuccess?: (taskId?: string) => void;
    initialMessage?: string | null;
    initialMode?: "manual" | "ai-assisted";
    editingTask?: ScheduledTask | null;
    isEditing?: boolean;
}

function useConflictAdvisory(config: TaskConfig, validate: ReturnType<typeof useValidateTaskConflict>): ConflictState {
    const [conflictError, setConflictError] = useState<ConflictState>(null);
    useEffect(() => {
        const instructions = config.taskMessage.trim();
        const expr = config.scheduleExpression.trim();
        const scheduleReady = config.scheduleType === "interval" ? parseInterval(expr) !== null : expr.length > 0;
        if (!instructions || !scheduleReady) {
            setConflictError(null);
            return;
        }
        let stale = false;
        const controller = new AbortController();
        const handle = setTimeout(() => {
            validate.mutate(
                { instructions, scheduleType: config.scheduleType, scheduleExpression: expr, timezone: config.timezone, targetAgent: config.targetAgentName, signal: controller.signal },
                {
                    onSuccess: result => {
                        if (stale) return;
                        setConflictError(
                            result.conflict ? { reason: result.reason || "These instructions look out of step with this schedule.", fields: result.affectedFields.length > 0 ? result.affectedFields : ["instructions", "schedule"] } : null
                        );
                    },
                    onError: () => {
                        if (stale) return;
                        setConflictError(null);
                    },
                }
            );
        }, 800);
        return () => {
            stale = true;
            controller.abort();
            clearTimeout(handle);
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [config.taskMessage, config.scheduleType, config.scheduleExpression, config.timezone, config.targetAgentName]);
    return conflictError;
}

export const TaskTemplateBuilder: React.FC<TaskTemplateBuilderProps> = ({ onBack, onSuccess, initialMessage, initialMode = "ai-assisted", editingTask, isEditing = false }) => {
    const [builderMode, setBuilderMode] = useState<"manual" | "ai-assisted">(initialMode);
    const isMobile = useIsMobile();
    const [mobileAiPane, setMobileAiPane] = useState<"builder" | "preview">("builder");
    const [highlightedFields, setHighlightedFields] = useState<string[]>([]);
    const [validationErrors, setValidationErrors] = useState<Record<string, string>>({});
    const [pendingAction, setPendingAction] = useState<"create" | "create-activate" | "save" | null>(null);
    const { targets: agents, allTargets } = useSchedulableTargets();
    const createTaskMutation = useCreateScheduledTask();
    const updateTaskMutation = useUpdateScheduledTask();
    const validateConflictMutation = useValidateTaskConflict();
    const isLoading = createTaskMutation.isPending || updateTaskMutation.isPending;

    const [initialConfig, setInitialConfig] = useState<TaskConfig | null>(null);
    const { allowNavigation, NavigationBlocker, setBlockingEnabled } = useNavigationBlocker();

    const [config, setConfig] = useState<TaskConfig>({
        name: "",
        description: "",
        scheduleType: "cron",
        scheduleExpression: "",
        targetType: "agent",
        targetAgentName: "",
        taskMessage: "",
        timezone: browserTimezone() ?? "UTC",
        enabled: true,
    });

    const inputSchemaOf = (targetType: TargetType, targetName: string) => (targetType === "workflow" ? structuredInputSchema(allTargets.find(card => card.name === targetName)?.inputSchema) : undefined);
    const workflowInputSchema = inputSchemaOf(config.targetType, config.targetAgentName);

    const taskMessageAfterTargetChange = (nextTargetType: TargetType, nextTargetName: string): string => messageAfterTargetChange(config.taskMessage, workflowInputSchema, inputSchemaOf(nextTargetType, nextTargetName));

    const [scheduleExprByType, setScheduleExprByType] = useState<Record<TaskConfig["scheduleType"], string>>({
        cron: "0 9 * * *",
        interval: "30m",
        one_time: "",
    });

    useEffect(() => {
        setScheduleExprByType(prev => (prev[config.scheduleType] === config.scheduleExpression ? prev : { ...prev, [config.scheduleType]: config.scheduleExpression }));
    }, [config.scheduleType, config.scheduleExpression]);

    useEffect(() => {
        if (editingTask && isEditing) {
            const initialData: TaskConfig = {
                name: editingTask.name,
                description: editingTask.description || "",
                scheduleType: editingTask.scheduleType,
                scheduleExpression: editingTask.scheduleExpression,
                targetType: editingTask.targetType || "agent",
                targetAgentName: editingTask.targetAgentName,
                taskMessage: editingTask.taskMessage?.[0]?.text || "",
                timezone: editingTask.timezone,
                enabled: editingTask.enabled,
            };
            setConfig(initialData);
            setInitialConfig(initialData);
        } else {
            setInitialConfig({
                name: "",
                description: "",
                scheduleType: "cron",
                scheduleExpression: "",
                targetType: "agent",
                targetAgentName: "",
                taskMessage: "",
                timezone: browserTimezone() ?? "UTC",
                enabled: true,
            });
        }
    }, [editingTask, isEditing]);

    useEffect(() => {
        if (!initialConfig) {
            setBlockingEnabled(false);
            return;
        }

        const hasContent = !!(config.name?.trim() || config.description?.trim() || config.taskMessage?.trim());

        if (!hasContent) {
            setBlockingEnabled(false);
            return;
        }

        const hasUnsavedChanges =
            config.name !== initialConfig.name ||
            config.description !== initialConfig.description ||
            config.scheduleType !== initialConfig.scheduleType ||
            config.scheduleExpression !== initialConfig.scheduleExpression ||
            config.targetType !== initialConfig.targetType ||
            config.targetAgentName !== initialConfig.targetAgentName ||
            config.taskMessage !== initialConfig.taskMessage ||
            config.timezone !== initialConfig.timezone ||
            config.enabled !== initialConfig.enabled;

        setBlockingEnabled(hasUnsavedChanges);
    }, [config, initialConfig, setBlockingEnabled]);

    const updateConfig = (updates: Partial<TaskConfig>) => {
        setConfig(prev => ({ ...prev, ...updates }));

        const changedFields = Object.keys(updates);
        setHighlightedFields(changedFields);

        setValidationErrors(prev => {
            const newErrors = { ...prev };
            changedFields.forEach(field => delete newErrors[field]);
            return newErrors;
        });
    };

    const handleConfigUpdate = (updates: Record<string, unknown>) => {
        const taskUpdates: Partial<TaskConfig> = {};

        if (updates.name) taskUpdates.name = String(updates.name);
        if (updates.description) taskUpdates.description = String(updates.description);
        if (updates.scheduleType || updates.schedule_type) taskUpdates.scheduleType = String(updates.scheduleType || updates.schedule_type).toLowerCase() as "cron" | "interval" | "one_time";
        if (updates.scheduleExpression || updates.schedule_expression) taskUpdates.scheduleExpression = String(updates.scheduleExpression || updates.schedule_expression);
        if (updates.targetType || updates.target_type) taskUpdates.targetType = (updates.targetType || updates.target_type) as TargetType;
        if (updates.targetAgentName !== undefined || updates.target_agent_name !== undefined) {
            const next = updates.targetAgentName ?? updates.target_agent_name;
            taskUpdates.targetAgentName = next == null ? "" : String(next);
        }
        if (updates.taskMessage || updates.task_message) taskUpdates.taskMessage = String(updates.taskMessage || updates.task_message);
        if (updates.timezone) taskUpdates.timezone = String(updates.timezone);

        const changedFields = Object.keys(taskUpdates).filter(key => {
            const oldValue = (config as unknown as Record<string, unknown>)[key];
            const newValue = (taskUpdates as unknown as Record<string, unknown>)[key];

            const normalizedOld = oldValue === undefined || oldValue === null || oldValue === "" ? "" : oldValue;
            const normalizedNew = newValue === undefined || newValue === null || newValue === "" ? "" : newValue;

            return normalizedOld !== normalizedNew;
        });

        setConfig(prev => ({ ...prev, ...taskUpdates }));

        setHighlightedFields(changedFields);

        setValidationErrors(prev => {
            const newErrors = { ...prev };
            changedFields.forEach(field => delete newErrors[field]);
            return newErrors;
        });
    };

    const validateConfig = (): boolean => {
        const errors = validateTaskConfig(config, workflowInputSchema);
        setValidationErrors(errors);
        return Object.keys(errors).length === 0;
    };

    const handleClose = (skipCheck = false) => {
        if (skipCheck) {
            allowNavigation(() => {
                setInitialConfig(null);
                onBack();
            });
        } else {
            onBack();
        }
    };

    const conflictError = useConflictAdvisory(config, validateConflictMutation);

    const handleSave = async (activate?: boolean) => {
        if (!validateConfig()) {
            return;
        }

        const action: "create" | "create-activate" | "save" = isEditing ? "save" : activate ? "create-activate" : "create";
        setPendingAction(action);

        const enabled = isEditing ? config.enabled : activate === true;

        try {
            const taskData: CreateScheduledTaskRequest = {
                name: config.name,
                description: config.description,
                scheduleType: config.scheduleType,
                scheduleExpression: config.scheduleExpression,
                timezone: config.timezone,
                targetAgentName: config.targetAgentName,
                targetType: config.targetType,
                taskMessage: [{ type: "text", text: config.taskMessage }],
                enabled,
                timeoutSeconds: editingTask?.timeoutSeconds || 3600,
            };

            let savedTask;
            if (isEditing && editingTask) {
                savedTask = await updateTaskMutation.mutateAsync({ taskId: editingTask.id, updates: taskData });
            } else {
                savedTask = await createTaskMutation.mutateAsync(taskData);
            }

            allowNavigation(() => {
                handleClose(true);
                if (onSuccess) {
                    onSuccess(savedTask.id);
                }
            });
        } catch (error) {
            const errorMsg = error instanceof Error ? error.message : `An error occurred while ${isEditing ? "updating" : "creating"} the task`;
            setValidationErrors({ general: errorMsg });
        } finally {
            setPendingAction(null);
        }
    };

    const handleSwitchToManual = () => {
        setBuilderMode("manual");
        setHighlightedFields([]);
    };

    const handleSwitchToAI = () => {
        setBuilderMode("ai-assisted");
        setHighlightedFields([]);
    };

    const hasValidationErrors = Object.keys(validationErrors).length > 0;
    const validationErrorMessages = Object.values(validationErrors).filter(Boolean);

    const targetNoun = config.targetType === "workflow" ? "workflow" : "agent";
    const targetOptions: Array<{ name: string; displayName?: string }> = agents.filter(agent => (config.targetType === "workflow" ? agent.isWorkflow : !agent.isWorkflow)).map(({ name, displayName }) => ({ name, displayName }));
    const savedTarget = config.targetAgentName;
    if (savedTarget && !targetOptions.some(option => option.name === savedTarget)) {
        targetOptions.push({ name: savedTarget, displayName: allTargets.find(card => card.name === savedTarget)?.displayName ?? savedTarget });
    }

    return (
        <div className="flex h-full flex-col">
            <Header
                title={isEditing ? "Edit Scheduled Task" : "Create Scheduled Task"}
                breadcrumbs={[{ label: "Scheduled Tasks", onClick: () => handleClose() }, { label: isEditing ? "Edit Task" : "Create Task" }]}
                buttons={
                    builderMode === "ai-assisted"
                        ? [
                              <Button key="edit-manually" onClick={handleSwitchToManual} variant="ghost" size="sm">
                                  <Pencil className="mr-1 h-3 w-3" />
                                  Edit Manually
                              </Button>,
                          ]
                        : [
                              <Button key="build-with-ai" onClick={handleSwitchToAI} variant="ghost" size="sm">
                                  <Sparkles className="mr-1 h-3 w-3" />
                                  {isEditing ? "Edit with AI" : "Build with AI"}
                              </Button>,
                          ]
                }
            />

            {hasValidationErrors && (
                <div className="px-8 py-3">
                    <MessageBanner
                        variant="error"
                        message={
                            validationErrorMessages.length === 1 ? (
                                <span>{validationErrorMessages[0]}</span>
                            ) : (
                                <div>
                                    <p className="font-semibold">Please fix the following:</p>
                                    <ul className="mt-1 ml-5 list-disc">
                                        {validationErrorMessages.map((msg, i) => (
                                            <li key={i}>{msg}</li>
                                        ))}
                                    </ul>
                                </div>
                            )
                        }
                    />
                </div>
            )}

            {builderMode === "ai-assisted" && isMobile && (
                <Tabs value={mobileAiPane} onValueChange={v => setMobileAiPane(v as "builder" | "preview")} className="border-b px-4 py-2">
                    <TabsList className="w-full bg-transparent p-0">
                        <TabsTrigger value="builder" className="rounded-none rounded-l-md px-4">
                            <Sparkles className="h-4 w-4 shrink-0" />
                            AI Builder
                        </TabsTrigger>
                        <TabsTrigger value="preview" className="rounded-none rounded-r-md border-l-0 px-4">
                            <CalendarDays className="h-4 w-4 shrink-0" />
                            Preview
                        </TabsTrigger>
                    </TabsList>
                </Tabs>
            )}

            <div className="flex min-h-0 flex-1">
                <div className={cn("w-full overflow-hidden border-r sm:w-[40%]", builderMode === "manual" && "hidden", builderMode === "ai-assisted" && isMobile && mobileAiPane !== "builder" && "hidden")}>
                    <TaskBuilderChat onConfigUpdate={handleConfigUpdate} currentConfig={config} initialMessage={initialMessage} availableAgents={agents} isEditing={isEditing} />
                </div>

                {builderMode === "ai-assisted" && (
                    <div className={cn("w-full overflow-hidden sm:w-[60%]", isMobile && mobileAiPane !== "preview" && "hidden")}>
                        <TaskPreviewPanel config={config} highlightedFields={highlightedFields} />
                    </div>
                )}

                {builderMode === "manual" && (
                    <ManualTaskForm
                        config={config}
                        validationErrors={validationErrors}
                        conflictError={conflictError}
                        scheduleExprByType={scheduleExprByType}
                        targetOptions={targetOptions}
                        targetNoun={targetNoun}
                        workflowInputSchema={workflowInputSchema}
                        updateConfig={updateConfig}
                        taskMessageAfterTargetChange={taskMessageAfterTargetChange}
                    />
                )}
            </div>
            <NavigationBlocker />
            <TaskFormFooter isEditing={isEditing} isLoading={isLoading} pendingAction={pendingAction} onCancel={() => handleClose()} onSave={handleSave} />
        </div>
    );
};
