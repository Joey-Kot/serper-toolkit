import asyncio
import json
import logging
import math
import os
import random
import re
import tempfile
import unicodedata
from concurrent.futures import ThreadPoolExecutor
from typing import Any, Dict, List, Optional, Tuple, Union

import httpx
from dotenv import load_dotenv
from fastmcp import FastMCP

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

load_dotenv()

API_KEY: Optional[str] = os.getenv("SERPER_API_KEY")
mcp = FastMCP("serper-mcp")

ALIAS_MAP: Dict[str, str] = {}
ALIAS_KEYS_SORTED: List[str] = []
_aliases_path = os.path.join(os.path.dirname(__file__), "data", "country_aliases.json")

USER_AGENT = "serper_client/2.0"
HTTP_TIMEOUT = 30.0
DEFAULT_COUNTRY = "US"

API_ENDPOINTS = {
    "search": "https://google.serper.dev/search",
    "images": "https://google.serper.dev/images",
    "videos": "https://google.serper.dev/videos",
    "places": "https://google.serper.dev/places",
    "maps": "https://google.serper.dev/maps",
    "reviews": "https://google.serper.dev/reviews",
    "news": "https://google.serper.dev/news",
    "lens": "https://google.serper.dev/lens",
    "scholar": "https://google.serper.dev/scholar",
    "shopping": "https://google.serper.dev/shopping",
    "patents": "https://google.serper.dev/patents",
    "scrape": "https://scrape.serper.dev",
}

SEARCH_ITEMS_KEY = {
    "search": "organic",
    "images": "images",
    "videos": "videos",
    "places": "places",
    "maps": "places",
    "reviews": "reviews",
    "news": "news",
    "lens": "organic",
    "scholar": "organic",
    "shopping": "shopping",
    "patents": "organic",
}

SUPPORTED_TIME_ENDPOINTS = {"search", "images", "videos", "news"}
COUNTRY_ENDPOINTS = {"search", "images", "videos", "places", "maps", "reviews", "news", "lens", "scholar", "shopping"}
LANGUAGE_ENDPOINTS = {"search", "images", "videos", "places", "maps", "reviews", "news", "lens", "scholar", "shopping"}

SERPER_MAX_CONNECTIONS = int(os.getenv("SERPER_MAX_CONNECTIONS", "200"))
SERPER_KEEPALIVE = int(os.getenv("SERPER_KEEPALIVE", "20"))
SERPER_HTTP2 = os.getenv("SERPER_HTTP2", "0") == "1"
if SERPER_HTTP2:
    try:
        import h2  # noqa: F401
    except Exception:
        logger.warning("SERPER_HTTP2=1 but h2 is unavailable; disabling HTTP/2.")
        SERPER_HTTP2 = False

SERPER_MAX_CONCURRENT_REQUESTS = int(os.getenv("SERPER_MAX_CONCURRENT_REQUESTS", "200"))
SERPER_MAX_WORKERS = int(os.getenv("SERPER_MAX_WORKERS", "10"))
SERPER_RETRY_COUNT = int(os.getenv("SERPER_RETRY_COUNT", "3"))
SERPER_RETRY_BASE_DELAY = float(os.getenv("SERPER_RETRY_BASE_DELAY", "0.5"))

try:
    PER_ENDPOINT_MAX_CONCURRENT = json.loads(os.getenv("SERPER_ENDPOINT_CONCURRENCY", "{}"))
except Exception:
    PER_ENDPOINT_MAX_CONCURRENT = {}

try:
    PER_ENDPOINT_ALLOW_RETRY = json.loads(os.getenv("SERPER_ENDPOINT_RETRYABLE", '{"search": true, "scrape": false}'))
except Exception:
    PER_ENDPOINT_ALLOW_RETRY = {}

REQUEST_SEMAPHORE: Optional[asyncio.Semaphore] = None
ENDPOINT_SEMAPHORES: Dict[str, asyncio.Semaphore] = {}


def to_compact_json(payload: Dict[str, Any]) -> str:
    return json.dumps(payload, ensure_ascii=False, separators=(",", ":"))


def pick(d: Dict[str, Any], path: List[str], default: Any = None) -> Any:
    current: Any = d
    for key in path:
        if not isinstance(current, dict) or key not in current:
            return default
        current = current[key]
    return current


def compact_error_response(message: str, status_code: Optional[int] = None, extra: Optional[Dict[str, Any]] = None) -> str:
    result: Dict[str, Any] = {"success": False, "error": True, "message": message}
    if status_code is not None:
        result["status_code"] = status_code
    if extra:
        result.update(extra)
    return to_compact_json(result)


def normalize(text: str) -> str:
    if not text:
        return ""
    s = unicodedata.normalize("NFKD", text)
    s = "".join(ch for ch in s if not unicodedata.combining(ch))
    s = s.replace("\u3000", " ").strip().lower()
    s = re.sub(r"[^\w\s'-]", " ", s, flags=re.UNICODE)
    return re.sub(r"[_\s]+", " ", s).strip()


def binary_search(arr: List[str], target: str) -> Optional[int]:
    lo, hi = 0, len(arr) - 1
    while lo <= hi:
        mid = (lo + hi) // 2
        if arr[mid] == target:
            return mid
        if arr[mid] < target:
            lo = mid + 1
        else:
            hi = mid - 1
    return None


def _generate_variants(alias: str) -> set:
    variants = set()
    base = alias.strip()
    variants.add(base)
    norm = normalize(base)
    variants.add(norm)
    variants.add(re.sub(r"[^\w\s]", "", norm))
    if "," in base:
        parts = [p.strip() for p in base.split(",") if p.strip()]
        if len(parts) >= 2:
            reordered = " ".join(reversed(parts))
            variants.add(reordered)
            variants.add(normalize(reordered))
    if len(norm.split()) == 2:
        variants.add(" ".join(reversed(norm.split())))
    return {v for v in variants if v}


try:
    with open(_aliases_path, "r", encoding="utf-8") as f:
        _forward = json.load(f)

    for code, names in _forward.items():
        code_up = str(code).upper()
        iter_names = names if isinstance(names, list) else [names]
        for name in iter_names:
            if not isinstance(name, str):
                continue
            for variant in _generate_variants(name):
                key = normalize(variant)
                if key:
                    ALIAS_MAP[key] = code_up

    ALIAS_KEYS_SORTED = sorted(ALIAS_MAP.keys())
except FileNotFoundError:
    logger.warning("Country aliases file not found: %s", _aliases_path)
except Exception as e:
    logger.warning("Failed loading country aliases: %s", e)


def get_country_code_alpha2(country_name: Optional[str]) -> str:
    if not country_name:
        return DEFAULT_COUNTRY

    name = country_name.strip()
    if not name:
        return DEFAULT_COUNTRY

    norm = normalize(name)
    if norm in ALIAS_MAP:
        return ALIAS_MAP[norm]

    if ALIAS_KEYS_SORTED:
        idx = binary_search(ALIAS_KEYS_SORTED, norm)
        if idx is not None:
            return ALIAS_MAP.get(ALIAS_KEYS_SORTED[idx], DEFAULT_COUNTRY)

    if re.fullmatch(r"[A-Za-z]{2}", name):
        return name.upper()

    upper_norm = normalize(name.upper())
    if upper_norm in ALIAS_MAP:
        return ALIAS_MAP[upper_norm]

    return DEFAULT_COUNTRY


def map_search_time_to_tbs_param(time_period_str: Optional[str]) -> Optional[str]:
    if not time_period_str:
        return None
    s = time_period_str.strip().lower()
    allowed_qdr = {"qdr:h", "qdr:d", "qdr:w", "qdr:m", "qdr:y"}
    if s.startswith("qdr:"):
        return s if s in allowed_qdr else None

    mapping = {
        "小时": "qdr:h", "hour": "qdr:h", "h": "qdr:h",
        "天": "qdr:d", "day": "qdr:d", "d": "qdr:d",
        "周": "qdr:w", "week": "qdr:w", "w": "qdr:w",
        "月": "qdr:m", "month": "qdr:m", "m": "qdr:m",
        "年": "qdr:y", "year": "qdr:y", "y": "qdr:y",
    }
    for k, v in mapping.items():
        if k in s:
            return v
    return None


class AsyncHttpClientManager:
    _client: Optional[httpx.AsyncClient] = None
    _lock = asyncio.Lock()

    @classmethod
    async def startup(cls):
        async with cls._lock:
            if cls._client is None:
                limits = httpx.Limits(max_connections=SERPER_MAX_CONNECTIONS, max_keepalive_connections=SERPER_KEEPALIVE)
                timeout_obj = httpx.Timeout(connect=5.0, read=20.0, write=10.0, pool=30.0, timeout=HTTP_TIMEOUT)
                cls._client = httpx.AsyncClient(
                    timeout=timeout_obj,
                    headers={"User-Agent": USER_AGENT},
                    limits=limits,
                    http2=SERPER_HTTP2,
                )

    @classmethod
    def get_client(cls) -> httpx.AsyncClient:
        if cls._client is None:
            raise RuntimeError("AsyncHttpClientManager has not been started")
        return cls._client

    @classmethod
    async def shutdown(cls):
        async with cls._lock:
            if cls._client:
                await cls._client.aclose()
                cls._client = None


class ThreadPoolManager:
    _executor: Optional[ThreadPoolExecutor] = None

    @classmethod
    def startup(cls):
        if cls._executor is None:
            cls._executor = ThreadPoolExecutor(max_workers=SERPER_MAX_WORKERS)

    @classmethod
    def get_executor(cls) -> ThreadPoolExecutor:
        if cls._executor is None:
            raise RuntimeError("ThreadPoolManager has not been started")
        return cls._executor

    @classmethod
    def shutdown(cls):
        if cls._executor:
            cls._executor.shutdown(wait=True)
            cls._executor = None


async def execute_serper_request(api_name: str, payload: Dict[str, Any]) -> Union[Dict[str, Any], None]:
    if not API_KEY:
        logger.error("SERPER_API_KEY is missing")
        return None

    api_url = API_ENDPOINTS.get(api_name)
    if not api_url:
        return {"error": True, "message": f"Unknown API endpoint: {api_name}"}

    headers = {"X-API-KEY": API_KEY, "Content-Type": "application/json"}

    try:
        client = AsyncHttpClientManager.get_client()
    except RuntimeError as e:
        return {"error": True, "message": str(e)}

    sem = ENDPOINT_SEMAPHORES.get(api_name, REQUEST_SEMAPHORE)
    retry_allowed = PER_ENDPOINT_ALLOW_RETRY.get(api_name, True)

    attempt = 0
    while True:
        try:
            if sem:
                async with sem:
                    response = await client.post(api_url, json=payload, headers=headers)
            else:
                response = await client.post(api_url, json=payload, headers=headers)

            response.raise_for_status()
            return response.json()

        except httpx.HTTPStatusError as e:
            status = e.response.status_code if e.response is not None else None
            if retry_allowed and status and 500 <= status < 600 and attempt < SERPER_RETRY_COUNT:
                attempt += 1
                delay = SERPER_RETRY_BASE_DELAY * (2 ** (attempt - 1)) + random.uniform(0, 0.1)
                await asyncio.sleep(delay)
                continue
            return {"error": True, "message": f"{api_name} HTTP status error: {status}", "status_code": status}

        except httpx.RequestError as e:
            if retry_allowed and attempt < SERPER_RETRY_COUNT:
                attempt += 1
                delay = SERPER_RETRY_BASE_DELAY * (2 ** (attempt - 1)) + random.uniform(0, 0.1)
                await asyncio.sleep(delay)
                continue
            return {"error": True, "message": f"{api_name} request error: {e}"}

        except Exception as e:
            return {"error": True, "message": f"{api_name} unknown error: {e}"}


def clamp_search_num(search_num: int) -> int:
    if not isinstance(search_num, int):
        return 10
    if search_num < 1:
        return 1
    if search_num > 100:
        return 100
    return search_num


def normalize_search_num_by_endpoint(endpoint: str, requested_num: int) -> int:
    n = clamp_search_num(requested_num)
    if endpoint == "images":
        return 10 if n <= 10 else 100
    return int(math.ceil(n / 10.0) * 10)


def compute_pages_for_target(endpoint: str, effective_num: int) -> int:
    if endpoint == "images":
        return 1
    return max(1, effective_num // 10)


def _stable_unique(items: List[Dict[str, Any]], endpoint: str) -> List[Dict[str, Any]]:
    unique: List[Dict[str, Any]] = []
    seen = set()

    for item in items:
        if not isinstance(item, dict):
            continue

        key_parts = []
        for k in ("id", "link", "cid", "placeId", "publicationNumber", "productId", "title"):
            v = item.get(k)
            if isinstance(v, str) and v.strip():
                key_parts.append((k, v.strip()))
                break
        if not key_parts:
            key_parts.append(("_raw", json.dumps(item, ensure_ascii=False, sort_keys=True)))

        key = tuple(key_parts)
        if key in seen:
            continue
        seen.add(key)
        unique.append(item)

    return unique


def _merge_page_results(endpoint: str, page_results: List[Dict[str, Any]], effective_num: int) -> Dict[str, Any]:
    item_key = SEARCH_ITEMS_KEY[endpoint]
    merged_items: List[Dict[str, Any]] = []
    total_credits = 0

    for result in page_results:
        total_credits += int(result.get("credits", 0)) if isinstance(result.get("credits", 0), int) else 0
        items = result.get(item_key, [])
        if isinstance(items, list):
            merged_items.extend([x for x in items if isinstance(x, dict)])

    merged_items = _stable_unique(merged_items, endpoint)[:effective_num]

    merged: Dict[str, Any] = {item_key: merged_items, "credits": total_credits}

    first = page_results[0] if page_results else {}
    if endpoint == "search":
        merged["knowledgeGraph"] = first.get("knowledgeGraph")
        merged["peopleAlsoAsk"] = first.get("peopleAlsoAsk", [])
        merged["relatedSearches"] = first.get("relatedSearches", [])
    if endpoint == "maps":
        merged["ll"] = first.get("ll")

    return merged


async def fetch_pages_and_merge(endpoint: str, base_payload: Dict[str, Any], requested_num: int) -> Union[Tuple[Dict[str, Any], Dict[str, Any]], Tuple[None, Dict[str, Any]]]:
    effective_num = normalize_search_num_by_endpoint(endpoint, requested_num)
    pages = compute_pages_for_target(endpoint, effective_num)

    if endpoint == "maps" and pages > 1 and base_payload.get("q") and not base_payload.get("ll"):
        return None, {"message": "maps 多页聚合时参数 ll 必填", "status_code": 400}

    requests: List[Dict[str, Any]] = []

    if endpoint == "images":
        payload = dict(base_payload)
        payload["num"] = effective_num
        payload["page"] = 1
        requests.append(payload)
    else:
        for page in range(1, pages + 1):
            payload = dict(base_payload)
            payload["page"] = page
            requests.append(payload)

    async def _one(payload: Dict[str, Any]) -> Union[Dict[str, Any], None]:
        return await execute_serper_request(endpoint, payload)

    results = await asyncio.gather(*[_one(p) for p in requests])

    page_results: List[Dict[str, Any]] = []
    for result in results:
        if result is None:
            return None, {"message": f"{endpoint} returned empty response"}
        if isinstance(result, dict) and result.get("error"):
            return None, {
                "message": result.get("message", f"{endpoint} upstream error"),
                "status_code": result.get("status_code"),
            }
        if not isinstance(result, dict):
            return None, {"message": f"{endpoint} returned non-object payload"}
        page_results.append(result)

    merged = _merge_page_results(endpoint, page_results, effective_num)

    meta = {
        "requested_search_num": requested_num,
        "effective_search_num": effective_num,
        "pages_fetched": pages,
        "result_count": len(merged.get(SEARCH_ITEMS_KEY[endpoint], [])),
        "credits": merged.get("credits", 0),
    }
    return merged, meta


def map_items(items: Any, fields: List[str]) -> List[Dict[str, Any]]:
    if not isinstance(items, list):
        return []
    mapped: List[Dict[str, Any]] = []
    for item in items:
        if isinstance(item, dict):
            mapped.append({f: item.get(f, None) for f in fields})
    return mapped


def transform_general_result(raw: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "knowledge_graph": {
            "title": pick(raw, ["knowledgeGraph", "title"], None),
            "description": pick(raw, ["knowledgeGraph", "description"], None),
            "descriptionLink": pick(raw, ["knowledgeGraph", "descriptionLink"], None),
            "imageUrl": pick(raw, ["knowledgeGraph", "imageUrl"], None),
        },
        "organic": map_items(raw.get("organic", []), ["title", "link", "snippet", "date", "position"]),
        "people_also_ask": map_items(raw.get("peopleAlsoAsk", []), ["question", "title", "link", "snippet"]),
        "related_searches": map_items(raw.get("relatedSearches", []), ["query"]),
    }


def transform_images_result(raw: Dict[str, Any]) -> Dict[str, Any]:
    return {"images": map_items(raw.get("images", []), ["title", "link", "imageUrl", "thumbnailUrl", "source", "position"])}


def transform_videos_result(raw: Dict[str, Any]) -> Dict[str, Any]:
    return {"videos": map_items(raw.get("videos", []), ["title", "link", "snippet", "source", "channel", "duration", "date", "imageUrl", "position"])}


def transform_places_result(raw: Dict[str, Any]) -> Dict[str, Any]:
    return {"places": map_items(raw.get("places", []), ["title", "address", "phoneNumber", "website", "latitude", "longitude", "cid", "position"])}


def transform_maps_result(raw: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "ll": raw.get("ll"),
        "places": map_items(raw.get("places", []), [
            "title", "address", "rating", "ratingCount", "type", "website", "phoneNumber",
            "latitude", "longitude", "cid", "fid", "placeId", "position",
        ]),
    }


def transform_reviews_result(raw: Dict[str, Any]) -> Dict[str, Any]:
    result = []
    for item in raw.get("reviews", []) if isinstance(raw.get("reviews", []), list) else []:
        if not isinstance(item, dict):
            continue
        user = item.get("user", {}) if isinstance(item.get("user"), dict) else {}
        result.append({
            "rating": item.get("rating"),
            "date": item.get("date"),
            "isoDate": item.get("isoDate"),
            "snippet": item.get("snippet"),
            "id": item.get("id"),
            "user": {
                "name": user.get("name"),
                "link": user.get("link"),
                "reviews": user.get("reviews"),
                "photos": user.get("photos"),
            },
        })
    return {"reviews": result}


def transform_news_result(raw: Dict[str, Any]) -> Dict[str, Any]:
    return {"news": map_items(raw.get("news", []), ["title", "link", "snippet", "date", "source", "imageUrl"])}


def transform_lens_result(raw: Dict[str, Any]) -> Dict[str, Any]:
    return {"organic": map_items(raw.get("organic", []), ["title", "link", "source", "imageUrl", "thumbnailUrl"])}


def transform_scholar_result(raw: Dict[str, Any]) -> Dict[str, Any]:
    return {"organic": map_items(raw.get("organic", []), ["title", "link", "publicationInfo", "snippet", "year", "citedBy", "pdfUrl", "id"])}


def transform_shopping_result(raw: Dict[str, Any]) -> Dict[str, Any]:
    return {"shopping": map_items(raw.get("shopping", []), ["title", "source", "link", "price", "rating", "ratingCount", "productId", "position"])}


def transform_patents_result(raw: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "organic": map_items(raw.get("organic", []), [
            "title", "link", "snippet", "priorityDate", "filingDate", "grantDate", "publicationDate",
            "inventor", "assignee", "publicationNumber", "pdfUrl",
        ])
    }


def transform_scrape_result(raw: Dict[str, Any]) -> Dict[str, Any]:
    metadata = raw.get("metadata", {}) if isinstance(raw.get("metadata"), dict) else {}
    return {
        "title": metadata.get("title"),
        "description": metadata.get("description") or metadata.get("og:description"),
        "text": raw.get("text"),
        "markdown": raw.get("markdown"),
        "credits": raw.get("credits"),
    }


def _build_search_payload(
    endpoint: str,
    *,
    query: Optional[str] = None,
    country: Optional[str] = None,
    language: Optional[str] = None,
    search_time: Optional[str] = None,
    extra: Optional[Dict[str, Any]] = None,
) -> Dict[str, Any]:
    payload: Dict[str, Any] = {}
    if query is not None:
        payload["q"] = query

    if endpoint in COUNTRY_ENDPOINTS:
        payload["gl"] = get_country_code_alpha2(country)

    if endpoint in LANGUAGE_ENDPOINTS and language:
        payload["hl"] = language

    if endpoint in SUPPORTED_TIME_ENDPOINTS:
        tbs = map_search_time_to_tbs_param(search_time)
        if tbs:
            payload["tbs"] = tbs

    if extra:
        payload.update(extra)

    return payload


def _success_payload(meta: Dict[str, Any], data: Dict[str, Any], credits: Optional[int] = None) -> str:
    payload: Dict[str, Any] = {"success": True, "meta": meta, "data": data}
    if credits is not None:
        payload["credits"] = credits
    return to_compact_json(payload)


async def _search_tool(
    endpoint: str,
    *,
    query: Optional[str],
    search_num: int,
    country: Optional[str] = None,
    language: Optional[str] = None,
    search_time: Optional[str] = None,
    extra: Optional[Dict[str, Any]] = None,
) -> str:
    if not API_KEY:
        return compact_error_response("环境变量 SERPER_API_KEY 未设置")

    payload = _build_search_payload(
        endpoint,
        query=query,
        country=country,
        language=language,
        search_time=search_time,
        extra=extra,
    )

    merged, err = await fetch_pages_and_merge(endpoint, payload, search_num)
    if merged is None:
        return compact_error_response(err.get("message", "请求失败"), status_code=err.get("status_code"))

    transform_map = {
        "search": transform_general_result,
        "images": transform_images_result,
        "videos": transform_videos_result,
        "places": transform_places_result,
        "maps": transform_maps_result,
        "reviews": transform_reviews_result,
        "news": transform_news_result,
        "lens": transform_lens_result,
        "scholar": transform_scholar_result,
        "shopping": transform_shopping_result,
        "patents": transform_patents_result,
    }

    transformed = transform_map[endpoint](merged)
    return _success_payload(err, transformed, merged.get("credits"))


@mcp.tool(name="serper-aggregated-search")
async def serper_aggregated_search(
    query: str,
    search_num: int = 20,
    search_country: Optional[str] = None,
    search_language: Optional[str] = None,
    search_time: Optional[str] = None,
) -> str:
    if not API_KEY:
        return compact_error_response("环境变量 SERPER_API_KEY 未设置")

    web_payload = _build_search_payload("search", query=query, country=search_country, language=search_language, search_time=search_time)
    news_payload = _build_search_payload("news", query=query, country=search_country, language=search_language, search_time=search_time)
    image_payload = _build_search_payload("images", query=query, country=search_country, language=search_language, search_time=search_time)

    web_result, web_meta = await fetch_pages_and_merge("search", web_payload, search_num)
    if web_result is None:
        return compact_error_response(web_meta.get("message", "web 聚合失败"), status_code=web_meta.get("status_code"))

    news_result, news_meta = await fetch_pages_and_merge("news", news_payload, search_num)
    if news_result is None:
        return compact_error_response(news_meta.get("message", "news 聚合失败"), status_code=news_meta.get("status_code"))

    image_result, image_meta = await fetch_pages_and_merge("images", image_payload, search_num)
    if image_result is None:
        return compact_error_response(image_meta.get("message", "images 聚合失败"), status_code=image_meta.get("status_code"))

    data = {
        "web": transform_general_result(web_result).get("organic", []),
        "news": transform_news_result(news_result).get("news", []),
        "images": transform_images_result(image_result).get("images", []),
    }

    meta = {
        "requested_search_num": clamp_search_num(search_num),
        "effective_web_search_num": web_meta.get("effective_search_num"),
        "effective_news_search_num": news_meta.get("effective_search_num"),
        "effective_image_search_num": image_meta.get("effective_search_num"),
        "pages_fetched": {
            "web": web_meta.get("pages_fetched"),
            "news": news_meta.get("pages_fetched"),
            "images": image_meta.get("pages_fetched"),
        },
        "result_count": {
            "web": len(data["web"]),
            "news": len(data["news"]),
            "images": len(data["images"]),
        },
    }
    credits = int(web_result.get("credits", 0)) + int(news_result.get("credits", 0)) + int(image_result.get("credits", 0))
    return _success_payload(meta, data, credits)


@mcp.tool(name="serper-general-search")
async def serper_general_search(
    search_key_words: str,
    search_num: int = 10,
    search_country: Optional[str] = None,
    search_language: Optional[str] = None,
    search_time: Optional[str] = None,
) -> str:
    return await _search_tool("search", query=search_key_words, search_num=search_num, country=search_country, language=search_language, search_time=search_time)


@mcp.tool(name="serper-image-search")
async def serper_image_search(
    search_key_words: str,
    search_num: int = 10,
    search_country: Optional[str] = None,
    search_language: Optional[str] = None,
    search_time: Optional[str] = None,
) -> str:
    return await _search_tool("images", query=search_key_words, search_num=search_num, country=search_country, language=search_language, search_time=search_time)


@mcp.tool(name="serper-video-search")
async def serper_video_search(
    search_key_words: str,
    search_num: int = 10,
    search_country: Optional[str] = None,
    search_language: Optional[str] = None,
    search_time: Optional[str] = None,
) -> str:
    return await _search_tool("videos", query=search_key_words, search_num=search_num, country=search_country, language=search_language, search_time=search_time)


@mcp.tool(name="serper-place-search")
async def serper_place_search(
    search_key_words: str,
    search_num: int = 10,
    search_country: Optional[str] = None,
    search_language: Optional[str] = None,
    location: Optional[str] = None,
) -> str:
    extra = {"location": location} if location else None
    return await _search_tool("places", query=search_key_words, search_num=search_num, country=search_country, language=search_language, extra=extra)


@mcp.tool(name="serper-maps-search")
async def serper_maps_search(
    search_key_words: str,
    search_num: int = 10,
    ll: Optional[str] = None,
    placeId: Optional[str] = None,
    cid: Optional[str] = None,
    search_country: Optional[str] = None,
    search_language: Optional[str] = None,
) -> str:
    extra: Dict[str, Any] = {}
    if ll:
        extra["ll"] = ll
    if placeId:
        extra["placeId"] = placeId
    if cid:
        extra["cid"] = cid
    return await _search_tool("maps", query=search_key_words, search_num=search_num, country=search_country, language=search_language, extra=extra or None)


@mcp.tool(name="serper-reviews-search")
async def serper_reviews_search(
    search_num: int = 10,
    fid: Optional[str] = None,
    cid: Optional[str] = None,
    placeId: Optional[str] = None,
    sortBy: Optional[str] = None,
    search_country: Optional[str] = None,
    search_language: Optional[str] = None,
) -> str:
    if not any([fid, cid, placeId]):
        return compact_error_response("reviews 搜索至少需要 fid/cid/placeId 其中之一")

    extra: Dict[str, Any] = {}
    if fid:
        extra["fid"] = fid
    if cid:
        extra["cid"] = cid
    if placeId:
        extra["placeId"] = placeId
    if sortBy:
        extra["sortBy"] = sortBy

    return await _search_tool("reviews", query=None, search_num=search_num, country=search_country, language=search_language, extra=extra)


@mcp.tool(name="serper-news-search")
async def serper_news_search(
    search_key_words: str,
    search_num: int = 10,
    search_country: Optional[str] = None,
    search_language: Optional[str] = None,
    search_time: Optional[str] = None,
) -> str:
    return await _search_tool("news", query=search_key_words, search_num=search_num, country=search_country, language=search_language, search_time=search_time)


@mcp.tool(name="serper-lens-search")
async def serper_lens_search(
    image_url: str,
    search_num: int = 10,
    search_country: Optional[str] = None,
    search_language: Optional[str] = None,
) -> str:
    payload = _build_search_payload("lens", country=search_country, language=search_language)
    payload["url"] = image_url
    merged, err = await fetch_pages_and_merge("lens", payload, search_num)
    if merged is None:
        return compact_error_response(err.get("message", "lens 请求失败"), status_code=err.get("status_code"))
    return _success_payload(err, transform_lens_result(merged), merged.get("credits"))


@mcp.tool(name="serper-scholar-search")
async def serper_scholar_search(
    search_key_words: str,
    search_num: int = 10,
    search_country: Optional[str] = None,
    search_language: Optional[str] = None,
) -> str:
    return await _search_tool("scholar", query=search_key_words, search_num=search_num, country=search_country, language=search_language)


@mcp.tool(name="serper-shopping-search")
async def serper_shopping_search(
    search_key_words: str,
    search_num: int = 10,
    search_country: Optional[str] = None,
    search_language: Optional[str] = None,
) -> str:
    return await _search_tool("shopping", query=search_key_words, search_num=search_num, country=search_country, language=search_language)


@mcp.tool(name="serper-patents-search")
async def serper_patents_search(
    search_key_words: str,
    search_num: int = 10,
) -> str:
    return await _search_tool("patents", query=search_key_words, search_num=search_num)


@mcp.tool(name="serper-scrape")
async def serper_scrape(url: str, include_markdown: bool = False) -> str:
    if not API_KEY:
        return compact_error_response("环境变量 SERPER_API_KEY 未设置")
    if not url or not isinstance(url, str) or not url.strip():
        return compact_error_response("参数 url 必填且不能为空字符串")

    payload: Dict[str, Any] = {"url": url}
    if include_markdown:
        payload["includeMarkdown"] = True

    result = await execute_serper_request("scrape", payload)
    if result is None:
        return compact_error_response("scrape 请求失败，接口响应为空")
    if isinstance(result, dict) and result.get("error"):
        return compact_error_response(result.get("message", "scrape 请求失败"), status_code=result.get("status_code"))
    if not isinstance(result, dict):
        return compact_error_response("scrape 请求失败，接口响应格式异常")

    return _success_payload({"url": url}, transform_scrape_result(result), result.get("credits"))


async def run_blocking_task_in_threadpool(blocking_func, *args, **kwargs):
    loop = asyncio.get_running_loop()
    executor = ThreadPoolManager.get_executor()
    return await loop.run_in_executor(executor, lambda: blocking_func(*args, **kwargs))


async def startup_all():
    global REQUEST_SEMAPHORE, ENDPOINT_SEMAPHORES
    await AsyncHttpClientManager.startup()
    ThreadPoolManager.startup()
    REQUEST_SEMAPHORE = asyncio.Semaphore(SERPER_MAX_CONCURRENT_REQUESTS)
    ENDPOINT_SEMAPHORES = {}
    for endpoint_name, max_concurrent in PER_ENDPOINT_MAX_CONCURRENT.items():
        if isinstance(max_concurrent, int) and max_concurrent > 0:
            ENDPOINT_SEMAPHORES[endpoint_name] = asyncio.Semaphore(max_concurrent)


async def shutdown_all():
    await AsyncHttpClientManager.shutdown()
    ThreadPoolManager.shutdown()


def _acquire_process_lock(lock_path: str):
    def _default_lock_path() -> str:
        if os.name == "nt":
            return os.path.join(tempfile.gettempdir(), "serper_mcp.lock")
        return "/tmp/serper_mcp.lock"

    lock_path = lock_path or _default_lock_path()

    if os.name == "nt":
        try:
            fd = os.open(lock_path, os.O_CREAT | os.O_EXCL | os.O_RDWR)
            os.write(fd, str(os.getpid()).encode("utf-8"))
            return fd
        except FileExistsError:
            raise RuntimeError(f"Cannot acquire process lock ({lock_path} exists)")
    else:
        import fcntl
        f = open(lock_path, "a+")
        try:
            fcntl.flock(f.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            f.close()
            raise RuntimeError(f"Cannot acquire process lock ({lock_path} is held)")
        f.seek(0)
        f.truncate()
        f.write(str(os.getpid()))
        f.flush()
        return f


def _release_process_lock(lock_handle, lock_path: str):
    try:
        if os.name == "nt":
            os.close(lock_handle)
            try:
                os.remove(lock_path)
            except Exception:
                pass
        else:
            import fcntl
            fcntl.flock(lock_handle.fileno(), fcntl.LOCK_UN)
            lock_handle.close()
            try:
                os.remove(lock_path)
            except Exception:
                pass
    except Exception:
        pass


def _env_enabled(name: str, default: bool = False) -> bool:
    value = os.getenv(name)
    if value is None:
        return default
    return str(value).strip().lower() in ("1", "true", "yes", "on")


def main():
    if not API_KEY:
        logger.error("Warning: SERPER_API_KEY is not set")

    enable_stdio = _env_enabled("SERPER_MCP_ENABLE_STDIO", False)
    enable_sse = _env_enabled("SERPER_MCP_ENABLE_SSE", False)
    enable_http = _env_enabled("SERPER_MCP_ENABLE_HTTP", False)

    enabled = [name for name, on in (("stdio", enable_stdio), ("sse", enable_sse), ("http", enable_http)) if on]

    if len(enabled) == 0:
        raise RuntimeError(
            "No transport selected. Set one of SERPER_MCP_ENABLE_STDIO/SERPER_MCP_ENABLE_SSE/SERPER_MCP_ENABLE_HTTP to 1"
        )
    if len(enabled) > 1:
        raise RuntimeError(f"Transport conflict: enabled={enabled}. Choose exactly one transport")

    transport = enabled[0]

    default_host = "127.0.0.1"
    default_port = 7001

    if transport == "sse":
        host = os.getenv("SERPER_MCP_SSE_HOST") or os.getenv("SERPER_MCP_HOST") or default_host
        port = int(os.getenv("SERPER_MCP_SSE_PORT") or os.getenv("SERPER_MCP_PORT") or str(default_port))
    elif transport == "http":
        host = os.getenv("SERPER_MCP_HTTP_HOST") or os.getenv("SERPER_MCP_HOST") or default_host
        port = int(os.getenv("SERPER_MCP_HTTP_PORT") or os.getenv("SERPER_MCP_PORT") or str(default_port))
    else:
        host = None
        port = None

    env_lock = os.getenv("SERPER_MCP_LOCK_FILE")
    if env_lock:
        lock_path = env_lock
    else:
        if os.name == "nt":
            lock_path = os.path.join(tempfile.gettempdir(), "serper_mcp.lock")
        else:
            lock_path = "/tmp/serper_mcp.lock"

    lock_handle = _acquire_process_lock(lock_path)

    async def _serve():
        await startup_all()
        try:
            if transport == "stdio":
                await mcp.run_async()
            else:
                await mcp.run_async(transport=transport, host=host, port=port)
        finally:
            await shutdown_all()

    try:
        asyncio.run(_serve())
    except KeyboardInterrupt:
        logger.info("Interrupted")
    finally:
        _release_process_lock(lock_handle, lock_path)


if __name__ == "__main__":
    main()
