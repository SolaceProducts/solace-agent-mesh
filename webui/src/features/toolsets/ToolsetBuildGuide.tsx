// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ExternalLink } from "lucide-react";
import { Link } from "react-router-dom";

import { CommandLine, useResolvedBuildTarget } from "@/common/BuildGuide";

import { Button } from "@/ui/button";

import { LearningBanner, NumberedStep } from "@/common";
import { DOC_LINKS } from "@/utils";

interface ToolsetBuildGuideProps {
    readonly className?: string;
}

export function ToolsetBuildGuide({ className }: ToolsetBuildGuideProps) {
    const { targetOS, targetArch, targetPython, isError } = useResolvedBuildTarget();

    const initCmd = "sam toolset init <name> --lang <python|go>";
    const validateCmd = "sam toolset validate <name>";
    const packageEnv = [`SAM_TOOL_TARGET_OS=${targetOS}`, `SAM_TOOL_TARGET_ARCH=${targetArch}`];
    if (targetPython) packageEnv.push(`SAM_TOOL_PYTHON_VERSION=${targetPython}`);
    const packageCmd = `${packageEnv.join(" \\\n")} \\\nsam toolset package <name>`;

    return (
        <div className={className}>
            <LearningBanner title="Build a Toolset with SAM's CLI" storageKey="sam-toolset-build-guide-dismissed" showIcon={false}>
                <p>Solace Agent Mesh includes a CLI to help you scaffold and package custom tools quickly. Run these commands to get started — no extra setup required.</p>
                <Link to={DOC_LINKS.buildingToolsets} target="_blank" rel="noopener noreferrer">
                    <Button variant="link" className="mt-1 p-0 has-[>svg]:px-0" title="Learn more about custom tool development">
                        <div className="truncate">Learn more about custom tool development</div>
                        <ExternalLink />
                    </Button>
                </Link>

                <div className="mt-4 space-y-4">
                    <NumberedStep
                        number={1}
                        title="Create a New Tool"
                        description={
                            <>
                                Creates an empty tool project with the right structure. Add your code inside the <code className="font-mono">src/</code> directory it generates.
                            </>
                        }
                    >
                        <CommandLine command={initCmd} testid="toolsetInitCommand" />
                        <p className="mt-2 text-xs text-(--secondary-text-wMain)">Replace &lt;name&gt; with your tool's directory name. Choose python or go.</p>
                    </NumberedStep>
                    <NumberedStep number={2} title="Check it" description="Builds your tool for your host and runs the same --schema discovery the STR performs, so you confirm it loads before packaging.">
                        <CommandLine command={validateCmd} testid="toolsetValidateCommand" />
                    </NumberedStep>
                    <NumberedStep number={3} title="Package it into a ZIP" description="Once your code is ready, this bundles it for your machine and produces a ZIP.">
                        <CommandLine command={packageCmd} testid="toolsetPackageCommand" />
                        {!isError && targetPython && (
                            <p className="mt-2 text-xs text-(--secondary-text-wMain)" data-testid="toolsetPythonNote">
                                Python toolsets build native wheels for the STR's Python ({targetPython}); the command above pins it so the ABI matches.
                            </p>
                        )}
                        {isError && (
                            <p className="mt-2 text-xs text-(--secondary-text-wMain)">
                                Couldn't detect the build target automatically. Set SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH (and SAM_TOOL_PYTHON_VERSION for Python tools) to match your STR's platform.
                            </p>
                        )}
                    </NumberedStep>
                    <NumberedStep number={4} title="Upload the ZIP" description="Use the 'Select Toolset File' button on this page to add your packaged tool to Solace Agent Mesh." />
                </div>
            </LearningBanner>
        </div>
    );
}
