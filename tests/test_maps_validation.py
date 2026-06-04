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

import unittest

from tests._stubs import install_test_stubs_if_needed

install_test_stubs_if_needed()

from serper_toolkit import server


class MapsValidationTests(unittest.IsolatedAsyncioTestCase):
    async def test_maps_requires_ll_for_multi_page(self):
        merged, err = await server.fetch_pages_and_merge("maps", {"q": "coffee"}, 25)
        self.assertIsNone(merged)
        self.assertIn("ll", err["message"])


if __name__ == "__main__":
    unittest.main()
