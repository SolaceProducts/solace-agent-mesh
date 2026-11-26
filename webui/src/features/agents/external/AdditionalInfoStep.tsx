// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Edit, Plus, RotateCcw } from "lucide-react";
import type { UseFormReturn } from "react-hook-form";
import { FormProvider, useForm } from "react-hook-form";
import { z } from "zod";

import type { RemoteAgentProtocolPin, RemoteAgentResponse } from "@/api/remote-agents";

import { SAFE_NAME_DESCRIPTION, SAFE_NAME_PATTERN, SAFE_TEXT_DESCRIPTION, SAFE_TEXT_PATTERN } from "@/common/form";
import { PageSection } from "@/common/layout";

import { Button } from "@/ui/button";
import { Input } from "@/ui/input";

import { KeyValuePairList } from "@/common";
import type { AgentSkillFE } from "@/types";

import { useDiscoveredAgents, useDuplicateNameChecker } from "../hooks";
import { createHeaderValidation } from "../utils/validationUtils";

import { AgentInfoSection, CommunicationModesSection, SkillsSection } from "./AgentCardFormSections";
import { AUTH_TYPE_OPTIONS, AUTH_TYPES, createAuthValidationSchema, OAuth2AuthFields, StaticApiKeyAuthFields, StaticBearerAuthFields } from "./AuthenticationFields";
import { useTrackAuthStoredSecrets } from "./authSecretTracking";
import { parseTrustedKeySetUrls, PROTOCOL_PINS, trustedKeySetUrlsProblem } from "./cardTrust";
import { A2AProtocolSection } from "./CardTrustSections";
import { FormField, SelectField, TextInput } from "./FormComponents";

export type AdditionalInfoFormData = {
    externalAgentName: string;
    solaceDisplayName: string;
    taskAuthenticationType?: string;
    taskAuthBearerToken?: string;
    taskAuthApikeyValue?: string;
    taskAuthOauth2TokenUrl?: string;
    taskAuthOauth2ClientId?: string;
    taskAuthOauth2ClientSecret?: string;
    taskAuthOauth2Scope?: string;
    taskHeaders: { key: string; value: string }[];
    agentName?: string;
    agentDescription?: string;
    skills: AgentSkillFE[];
    inputModes: string[];
    outputModes: string[];
    skillName?: string;
    skillDescription?: string;
    protocolVersion: RemoteAgentProtocolPin;
    trustedCardJwksUrls: string;
    requireSignedCard: boolean;
};

interface AdditionalInfoStepProps {
    agentCardLocation?: string;
    fetchedAgentCard?: import("@/types").ExternalAgentCard | null;
    onFormReady?: (methods: UseFormReturn<AdditionalInfoFormData>) => void;
    defaultValues?: Partial<AdditionalInfoFormData>;
    onChange?: () => void;
    initialRemoteAgent?: RemoteAgentResponse;
}

const createAdditionalInfoSchema = (authType?: string, hasAgentCard?: boolean, hasPreservableSecret: boolean = false) => {
    const baseSchema = z.object({
        externalAgentName: hasAgentCard ? z.string().min(1, "External Agent Name is required") : z.string().optional(),
        solaceDisplayName: hasAgentCard
            ? z
                  .string()
                  .transform(val => val.trim())
                  .pipe(
                      z
                          .string()
                          .min(3, "Name must be at least 3 characters")
                          .max(40, "Name must be 40 characters or less")
                          .refine(val => SAFE_TEXT_PATTERN.test(val), {
                              message: SAFE_TEXT_DESCRIPTION,
                          })
                  )
            : z.string().optional(),
        taskAuthenticationType: z.string().min(1, "Authentication Type is required"),
        taskHeaders: createHeaderValidation(),
        protocolVersion: z.enum(PROTOCOL_PINS as [RemoteAgentProtocolPin, ...RemoteAgentProtocolPin[]]),
        trustedCardJwksUrls: z.string().optional(),
        requireSignedCard: z.boolean().optional(),
    });

    const agentInfoSchema = !hasAgentCard
        ? {
              agentName: z
                  .string()
                  .transform(val => val.trim())
                  .pipe(
                      z
                          .string()
                          .min(3, "Name must be at least 3 characters")
                          .max(40, "Name must be 40 characters or less")
                          .refine(val => SAFE_TEXT_PATTERN.test(val), {
                              message: SAFE_TEXT_DESCRIPTION,
                          })
                  ),
              agentDescription: z
                  .string()
                  .transform(val => val.trim())
                  .pipe(
                      z
                          .string()
                          .min(10, "Value must be at least 10 characters")
                          .max(1000, "Value must be 1000 characters or less")
                          .refine(val => SAFE_TEXT_PATTERN.test(val), {
                              message: SAFE_TEXT_DESCRIPTION,
                          })
                  ),
              skillName: z
                  .string()
                  .transform(val => val.trim())
                  .pipe(
                      z
                          .string()
                          .max(40, "Name must be 40 characters or less")
                          .refine(val => val === "" || SAFE_NAME_PATTERN.test(val), {
                              message: SAFE_NAME_DESCRIPTION,
                          })
                  )
                  .optional(),
              skillDescription: z
                  .string()
                  .transform(val => val.trim())
                  .pipe(
                      z
                          .string()
                          .max(250, "Value must be 250 characters or less")
                          .refine(val => val === "" || SAFE_TEXT_PATTERN.test(val), {
                              message: SAFE_TEXT_DESCRIPTION,
                          })
                  )
                  .optional(),
          }
        : {};

    const authValidation = createAuthValidationSchema(authType, "taskAuth", hasPreservableSecret);
    return baseSchema.extend({ ...agentInfoSchema, ...authValidation }).superRefine((data, ctx) => {
        if (!hasAgentCard) return;
        const problem = trustedKeySetUrlsProblem(data.trustedCardJwksUrls);
        if (problem) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, path: ["trustedCardJwksUrls"], message: problem });
        } else if (data.requireSignedCard && parseTrustedKeySetUrls(data.trustedCardJwksUrls).length === 0) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, path: ["trustedCardJwksUrls"], message: "Trusted JWKS URL required. Enter at least one URL." });
        }
    });
};

const EMPTY_ADDITIONAL_INFO_FORM: AdditionalInfoFormData = {
    externalAgentName: "",
    solaceDisplayName: "",
    taskAuthenticationType: "",
    taskAuthBearerToken: "",
    taskAuthApikeyValue: "",
    taskAuthOauth2TokenUrl: "",
    taskAuthOauth2ClientId: "",
    taskAuthOauth2ClientSecret: "",
    taskAuthOauth2Scope: "",
    taskHeaders: [{ key: "", value: "" }],
    agentName: "",
    agentDescription: "",
    skills: [],
    inputModes: [],
    outputModes: [],
    skillName: "",
    skillDescription: "",
    protocolVersion: "auto",
    trustedCardJwksUrls: "",
    requireSignedCard: false,
};

export const AdditionalInfoStep = ({ agentCardLocation, fetchedAgentCard, onFormReady, defaultValues, onChange, initialRemoteAgent }: AdditionalInfoStepProps) => {
    const storedSecrets = useTrackAuthStoredSecrets(initialRemoteAgent?.agentCardAuthConfig, initialRemoteAgent?.agentAuthConfig);

    const initiallyCustomized = !!defaultValues?.solaceDisplayName && defaultValues.solaceDisplayName !== (fetchedAgentCard?.name ?? "");
    const [isDisplayNameEditable, setIsDisplayNameEditable] = useState(initiallyCustomized);
    const displayNameInputRef = useRef<HTMLInputElement>(null);
    const [duplicateWarning, setDuplicateWarning] = useState<string | null>(null);
    const [initialNameRef, setInitialNameRef] = useState<string>("");

    const hasAgentCard = agentCardLocation !== "none";

    const { discoveredAgents } = useDiscoveredAgents();

    const checkForDuplicate = useDuplicateNameChecker(discoveredAgents, initialNameRef);

    const externalAgentName = fetchedAgentCard?.name || "";

    const methods = useForm<AdditionalInfoFormData>({
        resolver: async (data, context, options) => {
            const authType = data.taskAuthenticationType;
            const storedType = initialRemoteAgent?.agentAuthConfig?.type;
            const schema = createAdditionalInfoSchema(authType, hasAgentCard, storedSecrets.taskAuth.size > 0 && authType === storedType);
            const resolver = zodResolver(schema);
            return resolver(data as any, context, options as any) as any;
        },
        defaultValues: {
            ...EMPTY_ADDITIONAL_INFO_FORM,
            ...defaultValues,
            externalAgentName: externalAgentName || defaultValues?.externalAgentName || "",
            solaceDisplayName: defaultValues?.solaceDisplayName || externalAgentName || "",
        },
        mode: "onBlur",
        reValidateMode: "onBlur",
    });

    const {
        register,
        control,
        watch,
        trigger,
        setValue,
        getValues,
        formState: { errors },
    } = methods;

    const hasAgentCardRef = useRef(hasAgentCard);
    const defaultValuesRef = useRef(defaultValues);

    useEffect(() => {
        const initialName = hasAgentCardRef.current ? defaultValuesRef.current?.solaceDisplayName : defaultValuesRef.current?.agentName;
        if (initialName) {
            setInitialNameRef(initialName);
        }
    }, []);

    const taskAuthenticationType = watch("taskAuthenticationType");

    useEffect(() => {
        const subscription = watch(() => {
            onChange?.();
        });
        return () => subscription.unsubscribe();
    }, [onChange]);

    const handleCheckForDuplicate = useCallback(() => {
        const nameToCheck = hasAgentCard ? getValues("solaceDisplayName") : getValues("agentName");

        if (!nameToCheck?.trim()) {
            setDuplicateWarning(null);
            return;
        }

        const result = checkForDuplicate(nameToCheck);
        if (result.isDuplicate) {
            setDuplicateWarning("This name is already being used in the agent mesh. Unique names make it easier for others in your organization to find the correct agent.");
        } else {
            setDuplicateWarning(null);
        }
    }, [hasAgentCard, getValues, checkForDuplicate]);

    const handleCustomize = useCallback(() => {
        setIsDisplayNameEditable(true);
        setTimeout(() => {
            displayNameInputRef.current?.focus();
        }, 0);
    }, []);

    const handleResetToDefault = useCallback(() => {
        setValue("solaceDisplayName", externalAgentName, { shouldValidate: true, shouldDirty: true, shouldTouch: true });
        setIsDisplayNameEditable(false);
        setTimeout(() => {
            handleCheckForDuplicate();
        }, 0);
    }, [externalAgentName, setValue, handleCheckForDuplicate]);

    const handleAddHeader = useCallback(() => {
        const currentHeaders = getValues("taskHeaders");
        setValue("taskHeaders", [...currentHeaders, { key: "", value: "" }]);
    }, [getValues, setValue]);

    const handleDisplayNameBlur = useCallback(async () => {
        await trigger("solaceDisplayName");
        handleCheckForDuplicate();
    }, [trigger, handleCheckForDuplicate]);

    const handleAgentNameBlur = useCallback(async () => {
        await trigger("agentName");
        handleCheckForDuplicate();
    }, [trigger, handleCheckForDuplicate]);

    const { ref: registerRef, ...registerRest } = register("solaceDisplayName");
    const mergedRef = useCallback(
        (instance: HTMLInputElement | null) => {
            registerRef(instance);
            displayNameInputRef.current = instance;
        },
        [registerRef]
    );

    useEffect(() => {
        if (fetchedAgentCard?.name && hasAgentCard) {
            const currentExternalName = getValues("externalAgentName");
            const currentDisplayName = getValues("solaceDisplayName");

            if (!currentExternalName || currentExternalName !== fetchedAgentCard.name) {
                setValue("externalAgentName", fetchedAgentCard.name, { shouldValidate: true });

                if (!currentDisplayName || currentDisplayName === currentExternalName) {
                    setValue("solaceDisplayName", fetchedAgentCard.name, { shouldValidate: true });
                }
            }
        }
    }, [fetchedAgentCard, hasAgentCard, getValues, setValue]);

    useEffect(() => {
        if (onFormReady) {
            onFormReady(methods);
        }
    }, [methods, onFormReady]);

    useEffect(() => {
        handleCheckForDuplicate();
    }, [handleCheckForDuplicate]);

    return (
        <FormProvider {...methods}>
            <form className="flex max-w-1/2 min-w-[700px] flex-col gap-8" autoComplete="off">
                <PageSection>
                    <div className="flex items-start justify-between">
                        <div>
                            <h2 className="text-xl font-semibold">Configure Agent Invocation</h2>
                            <p>Provide additional details for how the agent mesh should invoke your external agent.</p>
                        </div>
                    </div>
                </PageSection>

                {hasAgentCard ? (
                    <PageSection>
                        <div className="font-semibold">Agent Display Name</div>
                        <div className="pb-2 text-sm text-(--secondary-text-wMain)">By default, the agent display name is the same as your external agent name. You can customize this.</div>

                        <div className="space-y-4">
                            <TextInput name="externalAgentName" label="External Agent Name" register={register} error={errors.externalAgentName} required disabled />

                            <FormField
                                label="Solace Display Name"
                                required
                                error={errors.solaceDisplayName}
                                warning={duplicateWarning || undefined}
                                helpText={!duplicateWarning ? "The agent will be displayed with this name instead of its original name." : undefined}
                            >
                                <div className="flex items-center gap-2">
                                    <Input
                                        {...registerRest}
                                        ref={mergedRef}
                                        type="text"
                                        autoComplete="off"
                                        disabled={!isDisplayNameEditable}
                                        onBlur={handleDisplayNameBlur}
                                        aria-invalid={!!errors.solaceDisplayName}
                                        className="flex-1"
                                        data-testid="externalAgentInput-solaceDisplayName"
                                    />
                                    {!isDisplayNameEditable && (
                                        <Button type="button" variant="secondary" onClick={handleCustomize} data-testid="externalAgentDisplayNameCustomize">
                                            <Edit className="mr-2 size-4" />
                                            Customize
                                        </Button>
                                    )}
                                    {isDisplayNameEditable && (
                                        <Button type="button" variant="secondary" onClick={handleResetToDefault} data-testid="externalAgentDisplayNameReset">
                                            <RotateCcw className="mr-2 size-4" />
                                            Reset to Default
                                        </Button>
                                    )}
                                </div>
                            </FormField>
                        </div>
                    </PageSection>
                ) : (
                    <>
                        <PageSection>
                            <div className="font-semibold">Agent Info</div>
                            <div className="pb-2 text-sm text-(--secondary-text-wMain)">Provide information about your agent so others can understand when to use it.</div>
                            <AgentInfoSection nameFieldName="agentName" descriptionFieldName="agentDescription" onNameBlur={handleAgentNameBlur} nameWarning={!hasAgentCard ? duplicateWarning || undefined : undefined} />
                        </PageSection>

                        <PageSection>
                            <SkillsSection />
                        </PageSection>

                        <PageSection>
                            <CommunicationModesSection />
                        </PageSection>
                    </>
                )}

                <PageSection>
                    <div className="font-semibold">Authentication Details</div>
                    <div className="text-sm text-(--secondary-text-wMain)">Choose the way you want the agent mesh to connect to your agent.</div>

                    <div className="space-y-4">
                        <SelectField name="taskAuthenticationType" label="Authentication Type" control={control} options={AUTH_TYPE_OPTIONS} error={errors.taskAuthenticationType} required trigger={trigger} />

                        {taskAuthenticationType === AUTH_TYPES.BEARER && <StaticBearerAuthFields control={control} register={register} errors={errors} fieldPrefix="taskAuth" storedSecrets={storedSecrets} authScope="taskAuth" />}

                        {taskAuthenticationType === AUTH_TYPES.APIKEY && <StaticApiKeyAuthFields control={control} register={register} errors={errors} fieldPrefix="taskAuth" storedSecrets={storedSecrets} authScope="taskAuth" />}

                        {taskAuthenticationType === AUTH_TYPES.OAUTH2 && <OAuth2AuthFields control={control} register={register} errors={errors} fieldPrefix="taskAuth" trigger={trigger} storedSecrets={storedSecrets} authScope="taskAuth" />}
                    </div>
                </PageSection>

                <A2AProtocolSection hasAgentCard={hasAgentCard} storedAgent={initialRemoteAgent} />

                <PageSection>
                    <div className="flex items-center justify-between">
                        <div>
                            <div className="font-semibold">Headers</div>
                            <div className="text-sm text-(--secondary-text-wMain)">Add custom HTTP headers to include with agent task invocation requests.</div>
                        </div>
                        <Button type="button" variant="ghost" size="sm" onClick={handleAddHeader}>
                            <Plus className="mr-2 size-4" />
                            New Header
                        </Button>
                    </div>
                    <KeyValuePairList name="taskHeaders" error={errors.taskHeaders} />
                </PageSection>
            </form>
        </FormProvider>
    );
};
