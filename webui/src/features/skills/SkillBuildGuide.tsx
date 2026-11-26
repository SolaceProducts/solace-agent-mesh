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

interface SkillBuildGuideProps {
    readonly className?: string;
}

export function SkillBuildGuide({ className }: SkillBuildGuideProps) {
    const { targetOS, targetArch, targetPython, isError } = useResolvedBuildTarget();

    const initCmd = "sam skill init <name>";
    const validateCmd = "sam skill validate <name>";
    const packageEnv = [`SAM_TOOL_TARGET_OS=${targetOS}`, `SAM_TOOL_TARGET_ARCH=${targetArch}`];
    if (targetPython) packageEnv.push(`SAM_TOOL_PYTHON_VERSION=${targetPython}`);
    const packageCmd = `${packageEnv.join(" \\\n")} \\\nsam skill package <name>`;

    return (
        <div className={className}>
            <LearningBanner title="Build a Skill with SAM's CLI" storageKey="sam-skill-build-guide-dismissed" showIcon={false}>
                <p>Solace Agent Mesh includes a CLI to help you scaffold and package custom skills quickly. Run these commands to get started — no extra setup required.</p>
                <Link to={DOC_LINKS.buildingSkills} target="_blank" rel="noopener noreferrer">
                    <Button variant="link" className="mt-1 p-0 has-[>svg]:px-0" title="Learn more about skill development">
                        <div className="truncate">Learn more about skill development</div>
                        <ExternalLink />
                    </Button>
                </Link>

                <div className="mt-4 space-y-4">
                    <NumberedStep
                        number={1}
                        title="Scaffold Your Skill"
                        description={
                            <>
                                Creates <code className="font-mono">skills/&lt;name&gt;/</code> with a <code className="font-mono">SKILL.md</code> and <code className="font-mono">references/</code> directory. Edit{" "}
                                <code className="font-mono">SKILL.md</code> to describe what the skill teaches the agent.
                            </>
                        }
                    >
                        <CommandLine command={initCmd} testid="skillInitCommand" />
                        <p className="mt-2 text-xs text-(--secondary-text-wMain)">Add --with-tool to also scaffold a sample bundled tool under tools/ (append --lang python for a Python tool instead of Go).</p>
                    </NumberedStep>
                    <NumberedStep number={2} title="Check it" description="Builds any bundled tools for your host and runs the same --schema discovery the STR performs, so you confirm they load before packaging.">
                        <CommandLine command={validateCmd} testid="skillValidateCommand" />
                    </NumberedStep>
                    <NumberedStep number={3} title="Package it into a ZIP" description="Bundles the skill for this deployment's architecture — cross-compiling any Go tools and building native wheels for any Python tools.">
                        <CommandLine command={packageCmd} testid="skillPackageCommand" />
                        {!isError && targetPython && (
                            <p className="mt-2 text-xs text-(--secondary-text-wMain)" data-testid="skillPythonNote">
                                A bundled Python tool builds native wheels for the STR's Python ({targetPython}); the command above pins it so the ABI matches.
                            </p>
                        )}
                        {isError && (
                            <p className="mt-2 text-xs text-(--secondary-text-wMain)">
                                Couldn't detect the build target automatically. Set SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH (and SAM_TOOL_PYTHON_VERSION for a Python tool) to match your STR's platform.
                            </p>
                        )}
                    </NumberedStep>
                    <NumberedStep number={4} title="Upload the ZIP" description="Use the upload field on this page to add the packaged skill to Solace Agent Mesh." />
                </div>
            </LearningBanner>
        </div>
    );
}
