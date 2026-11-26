# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The runtime's entry point. AgentCore runs `main.py`; `python main.py announce` and `python main.py retire` describe
the agent to SAM and retire it, over the agent link (see sam_link_sdk.agentcore)."""

from agent import build_agent

from sam_link_sdk.agentcore import serve

if __name__ == "__main__":
    serve(build_agent)
