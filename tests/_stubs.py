# Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed WITHOUT ANY WARRANTY; without even the
# implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.
# See <https://www.gnu.org/licenses/> for more details.

import sys
import types


def install_test_stubs_if_needed():
    if "httpx" not in sys.modules:
        httpx_stub = types.ModuleType("httpx")

        class _DummyAsyncClient:
            def __init__(self, *args, **kwargs):
                pass

            async def post(self, *args, **kwargs):
                raise RuntimeError("httpx stub should not execute network calls in tests")

            async def aclose(self):
                return None

        class _DummyLimits:
            def __init__(self, *args, **kwargs):
                pass

        class _DummyTimeout:
            def __init__(self, *args, **kwargs):
                pass

        class _DummyHTTPStatusError(Exception):
            def __init__(self, *args, **kwargs):
                super().__init__(*args)
                self.response = kwargs.get("response")
                self.request = kwargs.get("request")

        class _DummyRequestError(Exception):
            pass

        httpx_stub.AsyncClient = _DummyAsyncClient
        httpx_stub.Limits = _DummyLimits
        httpx_stub.Timeout = _DummyTimeout
        httpx_stub.HTTPStatusError = _DummyHTTPStatusError
        httpx_stub.RequestError = _DummyRequestError
        sys.modules["httpx"] = httpx_stub

    if "dotenv" not in sys.modules:
        dotenv_stub = types.ModuleType("dotenv")
        dotenv_stub.load_dotenv = lambda *args, **kwargs: None
        sys.modules["dotenv"] = dotenv_stub

    if "fastmcp" not in sys.modules:
        fastmcp_stub = types.ModuleType("fastmcp")

        class _DummyFastMCP:
            def __init__(self, *args, **kwargs):
                pass

            def tool(self, name=None):
                def _decorator(func):
                    return func

                return _decorator

            async def run_async(self, *args, **kwargs):
                return None

        fastmcp_stub.FastMCP = _DummyFastMCP
        sys.modules["fastmcp"] = fastmcp_stub
