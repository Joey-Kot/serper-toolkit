import json
import unittest

from tests._stubs import install_test_stubs_if_needed

install_test_stubs_if_needed()

from serper_toolkit import server


class ResponseTransformTests(unittest.TestCase):
    def test_general_mapping(self):
        raw = {
            "knowledgeGraph": {"title": "A", "description": "D", "descriptionLink": "L", "imageUrl": "I"},
            "organic": [{"title": "t", "link": "u", "snippet": "s", "date": "today", "position": 1}],
            "peopleAlsoAsk": [{"question": "q", "title": "t2", "link": "u2", "snippet": "s2"}],
            "relatedSearches": [{"query": "x"}],
        }
        out = server.transform_general_result(raw)
        self.assertEqual(out["knowledge_graph"]["title"], "A")
        self.assertEqual(out["organic"][0]["position"], 1)
        self.assertEqual(out["people_also_ask"][0]["question"], "q")
        self.assertEqual(out["related_searches"][0]["query"], "x")

    def test_shopping_mapping_drops_image_base64(self):
        raw = {
            "shopping": [
                {
                    "title": "p",
                    "source": "s",
                    "link": "u",
                    "price": "$1",
                    "imageUrl": "data:image/webp;base64,AAAA",
                    "rating": 4.9,
                    "ratingCount": 9,
                    "productId": "pid",
                    "position": 1,
                }
            ]
        }
        out = server.transform_shopping_result(raw)
        self.assertNotIn("imageUrl", out["shopping"][0])

    def test_success_payload_is_single_line(self):
        payload = server._success_payload({"a": 1}, {"b": 2}, 3)
        self.assertNotIn("\n", payload)
        parsed = json.loads(payload)
        self.assertEqual(parsed["success"], True)


if __name__ == "__main__":
    unittest.main()
