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


class ImageNumNormalizationTests(unittest.TestCase):
    def test_image_normalization(self):
        self.assertEqual(server.normalize_search_num_by_endpoint("images", 1), 10)
        self.assertEqual(server.normalize_search_num_by_endpoint("images", 10), 10)
        self.assertEqual(server.normalize_search_num_by_endpoint("images", 11), 100)
        self.assertEqual(server.normalize_search_num_by_endpoint("images", 100), 100)

    def test_non_image_rounding(self):
        self.assertEqual(server.normalize_search_num_by_endpoint("search", 1), 10)
        self.assertEqual(server.normalize_search_num_by_endpoint("search", 25), 30)
        self.assertEqual(server.normalize_search_num_by_endpoint("search", 100), 100)


if __name__ == "__main__":
    unittest.main()
