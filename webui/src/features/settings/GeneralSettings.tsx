// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { MoonStar } from "lucide-react";

import { useConfigContext, useThemeContext, useUIModeContext } from "@/providers/contexts";

import { Label, Switch } from "@/ui";

import { SpeechSettings } from "./SpeechSettings";

export const GeneralSettings = () => {
    const { currentTheme, setTheme } = useThemeContext();
    const { speechToTextEnabled, textToSpeechEnabled } = useConfigContext();
    const { allowSpeech } = useUIModeContext();

    const sttEnabled = Boolean(speechToTextEnabled);
    const ttsEnabled = Boolean(textToSpeechEnabled);
    const speechEnabled = allowSpeech && (sttEnabled || ttsEnabled);

    return (
        <div className="space-y-12">
            <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                    <MoonStar className="size-5" />
                    <Label htmlFor="dark-mode-switch" className="font-semibold">
                        Dark Mode Display
                    </Label>
                </div>
                <Switch id="dark-mode-switch" checked={currentTheme === "dark"} onCheckedChange={checked => setTheme(checked ? "dark" : "light")} />
            </div>

            {speechEnabled && <SpeechSettings />}
        </div>
    );
};
