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

import json
import unittest
from pathlib import Path

from tests._stubs import install_test_stubs_if_needed

install_test_stubs_if_needed()

from serper_toolkit import server


class CountryAliasesCoverageTests(unittest.TestCase):
    def test_common_aliases_are_resolved(self):
        cases = {
            "US": "US",
            "us": "US",
            "U.S.": "US",
            "United States": "US",
            "CN": "CN",
        }
        for raw, expected in cases.items():
            with self.subTest(raw=raw):
                self.assertEqual(server.get_country_code_alpha2(raw), expected)

    def test_unknown_falls_back_to_us(self):
        self.assertEqual(server.get_country_code_alpha2("unknownland"), "US")
        self.assertEqual(server.get_country_code_alpha2(None), "US")
        self.assertEqual(server.get_country_code_alpha2(""), "US")

    def test_alias_file_is_valid_object(self):
        data_path = Path("serper_toolkit/data/country_aliases.json")
        data = json.loads(data_path.read_text(encoding="utf-8"))
        self.assertIsInstance(data, dict)
        self.assertIn("US", data)


if __name__ == "__main__":
    unittest.main()
