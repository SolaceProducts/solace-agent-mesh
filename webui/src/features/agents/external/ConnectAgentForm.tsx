// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Plus } from "lucide-react";
import type { UseFormReturn } from "react-hook-form";
import { FormProvider, useForm } from "react-hook-form";
import { z } from "zod";

import { type RemoteAgentResponse, remoteAgentsApi } from "@/api/remote-agents";

import { useLocalStorage } from "@/hooks/useLocalStorage";

import { PageSection } from "@/common/layout";

import { Button } from "@/ui/button";

import { KeyValuePairList, MessageBanner } from "@/common";

import { createHeaderValidation } from "../utils/validationUtils";

import { AUTH_TYPE_OPTIONS, AUTH_TYPES, createAuthValidationSchema, OAuth2AuthFields, StaticApiKeyAuthFields, StaticBearerAuthFields } from "./AuthenticationFields";
import { useTrackAuthStoredSecrets } from "./authSecretTracking";
import { buildWellKnownUrl } from "./connectAgentHelpers";
import type { SelectOption } from "./FormComponents";
import { SelectField, TextInput } from "./FormComponents";
import { FetchedCardDetails, OfferedInterfacesWarning } from "./RemoteProtocolDetails";

const CARD_LOCATIONS = {
    WELL_KNOWN: "well_known",
    CUSTOM: "custom",
    NONE: "none",
} as const;

const NO_CARD_INFO_DISMISSED_KEY = "agentCardNoCardInfoDismissed";

export type ConnectAgentFormData = {
    agentUrl: string;
    agentCardLocation: "" | "well_known" | "custom" | "none";
    agentCardUrl?: string;
    authenticationType?: string;
    agentCardHeaders: { key: string; value: string }[];
    authBearerToken?: string;
    authApikeyValue?: string;
    authOauth2TokenUrl?: string;
    authOauth2ClientId?: string;
    authOauth2ClientSecret?: string;
    authOauth2Scope?: string;
};

export type ConnectAgentFormMethods = UseFormReturn<ConnectAgentFormData> & {
    fetchAgentCard?: (isManualFetch?: boolean) => Promise<void>;
};

interface ConnectAgentFormProps {
    onFormReady?: (methods: ConnectAgentFormMethods) => void;
    defaultValues?: Partial<ConnectAgentFormData>;
    fetchedAgentCard?: import("@/types").ExternalAgentCard | null;
    onAgentCardFetched?: (card: import("@/types").ExternalAgentCard) => void;
    onFetchError?: () => void;
    isEditMode?: boolean;
    onChange?: () => void;
    initialRemoteAgent?: RemoteAgentResponse;
}

const createConnectAgentSchema = (authType?: string, cardLocation?: string, hasPreservableSecret: boolean = false) => {
    const hasCardSections = cardLocation === "well_known" || cardLocation === "custom";

    const baseSchema = z.object({
        agentUrl: z.string().min(1, "Agent URL is required").max(2048, "Agent URL must be 2048 characters or less").url("Must be a valid URL"),
        agentCardLocation: z.enum(["well_known", "custom", "none", ""]).refine(val => val !== "", "Please select an agent card location"),
        agentCardUrl: z
            .string()
            .refine(val => val === "" || (val.length <= 2048 && z.string().url().safeParse(val).success), {
                message: "Must be a valid URL and at most 2048 characters",
            })
            .optional()
            .or(z.literal("")),
        authenticationType: hasCardSections ? z.string().min(1, "Authentication Type is required") : z.string().optional(),
        agentCardHeaders: createHeaderValidation(),
    });

    if (!hasCardSections) return baseSchema;

    const authValidation = createAuthValidationSchema(authType, "auth", hasPreservableSecret);
    return baseSchema.extend(authValidation);
};

const handleAgentCardLocationChange = (newLocation: string, agentUrl: string, setValue: any, getValues: any) => {
    if (newLocation === CARD_LOCATIONS.WELL_KNOWN && agentUrl) {
        setValue("agentCardUrl", buildWellKnownUrl(agentUrl), { shouldValidate: true });
    } else if (newLocation === CARD_LOCATIONS.CUSTOM) {
        const currentValue = getValues("agentCardUrl");
        if (agentUrl && currentValue === buildWellKnownUrl(agentUrl)) {
            setValue("agentCardUrl", "", { shouldValidate: false });
        }
    } else if (newLocation === CARD_LOCATIONS.NONE) {
        setValue("agentCardUrl", "", { shouldValidate: false });
    }
};

const EMPTY_CONNECT_AGENT_FORM: ConnectAgentFormData = {
    agentUrl: "",
    agentCardLocation: "",
    agentCardUrl: "",
    authenticationType: "",
    agentCardHeaders: [{ key: "", value: "" }],
    authBearerToken: "",
    authApikeyValue: "",
    authOauth2TokenUrl: "",
    authOauth2ClientId: "",
    authOauth2ClientSecret: "",
    authOauth2Scope: "",
};

const fetchedCardMessage = (name: string) => `Successfully fetched agent card: ${name}`;
const fetchErrorIsAlreadyShown = () => undefined;

export const ConnectAgentForm = ({ onFormReady, defaultValues, fetchedAgentCard, onAgentCardFetched, onFetchError, isEditMode, onChange, initialRemoteAgent }: ConnectAgentFormProps) => {
    const storedSecrets = useTrackAuthStoredSecrets(initialRemoteAgent?.agentCardAuthConfig, initialRemoteAgent?.agentAuthConfig);

    const [isNoCardInfoDismissed, setIsNoCardInfoDismissed] = useLocalStorage(NO_CARD_INFO_DISMISSED_KEY, false);
    const [isFetchingCard, setIsFetchingCard] = useState(false);
    const [fetchStatus, setFetchStatus] = useState<{
        type: "success" | "error";
        message: string;
    } | null>(null);
    const [fetchedCardBannerDismissed, setFetchedCardBannerDismissed] = useState(false);

    const dismissNoCardInfo = useCallback(() => {
        setIsNoCardInfoDismissed(true);
    }, [setIsNoCardInfoDismissed]);

    useEffect(() => {
        setFetchStatus(null);
        setIsFetchingCard(false);
    }, [defaultValues]);

    const methods = useForm<ConnectAgentFormData>({
        resolver: async (data, context, options) => {
            const authType = data.authenticationType;
            const cardLocation = data.agentCardLocation;
            const storedType = initialRemoteAgent?.agentCardAuthConfig?.type;
            const schema = createConnectAgentSchema(authType, cardLocation, storedSecrets.agentCardAuth.size > 0 && authType === storedType);
            return zodResolver(schema)(data, context, options as any) as any;
        },
        defaultValues: {
            ...EMPTY_CONNECT_AGENT_FORM,
            ...defaultValues,
        },
        mode: "onBlur",
        reValidateMode: "onChange",
    });

    const {
        register,
        control,
        watch,
        setValue,
        getValues,
        trigger,
        setError,
        formState: { errors },
    } = methods;

    const agentUrl = watch("agentUrl");
    const agentCardLocation = watch("agentCardLocation");
    const agentCardUrl = watch("agentCardUrl");
    const authenticationType = watch("authenticationType");
    const authBearerToken = watch("authBearerToken");
    const authApikeyValue = watch("authApikeyValue");
    const authOauth2TokenUrl = watch("authOauth2TokenUrl");
    const authOauth2ClientId = watch("authOauth2ClientId");
    const authOauth2ClientSecret = watch("authOauth2ClientSecret");

    useEffect(() => {
        const subscription = watch(() => {
            onChange?.();
        });
        return () => subscription.unsubscribe();
    }, [onChange]);

    const handleFetchAgentCard = useCallback(
        async (isManualFetch: boolean = false) => {
            if (agentCardLocation === CARD_LOCATIONS.NONE) {
                return;
            }

            setFetchStatus(null);

            const isValid = await trigger();
            if (!isValid) {
                throw new Error("Form validation failed");
            }

            setIsFetchingCard(true);

            try {
                const formValues = getValues();

                const hasMissingCredentials =
                    !isEditMode &&
                    (() => {
                        if (formValues.authenticationType === AUTH_TYPES.BEARER) {
                            const token = formValues.authBearerToken;
                            return !token || token === "";
                        } else if (formValues.authenticationType === AUTH_TYPES.APIKEY) {
                            const apiKey = formValues.authApikeyValue;
                            return !apiKey || apiKey === "";
                        } else if (formValues.authenticationType === AUTH_TYPES.OAUTH2) {
                            const clientSecret = formValues.authOauth2ClientSecret;
                            return !clientSecret || clientSecret === "";
                        }
                        return false;
                    })();

                if (hasMissingCredentials) {
                    setFetchStatus({
                        type: "error",
                        message: "Please enter authentication credentials to fetch the agent card.",
                    });
                    setIsFetchingCard(false);
                    throw new Error("Cannot fetch agent card without credentials");
                }

                let authConfig = null;
                if (formValues.authenticationType === AUTH_TYPES.BEARER) {
                    const token = formValues.authBearerToken;
                    if (token && token.trim() !== "") {
                        authConfig = { bearerToken: token };
                    } else if (isEditMode && isManualFetch) {
                        setFetchStatus({
                            type: "error",
                            message: "Agent card auto-fetch failed. Please re-enter your Bearer token to manually fetch the agent card.",
                        });
                        setIsFetchingCard(false);
                        throw new Error("Bearer credentials required for manual fetch in edit mode");
                    } else if (isEditMode) {
                        setIsFetchingCard(false);
                        return;
                    }
                } else if (formValues.authenticationType === AUTH_TYPES.APIKEY) {
                    const apiKey = formValues.authApikeyValue;
                    if (apiKey && apiKey.trim() !== "") {
                        authConfig = { apikeyValue: apiKey };
                    } else if (isEditMode && isManualFetch) {
                        setFetchStatus({
                            type: "error",
                            message: "Agent card auto-fetch failed. Please re-enter your API key to manually fetch the agent card.",
                        });
                        setIsFetchingCard(false);
                        throw new Error("API key credentials required for manual fetch in edit mode");
                    } else if (isEditMode) {
                        setIsFetchingCard(false);
                        return;
                    }
                } else if (formValues.authenticationType === AUTH_TYPES.OAUTH2) {
                    const tokenUrl = formValues.authOauth2TokenUrl;
                    const clientId = formValues.authOauth2ClientId;
                    const clientSecret = formValues.authOauth2ClientSecret;

                    if (tokenUrl && clientId && clientSecret) {
                        authConfig = {
                            oauth2TokenUrl: tokenUrl,
                            oauth2ClientId: clientId,
                            oauth2ClientSecret: clientSecret,
                            oauth2Scope: formValues.authOauth2Scope,
                        };
                    } else if (isEditMode && tokenUrl && clientId && !clientSecret && isManualFetch) {
                        setFetchStatus({
                            type: "error",
                            message: "Agent card auto-fetch failed. Please re-enter your OAuth2 client secret to manually fetch the agent card.",
                        });
                        setIsFetchingCard(false);
                        throw new Error("OAuth2 credentials required for manual fetch in edit mode");
                    } else if (isEditMode && tokenUrl && clientId && !clientSecret) {
                        setIsFetchingCard(false);
                        return;
                    } else {
                        const missingFields = [];
                        if (!tokenUrl) missingFields.push("Token URL");
                        if (!clientId) missingFields.push("Client ID");
                        if (!isEditMode && !clientSecret) missingFields.push("Client Secret");

                        setFetchStatus({
                            type: "error",
                            message: `OAuth2 configuration incomplete. Missing: ${missingFields.join(", ")}`,
                        });
                        setIsFetchingCard(false);
                        throw new Error("Incomplete OAuth2 configuration");
                    }
                }

                const customHeaders: Record<string, string> = {};
                formValues.agentCardHeaders.forEach(({ key, value }) => {
                    if (key && value) customHeaders[key] = value;
                });

                const responseData = await remoteAgentsApi.fetchCard({
                    agentCardUrl: formValues.agentCardUrl,
                    authenticationType: formValues.authenticationType,
                    authConfig: authConfig,
                    customHeaders: Object.keys(customHeaders).length > 0 ? customHeaders : null,
                });

                const agentCard = responseData;

                onAgentCardFetched?.(agentCard);
                setFetchStatus({
                    type: "success",
                    message: fetchedCardMessage(agentCard.name),
                });
            } catch (err: any) {
                onFetchError?.();

                const validationDetails = err?.response?.data?.validationDetails || err?.data?.validationDetails || err?.validationDetails;
                let hasFieldErrors = false;

                if (validationDetails && typeof validationDetails === "object") {
                    Object.keys(validationDetails).forEach(fieldName => {
                        const errorMessages = validationDetails[fieldName];
                        if (Array.isArray(errorMessages) && errorMessages.length > 0) {
                            setError(fieldName as any, {
                                type: "manual",
                                message: errorMessages[0],
                            });
                            hasFieldErrors = true;
                        }
                    });
                }

                if (!hasFieldErrors) {
                    setFetchStatus({
                        type: "error",
                        message: err instanceof Error ? err.message : "Failed to fetch agent card",
                    });
                } else {
                    setFetchStatus({
                        type: "error",
                        message: "Please fix the validation errors below.",
                    });
                }

                throw err;
            } finally {
                setIsFetchingCard(false);
            }
        },
        [agentCardLocation, getValues, trigger, onAgentCardFetched, onFetchError, setError, isEditMode]
    );

    const handleAgentUrlBlur = useCallback(() => {
        const currentAgentUrl = getValues("agentUrl");
        const trimmedUrl = currentAgentUrl?.trim() || "";

        if (currentAgentUrl !== trimmedUrl) {
            setValue("agentUrl", trimmedUrl, { shouldValidate: true, shouldDirty: true });
        }

        const currentCardLocation = getValues("agentCardLocation");

        trigger("agentUrl");

        if (currentCardLocation === CARD_LOCATIONS.WELL_KNOWN && trimmedUrl) {
            try {
                new URL(trimmedUrl);
                setValue("agentCardUrl", buildWellKnownUrl(trimmedUrl), { shouldValidate: true });
            } catch {}
        }
    }, [getValues, setValue, trigger]);

    const handleAgentCardUrlBlur = useCallback(() => {
        const currentValue = getValues("agentCardUrl");
        const trimmedValue = currentValue?.trim() || "";

        if (currentValue !== trimmedValue) {
            setValue("agentCardUrl", trimmedValue, { shouldValidate: true, shouldDirty: true });
        }

        trigger("agentCardUrl");
    }, [getValues, setValue, trigger]);

    useEffect(() => {
        if (onFormReady) {
            const extendedMethods: ConnectAgentFormMethods = {
                ...methods,
                fetchAgentCard: handleFetchAgentCard,
            };
            onFormReady(extendedMethods);
        }
    }, [methods, onFormReady, handleFetchAgentCard]);

    const prevStructuralRef = useRef({ agentCardUrl, agentCardLocation });
    useEffect(() => {
        const changed = prevStructuralRef.current.agentCardUrl !== agentCardUrl || prevStructuralRef.current.agentCardLocation !== agentCardLocation;
        prevStructuralRef.current = { agentCardUrl, agentCardLocation };
        if (changed && fetchedAgentCard) {
            onFetchError?.();
            setFetchStatus(null);
        }
    }, [agentCardUrl, agentCardLocation, fetchedAgentCard, onFetchError]);

    const prevAuthRef = useRef({ authenticationType, authBearerToken, authApikeyValue, authOauth2TokenUrl, authOauth2ClientId, authOauth2ClientSecret });
    useEffect(() => {
        const changed =
            prevAuthRef.current.authenticationType !== authenticationType ||
            prevAuthRef.current.authBearerToken !== authBearerToken ||
            prevAuthRef.current.authApikeyValue !== authApikeyValue ||
            prevAuthRef.current.authOauth2TokenUrl !== authOauth2TokenUrl ||
            prevAuthRef.current.authOauth2ClientId !== authOauth2ClientId ||
            prevAuthRef.current.authOauth2ClientSecret !== authOauth2ClientSecret;
        prevAuthRef.current = { authenticationType, authBearerToken, authApikeyValue, authOauth2TokenUrl, authOauth2ClientId, authOauth2ClientSecret };
        if (changed && !isEditMode && fetchedAgentCard) {
            onFetchError?.();
            setFetchStatus(null);
        }
    }, [authenticationType, authBearerToken, authApikeyValue, authOauth2TokenUrl, authOauth2ClientId, authOauth2ClientSecret, fetchedAgentCard, isEditMode, onFetchError]);

    const showCardSections = agentCardLocation === CARD_LOCATIONS.WELL_KNOWN || agentCardLocation === CARD_LOCATIONS.CUSTOM;
    const cardOnShow = showCardSections ? fetchedAgentCard : null;
    const cardBanner = fetchStatus ?? (cardOnShow && !fetchedCardBannerDismissed && !isFetchingCard ? { type: "success" as const, message: fetchedCardMessage(cardOnShow.name) } : null);

    const canFetchAgentCard = useMemo(() => {
        if (agentCardLocation === CARD_LOCATIONS.NONE) return false;

        const formValues = getValues();
        if (!formValues.agentCardUrl) return false;

        try {
            new URL(formValues.agentCardUrl);
        } catch {
            return false;
        }

        if (formValues.authenticationType === AUTH_TYPES.OAUTH2) {
            if (!authOauth2TokenUrl || !authOauth2ClientId) {
                return false;
            }
            if (!isEditMode && !authOauth2ClientSecret) {
                return false;
            }
            try {
                new URL(authOauth2TokenUrl);
            } catch {
                return false;
            }
        }

        return true;
    }, [agentCardLocation, agentCardUrl, authenticationType, authOauth2TokenUrl, authOauth2ClientId, authOauth2ClientSecret, getValues, isEditMode]);

    const handleAddHeader = useCallback(() => {
        const currentHeaders = getValues("agentCardHeaders");
        setValue("agentCardHeaders", [...currentHeaders, { key: "", value: "" }]);
    }, [getValues, setValue]);

    const cardLocationOptions: SelectOption[] = [
        {
            value: CARD_LOCATIONS.WELL_KNOWN,
            label: "Well-known URI",
            description: "Your agent card is located at the standard well-known URI.",
        },
        {
            value: CARD_LOCATIONS.CUSTOM,
            label: "Custom",
            description: "Your agent card is located at a custom location.",
        },
        {
            value: CARD_LOCATIONS.NONE,
            label: "No Agent Card",
            description: "You do not have an agent card defined.",
        },
    ];

    return (
        <FormProvider {...methods}>
            <form className="flex max-w-1/2 min-w-[700px] flex-col gap-8" autoComplete="off">
                <PageSection>
                    <div className="flex items-center justify-between">
                        <div>
                            <h2 className="text-xl font-semibold">Connect Your Agent</h2>
                            <p>Provide the connection details for your external agent.</p>
                        </div>
                        {showCardSections && (
                            <Button type="button" onClick={() => handleFetchAgentCard(true).catch(fetchErrorIsAlreadyShown)} disabled={!canFetchAgentCard || isFetchingCard} variant="outline" size="sm" data-testid="externalAgentFetchCard">
                                {isFetchingCard ? "Fetching..." : "Fetch Agent Card"}
                            </Button>
                        )}
                    </div>

                    {cardBanner && (
                        <div className="mt-4">
                            <MessageBanner
                                variant={cardBanner.type === "success" ? "success" : "error"}
                                message={
                                    cardBanner.type === "success" && cardOnShow ? (
                                        <div>
                                            <span>{cardBanner.message}</span>
                                            <FetchedCardDetails interfaces={cardOnShow.interfaces} signed={cardOnShow.signed} />
                                        </div>
                                    ) : (
                                        cardBanner.message
                                    )
                                }
                                dismissible
                                onDismiss={() => {
                                    setFetchStatus(null);
                                    setFetchedCardBannerDismissed(true);
                                }}
                            />
                        </div>
                    )}
                    {cardOnShow && <OfferedInterfacesWarning interfaces={cardOnShow.interfaces} className="mt-4" />}
                </PageSection>

                <PageSection>
                    <div className="font-semibold">Location</div>

                    <div className="space-y-4">
                        <TextInput name="agentUrl" label="Agent URL" register={register} error={errors.agentUrl} required onBlur={handleAgentUrlBlur} />

                        <SelectField
                            name="agentCardLocation"
                            label="Agent Card Location"
                            control={control}
                            options={cardLocationOptions}
                            error={errors.agentCardLocation}
                            required
                            trigger={trigger}
                            onValueChange={value => handleAgentCardLocationChange(value, agentUrl, setValue, getValues)}
                        />

                        {agentCardLocation === CARD_LOCATIONS.NONE && !isNoCardInfoDismissed && (
                            <MessageBanner
                                variant="info"
                                message="Agent cards are recommended as they contain useful information that helps other agents understand when to use your agent. You will be asked to manually register the agent information in the next step."
                                dismissible
                                onDismiss={dismissNoCardInfo}
                            />
                        )}

                        {showCardSections && (
                            <TextInput
                                name="agentCardUrl"
                                label="Agent Card URL"
                                register={register}
                                error={errors.agentCardUrl}
                                required
                                onBlur={handleAgentCardUrlBlur}
                                readOnly={agentCardLocation === CARD_LOCATIONS.WELL_KNOWN}
                                helpText={agentCardLocation === CARD_LOCATIONS.WELL_KNOWN ? 'Derived from the Agent URL (/.well-known/agent-card.json). Select "Custom" to use a different location.' : undefined}
                            />
                        )}
                    </div>
                </PageSection>

                {showCardSections && (
                    <PageSection>
                        <div className="font-semibold">Agent Card Authentication</div>
                        <div className="pb-2 text-sm text-(--secondary-text-wMain)">Provide the credentials for accessing your agent card, if any.</div>

                        <div className="space-y-4">
                            <SelectField name="authenticationType" label="Authentication Type" control={control} options={AUTH_TYPE_OPTIONS} error={errors.authenticationType} required trigger={trigger} />

                            {authenticationType === AUTH_TYPES.BEARER && <StaticBearerAuthFields control={control} register={register} errors={errors} storedSecrets={storedSecrets} authScope="agentCardAuth" />}

                            {authenticationType === AUTH_TYPES.APIKEY && <StaticApiKeyAuthFields control={control} register={register} errors={errors} storedSecrets={storedSecrets} authScope="agentCardAuth" />}

                            {authenticationType === AUTH_TYPES.OAUTH2 && <OAuth2AuthFields control={control} register={register} errors={errors} trigger={trigger} storedSecrets={storedSecrets} authScope="agentCardAuth" />}
                        </div>
                    </PageSection>
                )}

                {showCardSections && (
                    <PageSection>
                        <div className="flex items-center justify-between">
                            <div>
                                <div className="font-semibold">Agent Card Headers</div>
                                <div className="text-sm text-(--secondary-text-wMain)">Add custom HTTP headers to include with agent card requests.</div>
                            </div>
                            <Button type="button" variant="ghost" size="sm" onClick={handleAddHeader}>
                                <Plus className="mr-2 size-4" />
                                New Header
                            </Button>
                        </div>
                        <KeyValuePairList name="agentCardHeaders" error={errors.agentCardHeaders} />
                    </PageSection>
                )}
            </form>
        </FormProvider>
    );
};
