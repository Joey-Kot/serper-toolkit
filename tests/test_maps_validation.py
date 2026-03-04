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
