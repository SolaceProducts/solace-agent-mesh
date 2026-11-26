# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Lazy module proxies that keep heavy stdlib imports off the tool cold-start path.

A tool subprocess pays every module-level import on startup, including
``--schema`` discovery runs that never execute a tool. ``asyncio`` is the
expensive one (~8 ms: it pulls in ssl, selectors, concurrent.futures), yet it is
only needed to drive *coroutine* handlers. Synchronous function tools never
touch an event loop, so deferring asyncio until first real use removes that cost
for the common sync case while leaving async / class / provider tools unchanged.
"""


class _LazyModule:
    """Stand-in for ``import <name>`` that imports the module on first access."""

    def __init__(self, name: str) -> None:
        self.__dict__["_name"] = name
        self.__dict__["_mod"] = None

    def __getattr__(self, attr: str):
        mod = self.__dict__["_mod"]
        if mod is None:
            import importlib

            mod = self.__dict__["_mod"] = importlib.import_module(self.__dict__["_name"])
        return getattr(mod, attr)


asyncio = _LazyModule("asyncio")
