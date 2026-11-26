// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef, useState } from "react";
import { Loader2, Mic, Play, Volume2 } from "lucide-react";

import { api } from "@/api/client";

import { useAudioSettings, useChatContext, useConfigContext } from "@/providers/contexts";

import { ErrorLabel } from "@/common";
import { Button, Input, Label, LifecycleBadge, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Switch } from "@/ui";

export const SpeechSettings = () => {
    const { settings, updateSetting } = useAudioSettings();
    const { speechToTextEnabled, textToSpeechEnabled } = useConfigContext();
    const { displayError } = useChatContext();
    const [availableVoices, setAvailableVoices] = useState<string[]>([]);
    const [loadingVoices, setLoadingVoices] = useState(false);
    const [sttConfigured, setSttConfigured] = useState<boolean | null>(null);
    const [ttsConfigured, setTtsConfigured] = useState<boolean | null>(null);
    const [sttProviders, setSttProviders] = useState<{ openai: boolean; azure: boolean } | null>(null);
    const [ttsProviders, setTtsProviders] = useState<{ gemini: boolean; azure: boolean; polly: boolean } | null>(null);
    const [playingSample, setPlayingSample] = useState(false);
    const [loadingSample, setLoadingSample] = useState(false);
    const [audioElement, setAudioElement] = useState<HTMLAudioElement | null>(null);

    const sttEnabled = Boolean(speechToTextEnabled);
    const ttsEnabled = Boolean(textToSpeechEnabled);

    useEffect(() => {
        const checkConfig = async () => {
            try {
                const config = await api.webui.get("/api/v1/speech/config");
                const sttExt = config.sttExternal || false;
                const ttsExt = config.ttsExternal || false;

                setSttConfigured(sttExt);
                setTtsConfigured(ttsExt);

                if (config.sttProviders) {
                    setSttProviders(config.sttProviders);
                }
                if (config.ttsProviders) {
                    setTtsProviders(config.ttsProviders);
                }

                if (!sttExt && settings.sttProvider !== "browser") {
                    console.warn("External STT not configured, resetting provider to browser");
                    updateSetting("sttProvider", "browser");
                    updateSetting("engineSTT", "browser");
                }
                if (!ttsExt && settings.ttsProvider !== "browser") {
                    console.warn("External TTS not configured, resetting provider to browser");
                    updateSetting("ttsProvider", "browser");
                    updateSetting("engineTTS", "browser");
                }
            } catch (error) {
                console.error("Error checking speech config:", error);
            }
        };
        checkConfig();
    }, [settings.sttProvider, settings.ttsProvider, updateSetting]);

    const prevProviderRef = useRef(settings.ttsProvider);

    useEffect(() => {
        const loadVoices = async () => {
            if (settings.ttsProvider === "browser") {
                setAvailableVoices([]);
                prevProviderRef.current = settings.ttsProvider;
                return;
            }

            setLoadingVoices(true);
            try {
                const provider = settings.ttsProvider || "gemini";
                const data = await api.webui.get(`/api/v1/speech/voices?provider=${provider}`);
                const voices = data.voices || [];
                setAvailableVoices(voices);

                const providerChanged = prevProviderRef.current !== settings.ttsProvider;
                if (providerChanged && voices.length > 0 && !voices.includes(settings.voice)) {
                    console.log(`Voice "${settings.voice}" not available for ${provider}, selecting first available: ${voices[0]}`);
                    updateSetting("voice", voices[0]);
                }

                prevProviderRef.current = settings.ttsProvider;
            } catch (error) {
                console.error("Error loading voices:", error);
            } finally {
                setLoadingVoices(false);
            }
        };

        loadVoices();
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [settings.ttsProvider]);

    useEffect(() => {
        return () => {
            if (audioElement) {
                audioElement.pause();
                audioElement.src = "";
            }
        };
    }, [audioElement]);

    const playVoiceSample = async (voice: string) => {
        try {
            if (audioElement) {
                audioElement.pause();
                audioElement.src = "";
                audioElement.onended = null;
                audioElement.onerror = null;
                setAudioElement(null);
            }

            setLoadingSample(true);
            setPlayingSample(false);

            const formData = new FormData();
            formData.append("voice", voice);
            if (settings.ttsProvider !== "browser") {
                formData.append("provider", settings.ttsProvider);
            }

            const response = await api.webui.post("/api/v1/speech/voiceSample", formData, { fullResponse: true });

            if (!response.ok) {
                throw new Error(`Failed to load voice sample: ${response.statusText}`);
            }

            const blob = await response.blob();
            const url = URL.createObjectURL(blob);

            setLoadingSample(false);
            setPlayingSample(true);

            const audio = new Audio(url);
            audio.onended = () => {
                setPlayingSample(false);
                setAudioElement(null);
                URL.revokeObjectURL(url);
            };
            audio.onerror = () => {
                setPlayingSample(false);
                setAudioElement(null);
                URL.revokeObjectURL(url);
            };

            audio.playbackRate = settings.playbackRate || 1.0;

            setAudioElement(audio);
            await audio.play();
        } catch (error) {
            console.error("Error playing voice sample:", error);
            setLoadingSample(false);
            setPlayingSample(false);
            setAudioElement(null);
            displayError({ title: "Playback Failed", error: "Please try again or contact your administrator." });
        }
    };

    let playButtonIcon = <Play className="size-5" />;
    if (loadingSample) {
        playButtonIcon = <Loader2 className="size-4 animate-spin" />;
    } else if (playingSample) {
        playButtonIcon = <Volume2 className="size-5" />;
    }

    return (
        <>
            {sttEnabled && (
                <div className="space-y-4">
                    <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2">
                            <Mic className="size-5" />
                            <Label htmlFor="speech-to-text-switch" className="font-semibold">
                                Speech-to-Text
                            </Label>
                            <LifecycleBadge variant="transparent" />
                        </div>
                        <Switch id="speech-to-text-switch" checked={settings.speechToText} onCheckedChange={checked => updateSetting("speechToText", checked)} />
                    </div>

                    {settings.speechToText && (
                        <>
                            {settings.sttProvider !== "browser" && sttConfigured === false && <ErrorLabel message="External STT is not configured. Select Browser or contact your administrator." />}

                            <div className="flex flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
                                <Label className="font-medium">Speech-to-Text Provider</Label>
                                <Select
                                    value={settings.sttProvider}
                                    onValueChange={(value: "browser" | "openai" | "azure") => {
                                        updateSetting("sttProvider", value);
                                        updateSetting("engineSTT", value === "browser" ? "browser" : "external");
                                    }}
                                >
                                    <SelectTrigger className="w-full sm:w-44">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="browser">Browser (Free)</SelectItem>
                                        {sttProviders?.openai && <SelectItem value="openai">OpenAI Whisper</SelectItem>}
                                        {sttProviders?.azure && <SelectItem value="azure">Azure Speech</SelectItem>}
                                    </SelectContent>
                                </Select>
                            </div>

                            <div className="flex flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
                                <Label className="font-medium">Language</Label>
                                <Select value={settings.languageSTT} onValueChange={value => updateSetting("languageSTT", value)}>
                                    <SelectTrigger className="w-full sm:w-44">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="en-US">English (US)</SelectItem>
                                        <SelectItem value="en-GB">English (UK)</SelectItem>
                                        <SelectItem value="es-ES">Spanish</SelectItem>
                                        <SelectItem value="fr-FR">French</SelectItem>
                                        <SelectItem value="de-DE">German</SelectItem>
                                        <SelectItem value="it-IT">Italian</SelectItem>
                                        <SelectItem value="ja-JP">Japanese</SelectItem>
                                        <SelectItem value="ko-KR">Korean</SelectItem>
                                        <SelectItem value="zh-CN">Chinese (Simplified)</SelectItem>
                                    </SelectContent>
                                </Select>
                            </div>
                        </>
                    )}
                </div>
            )}

            {ttsEnabled && (
                <div className="space-y-4">
                    <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2">
                            <Volume2 className="size-5" />
                            <Label htmlFor="text-to-speech-switch" className="font-semibold">
                                Text-to-Speech
                            </Label>
                            <LifecycleBadge variant="transparent" />
                        </div>
                        <Switch id="text-to-speech-switch" checked={settings.textToSpeech} onCheckedChange={checked => updateSetting("textToSpeech", checked)} />
                    </div>

                    {settings.textToSpeech && (
                        <>
                            {settings.ttsProvider !== "browser" && ttsConfigured === false && <ErrorLabel message="External TTS is not configured. Select Browser or contact your administrator." />}

                            <div className="flex flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
                                <Label className="font-medium">Text-to-Speech Provider</Label>
                                <Select
                                    value={settings.ttsProvider}
                                    onValueChange={(value: "browser" | "gemini" | "azure" | "polly") => {
                                        updateSetting("ttsProvider", value);
                                        updateSetting("engineTTS", value === "browser" ? "browser" : "external");
                                    }}
                                >
                                    <SelectTrigger className="w-full sm:w-44">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="browser">Browser (Free)</SelectItem>
                                        {ttsProviders?.gemini && <SelectItem value="gemini">Google Gemini</SelectItem>}
                                        {ttsProviders?.azure && <SelectItem value="azure">Azure Neural</SelectItem>}
                                        {ttsProviders?.polly && <SelectItem value="polly">AWS Polly</SelectItem>}
                                    </SelectContent>
                                </Select>
                            </div>

                            {settings.ttsProvider !== "browser" && (
                                <div className="flex flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
                                    <Label className="font-medium">Voice</Label>
                                    <div className="flex w-full items-center gap-2 sm:w-44">
                                        <Select value={settings.voice} onValueChange={value => updateSetting("voice", value)} disabled={loadingVoices}>
                                            <SelectTrigger className="min-w-0 flex-1">
                                                <SelectValue>{loadingVoices ? "Loading..." : settings.voice || "Select voice"}</SelectValue>
                                            </SelectTrigger>
                                            <SelectContent>
                                                {availableVoices.length > 0 ? (
                                                    (() => {
                                                        const isAzure = availableVoices.some(v => v.includes("DragonHD"));

                                                        if (isAzure && settings.ttsProvider === "azure") {
                                                            const hdVoices = availableVoices.filter(v => v.includes("DragonHD"));
                                                            const normalVoices = availableVoices.filter(v => !v.includes("DragonHD"));

                                                            return (
                                                                <>
                                                                    {hdVoices.length > 0 && (
                                                                        <>
                                                                            <div className="px-2 py-1.5 text-xs font-semibold text-(--secondary-text-wMain)">HD Voices (Premium)</div>
                                                                            {hdVoices.map(voice => {
                                                                                const parts = voice.split("-");
                                                                                const lastPart = parts[parts.length - 1] || voice;
                                                                                const name = lastPart.replace("MultilingualNeural:DragonHDLatestNeural", "").replace("Neural:DragonHDLatestNeural", "").replace(":DragonHDLatestNeural", "");
                                                                                return (
                                                                                    <SelectItem key={voice} value={voice}>
                                                                                        {name}
                                                                                    </SelectItem>
                                                                                );
                                                                            })}
                                                                        </>
                                                                    )}
                                                                    {normalVoices.length > 0 && (
                                                                        <>
                                                                            <div className="mt-1 border-t px-2 py-1.5 pt-2 text-xs font-semibold text-(--secondary-text-wMain)">Standard Voices</div>
                                                                            {normalVoices.map(voice => {
                                                                                const parts = voice.split("-");
                                                                                const lastPart = parts[parts.length - 1] || voice;
                                                                                const name = lastPart.replace("MultilingualNeural", "").replace("Neural", "");
                                                                                return (
                                                                                    <SelectItem key={voice} value={voice}>
                                                                                        {name}
                                                                                    </SelectItem>
                                                                                );
                                                                            })}
                                                                        </>
                                                                    )}
                                                                </>
                                                            );
                                                        } else {
                                                            return availableVoices.map(voice => {
                                                                let displayName = voice;
                                                                if (voice.includes("-")) {
                                                                    const parts = voice.split("-");
                                                                    displayName = parts[parts.length - 1].replace("Neural", "").replace("Multilingual", "") || voice;
                                                                }
                                                                return (
                                                                    <SelectItem key={voice} value={voice}>
                                                                        {displayName}
                                                                    </SelectItem>
                                                                );
                                                            });
                                                        }
                                                    })()
                                                ) : (
                                                    <SelectItem value="loading" disabled>
                                                        {loadingVoices ? "Loading..." : "No voices available"}
                                                    </SelectItem>
                                                )}
                                            </SelectContent>
                                        </Select>
                                        <Button
                                            variant="ghost"
                                            size="sm"
                                            onClick={() => {
                                                if (!playingSample && !loadingSample) {
                                                    playVoiceSample(settings.voice);
                                                }
                                            }}
                                            disabled={!settings.voice || loadingVoices || playingSample || loadingSample}
                                            title={loadingSample ? "Loading sample..." : playingSample ? "Playing sample..." : "Play voice sample"}
                                            className="w-10 shrink-0"
                                        >
                                            {playButtonIcon}
                                        </Button>
                                    </div>
                                </div>
                            )}

                            <div className="flex flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
                                <Label className="font-medium">Playback Speed</Label>
                                <Input type="number" min={0.5} max={2} step={0.1} value={settings.playbackRate} onChange={e => updateSetting("playbackRate", Number.parseFloat(e.target.value) || 1)} className="w-full sm:w-44" />
                            </div>
                        </>
                    )}
                </div>
            )}
        </>
    );
};
