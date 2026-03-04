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
