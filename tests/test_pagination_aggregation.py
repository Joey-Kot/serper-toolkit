import unittest

from tests._stubs import install_test_stubs_if_needed

install_test_stubs_if_needed()

from serper_toolkit import server


class PaginationAggregationTests(unittest.IsolatedAsyncioTestCase):
    async def test_non_image_search_rounds_to_ten_and_dedup(self):
        calls = []

        async def fake_execute(api_name, payload):
            calls.append((api_name, payload))
            page = payload.get("page", 1)
            return {
                "organic": [
                    {"title": f"a-{page}", "link": f"https://example.com/{page}", "position": 1},
                    {"title": "dup", "link": "https://example.com/dup", "position": 2},
                ],
                "credits": 1,
            }

        old = server.execute_serper_request
        server.execute_serper_request = fake_execute
        try:
            merged, meta = await server.fetch_pages_and_merge("search", {"q": "x"}, 25)
        finally:
            server.execute_serper_request = old

        self.assertIsNotNone(merged)
        self.assertEqual(meta["effective_search_num"], 30)
        self.assertEqual(meta["pages_fetched"], 3)
        self.assertEqual(len(calls), 3)
        links = [x["link"] for x in merged["organic"]]
        self.assertEqual(len(links), len(set(links)))


if __name__ == "__main__":
    unittest.main()
