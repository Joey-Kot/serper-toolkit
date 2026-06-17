package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	apiKeyEnv      = "SERPER_KEY"
	defaultCountry = "US"
)

var endpoints = map[string]string{
	"search":   "https://google.serper.dev/search",
	"images":   "https://google.serper.dev/images",
	"videos":   "https://google.serper.dev/videos",
	"places":   "https://google.serper.dev/places",
	"maps":     "https://google.serper.dev/maps",
	"reviews":  "https://google.serper.dev/reviews",
	"news":     "https://google.serper.dev/news",
	"lens":     "https://google.serper.dev/lens",
	"scholar":  "https://google.serper.dev/scholar",
	"shopping": "https://google.serper.dev/shopping",
	"patents":  "https://google.serper.dev/patents",
	"scrape":   "https://scrape.serper.dev",
}

var searchItemsKey = map[string]string{
	"search":   "organic",
	"images":   "images",
	"videos":   "videos",
	"places":   "places",
	"maps":     "places",
	"reviews":  "reviews",
	"news":     "news",
	"lens":     "organic",
	"scholar":  "organic",
	"shopping": "shopping",
	"patents":  "organic",
}

var (
	httpClient     = &http.Client{Timeout: 30 * time.Second}
	countryAliases = loadCountryAliases()
)

//go:embed data/country_aliases.json
var embeddedData embed.FS

type cliError struct {
	message string
	code    int
}

func (e cliError) Error() string {
	return e.message
}

type commandSpec struct {
	Name          string
	Endpoint      string
	QueryFlag     bool
	ImageURLFlag  bool
	CountryFlag   bool
	LanguageFlag  bool
	TimeFlag      bool
	LocationFlag  bool
	LLFlag        bool
	PlaceIDFlag   bool
	CIDFlag       bool
	FIDFlag       bool
	SortByFlag    bool
	DefaultNum    int
	RequireReview bool
}

type commandOptions struct {
	Query      string
	ImageURL   string
	SearchNum  int
	Country    string
	Language   string
	SearchTime string
	Location   string
	LL         string
	PlaceID    string
	CID        string
	FID        string
	SortBy     string
}

var commandSpecs = map[string]commandSpec{
	"general":  {Name: "general", Endpoint: "search", QueryFlag: true, CountryFlag: true, LanguageFlag: true, TimeFlag: true, DefaultNum: 10},
	"image":    {Name: "image", Endpoint: "images", QueryFlag: true, CountryFlag: true, LanguageFlag: true, TimeFlag: true, DefaultNum: 10},
	"video":    {Name: "video", Endpoint: "videos", QueryFlag: true, CountryFlag: true, LanguageFlag: true, TimeFlag: true, DefaultNum: 10},
	"place":    {Name: "place", Endpoint: "places", QueryFlag: true, CountryFlag: true, LanguageFlag: true, LocationFlag: true, DefaultNum: 10},
	"maps":     {Name: "maps", Endpoint: "maps", QueryFlag: true, CountryFlag: true, LanguageFlag: true, LLFlag: true, PlaceIDFlag: true, CIDFlag: true, DefaultNum: 10},
	"reviews":  {Name: "reviews", Endpoint: "reviews", CountryFlag: true, LanguageFlag: true, FIDFlag: true, CIDFlag: true, PlaceIDFlag: true, SortByFlag: true, DefaultNum: 10, RequireReview: true},
	"news":     {Name: "news", Endpoint: "news", QueryFlag: true, CountryFlag: true, LanguageFlag: true, TimeFlag: true, DefaultNum: 10},
	"lens":     {Name: "lens", Endpoint: "lens", ImageURLFlag: true, CountryFlag: true, LanguageFlag: true, DefaultNum: 10},
	"scholar":  {Name: "scholar", Endpoint: "scholar", QueryFlag: true, CountryFlag: true, LanguageFlag: true, DefaultNum: 10},
	"shopping": {Name: "shopping", Endpoint: "shopping", QueryFlag: true, CountryFlag: true, LanguageFlag: true, DefaultNum: 10},
	"patents":  {Name: "patents", Endpoint: "patents", QueryFlag: true, DefaultNum: 10},
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		var ce cliError
		if errors.As(err, &ce) {
			if ce.message != "" {
				fmt.Fprintln(os.Stderr, ce.message)
			}
			os.Exit(ce.code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		printRootUsage(stderr)
		return cliError{code: 2}
	}

	switch args[0] {
	case "-h", "--help", "help":
		printRootUsage(stdout)
		return nil
	case "aggregated":
		return runAggregated(args[1:], stdout, stderr)
	case "scrape":
		return runScrape(args[1:], stdout, stderr)
	default:
		if spec, ok := commandSpecs[args[0]]; ok {
			return runSearchCommand(spec, args[1:], stdout, stderr)
		}
		printRootUsage(stderr)
		return cliError{message: fmt.Sprintf("unknown subcommand: %s", args[0]), code: 2}
	}
}

func runAggregated(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("aggregated", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var query, country, language, searchTime string
	searchNum := 20
	fs.StringVar(&query, "query", "", "Search keywords. Required.")
	fs.IntVar(&searchNum, "search-num", 20, "Number of results to return. Optional. Range: 1-100. Default is 20.")
	fs.StringVar(&country, "country", "", "Country name or ISO code. Optional. Default is US.")
	fs.StringVar(&language, "language", "", "Language code, such as en. Optional.")
	fs.StringVar(&searchTime, "search-time", "", `Time filter. Optional. One of: "hour", "day", "week", "month", "year".`)
	fs.Usage = func() { printAggregatedUsage(stderr) }
	if err := fs.Parse(args); err != nil {
		return cliError{code: 2}
	}
	if strings.TrimSpace(query) == "" {
		fs.Usage()
		return cliError{message: "--query is required", code: 2}
	}
	if fs.NArg() > 0 {
		return cliError{message: fmt.Sprintf("unexpected positional arguments: %s", strings.Join(fs.Args(), " ")), code: 2}
	}
	if err := validateSearchNum(searchNum); err != nil {
		return cliError{message: err.Error(), code: 2}
	}
	if _, err := mapSearchTime(searchTime); err != nil {
		return cliError{message: err.Error(), code: 2}
	}

	web, webMeta, err := fetchPagesAndMerge("search", buildSearchPayload("search", commandOptions{Query: query, Country: country, Language: language, SearchTime: searchTime}), searchNum)
	if err != nil {
		fmt.Fprintln(stdout, compactError(err.Error(), err.StatusCode))
		return cliError{code: 1}
	}
	news, newsMeta, err := fetchPagesAndMerge("news", buildSearchPayload("news", commandOptions{Query: query, Country: country, Language: language, SearchTime: searchTime}), searchNum)
	if err != nil {
		fmt.Fprintln(stdout, compactError(err.Error(), err.StatusCode))
		return cliError{code: 1}
	}
	images, imageMeta, err := fetchPagesAndMerge("images", buildSearchPayload("images", commandOptions{Query: query, Country: country, Language: language, SearchTime: searchTime}), searchNum)
	if err != nil {
		fmt.Fprintln(stdout, compactError(err.Error(), err.StatusCode))
		return cliError{code: 1}
	}

	data := map[string]any{
		"web":    transformGeneralResult(web)["organic"],
		"news":   transformNewsResult(news)["news"],
		"images": transformImagesResult(images)["images"],
	}
	meta := map[string]any{
		"requested_search_num":       clampSearchNum(searchNum),
		"effective_web_search_num":   webMeta["effective_search_num"],
		"effective_news_search_num":  newsMeta["effective_search_num"],
		"effective_image_search_num": imageMeta["effective_search_num"],
		"pages_fetched":              map[string]any{"web": webMeta["pages_fetched"], "news": newsMeta["pages_fetched"], "images": imageMeta["pages_fetched"]},
		"result_count":               map[string]any{"web": len(data["web"].([]map[string]any)), "news": len(data["news"].([]map[string]any)), "images": len(data["images"].([]map[string]any))},
	}
	credits := intValue(web["credits"]) + intValue(news["credits"]) + intValue(images["credits"])
	fmt.Fprintln(stdout, compactJSON(successPayload(meta, data, credits)))
	return nil
}

func runSearchCommand(spec commandSpec, args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet(spec.Name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	opts := commandOptions{SearchNum: spec.DefaultNum}
	if spec.QueryFlag {
		fs.StringVar(&opts.Query, "query", "", "Search keywords. Required.")
	}
	if spec.ImageURLFlag {
		fs.StringVar(&opts.ImageURL, "image-url", "", "Public image URL. Required.")
	}
	fs.IntVar(&opts.SearchNum, "search-num", spec.DefaultNum, fmt.Sprintf("Number of results to return. Optional. Range: 1-100. Default is %d.", spec.DefaultNum))
	if spec.CountryFlag {
		fs.StringVar(&opts.Country, "country", "", "Country name or ISO code. Optional. Default is US.")
	}
	if spec.LanguageFlag {
		fs.StringVar(&opts.Language, "language", "", "Language code, such as en. Optional.")
	}
	if spec.TimeFlag {
		fs.StringVar(&opts.SearchTime, "search-time", "", `Time filter. Optional. One of: "hour", "day", "week", "month", "year".`)
	}
	if spec.LocationFlag {
		fs.StringVar(&opts.Location, "location", "", "Location hint. Optional.")
	}
	if spec.LLFlag {
		fs.StringVar(&opts.LL, "ll", "", "Latitude/longitude for maps query mode. Optional.")
	}
	if spec.PlaceIDFlag {
		fs.StringVar(&opts.PlaceID, "place-id", "", "Google place ID. Optional.")
	}
	if spec.CIDFlag {
		fs.StringVar(&opts.CID, "cid", "", "Google CID. Optional.")
	}
	if spec.FIDFlag {
		fs.StringVar(&opts.FID, "fid", "", "Google FID. Optional.")
	}
	if spec.SortByFlag {
		fs.StringVar(&opts.SortBy, "sort-by", "", "Review sort option. Optional.")
	}
	fs.Usage = func() { printSearchUsage(stderr, spec) }
	if err := fs.Parse(args); err != nil {
		return cliError{code: 2}
	}
	if fs.NArg() > 0 {
		return cliError{message: fmt.Sprintf("unexpected positional arguments: %s", strings.Join(fs.Args(), " ")), code: 2}
	}
	if spec.QueryFlag && strings.TrimSpace(opts.Query) == "" {
		fs.Usage()
		return cliError{message: "--query is required", code: 2}
	}
	if spec.ImageURLFlag && strings.TrimSpace(opts.ImageURL) == "" {
		fs.Usage()
		return cliError{message: "--image-url is required", code: 2}
	}
	if spec.RequireReview && !anyString(opts.FID, opts.CID, opts.PlaceID) {
		fs.Usage()
		return cliError{message: "reviews requires at least one of --fid, --cid, --place-id", code: 2}
	}
	if err := validateSearchNum(opts.SearchNum); err != nil {
		return cliError{message: err.Error(), code: 2}
	}
	if _, err := mapSearchTime(opts.SearchTime); err != nil {
		return cliError{message: err.Error(), code: 2}
	}

	payload := buildSearchPayload(spec.Endpoint, opts)
	merged, meta, upstreamErr := fetchPagesAndMerge(spec.Endpoint, payload, opts.SearchNum)
	if upstreamErr != nil {
		fmt.Fprintln(stdout, compactError(upstreamErr.Error(), upstreamErr.StatusCode))
		return cliError{code: 1}
	}
	fmt.Fprintln(stdout, compactJSON(successPayload(meta, transformResult(spec.Endpoint, merged), intValue(merged["credits"]))))
	return nil
}

func runScrape(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("scrape", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var output, targetURL string
	includeMarkdown := true
	fs.StringVar(&output, "output", "", "Export name. Required. The result is saved as <output>.md in the current directory.")
	fs.StringVar(&targetURL, "url", "", "Target URL to scrape. Required.")
	fs.BoolVar(&includeMarkdown, "include-markdown", true, "Request markdown content. Optional. Default is true.")
	fs.Usage = func() { printScrapeUsage(stderr) }
	if err := fs.Parse(args); err != nil {
		return cliError{code: 2}
	}
	if strings.TrimSpace(output) == "" {
		fs.Usage()
		return cliError{message: "--output is required", code: 2}
	}
	if strings.TrimSpace(targetURL) == "" {
		fs.Usage()
		return cliError{message: "--url is required", code: 2}
	}
	if fs.NArg() > 0 {
		return cliError{message: fmt.Sprintf("unexpected positional arguments: %s", strings.Join(fs.Args(), " ")), code: 2}
	}

	payload := map[string]any{"url": targetURL}
	if includeMarkdown {
		payload["includeMarkdown"] = true
	}
	raw, err := serperPost("scrape", payload)
	if err != nil {
		fmt.Fprintln(stdout, "false")
		fmt.Fprintln(stdout, err.Error())
		return cliError{code: 1}
	}
	result := transformScrapeResult(raw)
	path := outputPath(output)
	if err := os.WriteFile(path, []byte(renderMarkdownFile(result)), 0o644); err != nil {
		fmt.Fprintln(stdout, "false")
		fmt.Fprintln(stdout, err.Error())
		return cliError{code: 1}
	}
	fmt.Fprintln(stdout, "true")
	return nil
}

func buildSearchPayload(endpoint string, opts commandOptions) map[string]any {
	payload := map[string]any{}
	if opts.Query != "" {
		payload["q"] = opts.Query
	}
	if endpoint == "lens" && opts.ImageURL != "" {
		payload["url"] = opts.ImageURL
	}
	if supportsCountry(endpoint) {
		payload["gl"] = getCountryCodeAlpha2(opts.Country)
	}
	if supportsLanguage(endpoint) && opts.Language != "" {
		payload["hl"] = opts.Language
	}
	if supportsTime(endpoint) {
		if tbs, _ := mapSearchTime(opts.SearchTime); tbs != "" {
			payload["tbs"] = tbs
		}
	}
	if opts.Location != "" {
		payload["location"] = opts.Location
	}
	if opts.LL != "" {
		payload["ll"] = opts.LL
	}
	if opts.PlaceID != "" {
		payload["placeId"] = opts.PlaceID
	}
	if opts.CID != "" {
		payload["cid"] = opts.CID
	}
	if opts.FID != "" {
		payload["fid"] = opts.FID
	}
	if opts.SortBy != "" {
		payload["sortBy"] = opts.SortBy
	}
	return payload
}

func fetchPagesAndMerge(endpoint string, basePayload map[string]any, requestedNum int) (map[string]any, map[string]any, *upstreamError) {
	effectiveNum := normalizeSearchNumByEndpoint(endpoint, requestedNum)
	pages := computePagesForTarget(endpoint, effectiveNum)
	if endpoint == "maps" && pages > 1 && hasString(basePayload, "q") && !hasString(basePayload, "ll") {
		return nil, nil, &upstreamError{Message: "maps multi-page aggregation requires --ll in query mode", StatusCode: 400}
	}

	results := make([]map[string]any, 0, pages)
	if endpoint == "images" {
		payload := copyMap(basePayload)
		payload["num"] = effectiveNum
		payload["page"] = 1
		raw, err := serperPost(endpoint, payload)
		if err != nil {
			return nil, nil, err
		}
		results = append(results, raw)
	} else {
		for page := 1; page <= pages; page++ {
			payload := copyMap(basePayload)
			payload["page"] = page
			raw, err := serperPost(endpoint, payload)
			if err != nil {
				return nil, nil, err
			}
			results = append(results, raw)
		}
	}

	merged := mergePageResults(endpoint, results, effectiveNum)
	meta := map[string]any{
		"requested_search_num": requestedNum,
		"effective_search_num": effectiveNum,
		"pages_fetched":        pages,
		"result_count":         len(asObjects(merged[searchItemsKey[endpoint]])),
		"credits":              intValue(merged["credits"]),
	}
	return merged, meta, nil
}

type upstreamError struct {
	Message    string
	StatusCode int
}

func (e *upstreamError) Error() string {
	return e.Message
}

func serperPost(endpointName string, payload map[string]any) (map[string]any, *upstreamError) {
	key := strings.TrimSpace(os.Getenv(apiKeyEnv))
	if key == "" {
		return nil, &upstreamError{Message: apiKeyEnv + " is required"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, &upstreamError{Message: err.Error()}
	}
	req, err := http.NewRequest(http.MethodPost, endpoints[endpointName], bytes.NewReader(body))
	if err != nil {
		return nil, &upstreamError{Message: err.Error()}
	}
	req.Header.Set("X-API-KEY", key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "serper_cli/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, &upstreamError{Message: fmt.Sprintf("%s request error: %s", endpointName, err)}
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, &upstreamError{Message: readErr.Error()}
	}
	var parsed map[string]any
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return nil, &upstreamError{Message: fmt.Sprintf("%s response JSON parse failed: %s", endpointName, err)}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &upstreamError{Message: fmt.Sprintf("%s HTTP status error: %d", endpointName, resp.StatusCode), StatusCode: resp.StatusCode}
	}
	if parsed == nil {
		return nil, &upstreamError{Message: endpointName + " response is empty"}
	}
	if parsed["error"] == true {
		return nil, &upstreamError{Message: stringValue(parsed["message"]), StatusCode: intValue(parsed["statusCode"])}
	}
	return parsed, nil
}

func mergePageResults(endpoint string, pageResults []map[string]any, effectiveNum int) map[string]any {
	itemKey := searchItemsKey[endpoint]
	var mergedItems []map[string]any
	totalCredits := 0
	for _, result := range pageResults {
		totalCredits += intValue(result["credits"])
		mergedItems = append(mergedItems, asObjects(result[itemKey])...)
	}
	mergedItems = stableUniqueObjects(mergedItems)
	if len(mergedItems) > effectiveNum {
		mergedItems = mergedItems[:effectiveNum]
	}
	merged := map[string]any{itemKey: mergedItems, "credits": totalCredits}
	first := map[string]any{}
	if len(pageResults) > 0 {
		first = pageResults[0]
	}
	if endpoint == "search" {
		merged["knowledgeGraph"] = first["knowledgeGraph"]
		merged["peopleAlsoAsk"] = valueOrEmptyList(first["peopleAlsoAsk"])
		merged["relatedSearches"] = valueOrEmptyList(first["relatedSearches"])
	}
	if endpoint == "maps" {
		merged["ll"] = first["ll"]
	}
	return merged
}

func transformResult(endpoint string, raw map[string]any) map[string]any {
	switch endpoint {
	case "search":
		return transformGeneralResult(raw)
	case "images":
		return transformImagesResult(raw)
	case "videos":
		return map[string]any{"videos": mapItems(raw["videos"], []string{"title", "link", "snippet", "source", "channel", "duration", "date", "imageUrl", "position"})}
	case "places":
		return map[string]any{"places": mapItems(raw["places"], []string{"title", "address", "phoneNumber", "website", "latitude", "longitude", "cid", "position"})}
	case "maps":
		return map[string]any{"ll": raw["ll"], "places": mapItems(raw["places"], []string{"title", "address", "rating", "ratingCount", "type", "website", "phoneNumber", "latitude", "longitude", "cid", "fid", "placeId", "position"})}
	case "reviews":
		return transformReviewsResult(raw)
	case "news":
		return transformNewsResult(raw)
	case "lens":
		return map[string]any{"organic": mapItems(raw["organic"], []string{"title", "link", "source", "imageUrl", "thumbnailUrl"})}
	case "scholar":
		return map[string]any{"organic": mapItems(raw["organic"], []string{"title", "link", "publicationInfo", "snippet", "year", "citedBy", "pdfUrl", "id"})}
	case "shopping":
		return map[string]any{"shopping": mapItems(raw["shopping"], []string{"title", "source", "link", "price", "rating", "ratingCount", "productId", "position"})}
	case "patents":
		return map[string]any{"organic": mapItems(raw["organic"], []string{"title", "link", "snippet", "priorityDate", "filingDate", "grantDate", "publicationDate", "inventor", "assignee", "publicationNumber", "pdfUrl"})}
	default:
		return map[string]any{}
	}
}

func transformGeneralResult(raw map[string]any) map[string]any {
	return map[string]any{
		"knowledge_graph": map[string]any{
			"title":           pick(raw, "knowledgeGraph", "title"),
			"description":     pick(raw, "knowledgeGraph", "description"),
			"descriptionLink": pick(raw, "knowledgeGraph", "descriptionLink"),
			"imageUrl":        pick(raw, "knowledgeGraph", "imageUrl"),
		},
		"organic":          mapItems(raw["organic"], []string{"title", "link", "snippet", "date", "position"}),
		"people_also_ask":  mapItems(raw["peopleAlsoAsk"], []string{"question", "title", "link", "snippet"}),
		"related_searches": mapItems(raw["relatedSearches"], []string{"query"}),
	}
}

func transformImagesResult(raw map[string]any) map[string]any {
	return map[string]any{"images": mapItems(raw["images"], []string{"title", "link", "imageUrl", "thumbnailUrl", "source", "position"})}
}

func transformNewsResult(raw map[string]any) map[string]any {
	return map[string]any{"news": mapItems(raw["news"], []string{"title", "link", "snippet", "date", "source", "imageUrl"})}
}

func transformReviewsResult(raw map[string]any) map[string]any {
	reviews := []map[string]any{}
	for _, item := range asObjects(raw["reviews"]) {
		user, _ := item["user"].(map[string]any)
		reviews = append(reviews, map[string]any{
			"rating":  item["rating"],
			"date":    item["date"],
			"isoDate": item["isoDate"],
			"snippet": item["snippet"],
			"id":      item["id"],
			"user": map[string]any{
				"name":    valueOrNil(user, "name"),
				"link":    valueOrNil(user, "link"),
				"reviews": valueOrNil(user, "reviews"),
				"photos":  valueOrNil(user, "photos"),
			},
		})
	}
	return map[string]any{"reviews": reviews}
}

func transformScrapeResult(raw map[string]any) map[string]any {
	metadata, _ := raw["metadata"].(map[string]any)
	markdown := stringValue(raw["markdown"])
	if decoded, err := url.PathUnescape(markdown); err == nil {
		markdown = decoded
	}
	markdown = strings.ReplaceAll(markdown, `\n`, "\n")
	return map[string]any{
		"title":       valueOrNil(metadata, "title"),
		"description": firstNonEmpty(valueOrNil(metadata, "description"), valueOrNil(metadata, "og:description")),
		"text":        raw["text"],
		"markdown":    markdown,
		"credits":     raw["credits"],
	}
}

func renderMarkdownFile(result map[string]any) string {
	return fmt.Sprintf("## title: %s\n## description: %s\n## credits: %s\n\n---\n\n%s\n",
		stringValue(result["title"]),
		stringValue(result["description"]),
		stringValue(result["credits"]),
		stringValue(result["markdown"]),
	)
}

func successPayload(meta map[string]any, data map[string]any, credits int) map[string]any {
	payload := map[string]any{"success": true, "meta": meta, "data": data}
	payload["credits"] = credits
	return payload
}

func compactError(message string, statusCode int) string {
	payload := map[string]any{"success": false, "error": true, "message": message}
	if statusCode > 0 {
		payload["status_code"] = statusCode
	}
	return compactJSON(payload)
}

func compactJSON(payload any) string {
	out, err := json.Marshal(payload)
	if err != nil {
		return `{"success":false,"error":true,"message":"failed to encode JSON"}`
	}
	return string(out)
}

func mapItems(items any, fields []string) []map[string]any {
	rawItems := asObjects(items)
	mapped := make([]map[string]any, 0, len(rawItems))
	for _, item := range rawItems {
		row := make(map[string]any, len(fields))
		for _, field := range fields {
			row[field] = valueOrNil(item, field)
		}
		mapped = append(mapped, row)
	}
	return mapped
}

func pick(obj map[string]any, path ...string) any {
	var current any = obj
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[key]
	}
	return current
}

func valueOrNil(obj map[string]any, key string) any {
	if obj == nil {
		return nil
	}
	if val, ok := obj[key]; ok {
		return val
	}
	return nil
}

func firstNonEmpty(values ...any) any {
	for _, value := range values {
		if strings.TrimSpace(stringValue(value)) != "" {
			return value
		}
	}
	return nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}

func asObjects(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]map[string]any); ok {
			return typed
		}
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if obj, ok := item.(map[string]any); ok {
			result = append(result, obj)
		}
	}
	return result
}

func stableUniqueObjects(items []map[string]any) []map[string]any {
	unique := make([]map[string]any, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		key := uniqueKey(item)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, item)
	}
	return unique
}

func uniqueKey(item map[string]any) string {
	for _, key := range []string{"id", "link", "cid", "placeId", "publicationNumber", "productId", "title"} {
		if value := strings.TrimSpace(stringValue(item[key])); value != "" {
			return key + ":" + value
		}
	}
	out, _ := json.Marshal(item)
	return string(out)
}

func copyMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func valueOrEmptyList(value any) any {
	if value == nil {
		return []any{}
	}
	return value
}

func hasString(obj map[string]any, key string) bool {
	return strings.TrimSpace(stringValue(obj[key])) != ""
}

func anyString(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func validateSearchNum(searchNum int) error {
	if searchNum < 1 || searchNum > 100 {
		return fmt.Errorf("--search-num must be an integer from 1 to 100")
	}
	return nil
}

func clampSearchNum(searchNum int) int {
	if searchNum < 1 {
		return 1
	}
	if searchNum > 100 {
		return 100
	}
	return searchNum
}

func normalizeSearchNumByEndpoint(endpoint string, requestedNum int) int {
	n := clampSearchNum(requestedNum)
	if endpoint == "images" {
		if n <= 10 {
			return 10
		}
		return 100
	}
	return int(math.Ceil(float64(n)/10.0) * 10)
}

func computePagesForTarget(endpoint string, effectiveNum int) int {
	if endpoint == "images" {
		return 1
	}
	pages := effectiveNum / 10
	if pages < 1 {
		return 1
	}
	return pages
}

func mapSearchTime(value string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "":
		return "", nil
	case "hour", "h":
		return "qdr:h", nil
	case "day", "d":
		return "qdr:d", nil
	case "week", "w":
		return "qdr:w", nil
	case "month", "m":
		return "qdr:m", nil
	case "year", "y":
		return "qdr:y", nil
	case "qdr:h", "qdr:d", "qdr:w", "qdr:m", "qdr:y":
		return strings.ToLower(value), nil
	default:
		return "", fmt.Errorf(`--search-time must be one of "hour", "day", "week", "month", "year"`)
	}
}

func supportsCountry(endpoint string) bool {
	switch endpoint {
	case "search", "images", "videos", "places", "maps", "reviews", "news", "lens", "scholar", "shopping":
		return true
	default:
		return false
	}
}

func supportsLanguage(endpoint string) bool {
	return supportsCountry(endpoint)
}

func supportsTime(endpoint string) bool {
	switch endpoint {
	case "search", "images", "videos", "news":
		return true
	default:
		return false
	}
}

func outputPath(output string) string {
	name := filepath.Base(strings.TrimSpace(output))
	if !strings.HasSuffix(strings.ToLower(name), ".md") {
		name += ".md"
	}
	return filepath.Join(".", name)
}

func loadCountryAliases() map[string]string {
	data, err := embeddedData.ReadFile("data/country_aliases.json")
	if err != nil {
		return map[string]string{}
	}
	var raw map[string][]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[string]string{}
	}
	aliases := map[string]string{}
	for code, names := range raw {
		code = strings.ToUpper(code)
		for _, name := range names {
			for _, variant := range countryVariants(name) {
				key := normalizeCountry(variant)
				if key != "" {
					aliases[key] = code
				}
			}
		}
	}
	return aliases
}

func getCountryCodeAlpha2(country string) string {
	name := strings.TrimSpace(country)
	if name == "" {
		return defaultCountry
	}
	norm := normalizeCountry(name)
	if code, ok := countryAliases[norm]; ok {
		return code
	}
	if len([]rune(name)) == 2 && isLetters(name) {
		return strings.ToUpper(name)
	}
	return defaultCountry
}

func countryVariants(alias string) []string {
	base := strings.TrimSpace(alias)
	if base == "" {
		return nil
	}
	variants := []string{base, normalizeCountry(base), stripCountryPunctuation(normalizeCountry(base))}
	if strings.Contains(base, ",") {
		parts := strings.Split(base, ",")
		cleaned := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				cleaned = append(cleaned, part)
			}
		}
		if len(cleaned) >= 2 {
			for i, j := 0, len(cleaned)-1; i < j; i, j = i+1, j-1 {
				cleaned[i], cleaned[j] = cleaned[j], cleaned[i]
			}
			reordered := strings.Join(cleaned, " ")
			variants = append(variants, reordered, normalizeCountry(reordered))
		}
	}
	parts := strings.Fields(normalizeCountry(base))
	if len(parts) == 2 {
		variants = append(variants, parts[1]+" "+parts[0])
	}
	return stableUniqueStrings(variants)
}

func normalizeCountry(value string) string {
	value = strings.ReplaceAll(value, "\u3000", " ")
	value = strings.TrimSpace(strings.ToLower(value))
	var b strings.Builder
	previousSpace := false
	for _, r := range value {
		switch {
		case r == '_' || unicode.IsSpace(r):
			if !previousSpace {
				b.WriteRune(' ')
				previousSpace = true
			}
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '-':
			b.WriteRune(r)
			previousSpace = false
		default:
			if !previousSpace {
				b.WriteRune(' ')
				previousSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func stripCountryPunctuation(value string) string {
	re := regexp.MustCompile(`[^\p{L}\p{N}\s]`)
	return strings.TrimSpace(re.ReplaceAllString(value, ""))
}

func isLetters(value string) bool {
	for _, r := range value {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func stableUniqueStrings(items []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	return result
}

func printRootUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  serper aggregated --query <keywords> [--search-num <1-100>] [--country <country>] [--language <lang>] [--search-time <hour|day|week|month|year>]
  serper general    --query <keywords> [--search-num <1-100>] [--country <country>] [--language <lang>] [--search-time <hour|day|week|month|year>]
  serper image      --query <keywords> [--search-num <1-100>] [--country <country>] [--language <lang>] [--search-time <hour|day|week|month|year>]
  serper video      --query <keywords> [--search-num <1-100>] [--country <country>] [--language <lang>] [--search-time <hour|day|week|month|year>]
  serper place      --query <keywords> [--search-num <1-100>] [--country <country>] [--language <lang>] [--location <location>]
  serper maps       --query <keywords> [--search-num <1-100>] [--ll <lat,lng>] [--place-id <id>] [--cid <cid>] [--country <country>] [--language <lang>]
  serper reviews    [--search-num <1-100>] (--fid <fid> | --cid <cid> | --place-id <id>) [--sort-by <sort>] [--country <country>] [--language <lang>]
  serper news       --query <keywords> [--search-num <1-100>] [--country <country>] [--language <lang>] [--search-time <hour|day|week|month|year>]
  serper lens       --image-url <url> [--search-num <1-100>] [--country <country>] [--language <lang>]
  serper scholar    --query <keywords> [--search-num <1-100>] [--country <country>] [--language <lang>]
  serper shopping   --query <keywords> [--search-num <1-100>] [--country <country>] [--language <lang>]
  serper patents    --query <keywords> [--search-num <1-100>]
  serper scrape     --output <name> --url <url> [--include-markdown]

The API key is read from SERPER_KEY.

`)
}

func printAggregatedUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  serper aggregated --query <keywords> [--search-num <1-100>] [--country <country>] [--language <lang>] [--search-time <hour|day|week|month|year>]

Parameters:
  --query        Search keywords. Required.
  --search-num   Number of results to return. Optional. Range: 1-100. Default is 20.
  --country      Country name or ISO code. Optional. Default is US.
  --language     Language code, such as en. Optional.
  --search-time  Time filter. Optional. One of: "hour", "day", "week", "month", "year".

Output:
  Compact single-line JSON with success, meta, data.web, data.news, data.images, and credits.

`)
}

func printSearchUsage(w io.Writer, spec commandSpec) {
	fmt.Fprintf(w, "Usage:\n  serper %s", spec.Name)
	if spec.QueryFlag {
		fmt.Fprint(w, " --query <keywords>")
	}
	if spec.ImageURLFlag {
		fmt.Fprint(w, " --image-url <url>")
	}
	fmt.Fprint(w, " [--search-num <1-100>]")
	if spec.CountryFlag {
		fmt.Fprint(w, " [--country <country>]")
	}
	if spec.LanguageFlag {
		fmt.Fprint(w, " [--language <lang>]")
	}
	if spec.TimeFlag {
		fmt.Fprint(w, " [--search-time <hour|day|week|month|year>]")
	}
	if spec.LocationFlag {
		fmt.Fprint(w, " [--location <location>]")
	}
	if spec.LLFlag {
		fmt.Fprint(w, " [--ll <lat,lng>]")
	}
	if spec.PlaceIDFlag {
		fmt.Fprint(w, " [--place-id <id>]")
	}
	if spec.CIDFlag {
		fmt.Fprint(w, " [--cid <cid>]")
	}
	if spec.FIDFlag {
		fmt.Fprint(w, " [--fid <fid>]")
	}
	if spec.SortByFlag {
		fmt.Fprint(w, " [--sort-by <sort>]")
	}
	fmt.Fprint(w, "\n\nParameters:\n")
	if spec.QueryFlag {
		fmt.Fprint(w, "  --query       Search keywords. Required.\n")
	}
	if spec.ImageURLFlag {
		fmt.Fprint(w, "  --image-url   Public image URL. Required.\n")
	}
	fmt.Fprintf(w, "  --search-num  Number of results to return. Optional. Range: 1-100. Default is %d.\n", spec.DefaultNum)
	if spec.CountryFlag {
		fmt.Fprint(w, "  --country     Country name or ISO code. Optional. Default is US.\n")
	}
	if spec.LanguageFlag {
		fmt.Fprint(w, "  --language    Language code, such as en. Optional.\n")
	}
	if spec.TimeFlag {
		fmt.Fprint(w, "  --search-time Time filter. Optional. One of: \"hour\", \"day\", \"week\", \"month\", \"year\".\n")
	}
	if spec.LocationFlag {
		fmt.Fprint(w, "  --location    Location hint. Optional.\n")
	}
	if spec.LLFlag {
		fmt.Fprint(w, "  --ll          Latitude/longitude for maps query mode. Required when maps query aggregation fetches more than one page.\n")
	}
	if spec.PlaceIDFlag {
		fmt.Fprint(w, "  --place-id    Google place ID. Optional.\n")
	}
	if spec.CIDFlag {
		fmt.Fprint(w, "  --cid         Google CID. Optional.\n")
	}
	if spec.FIDFlag {
		fmt.Fprint(w, "  --fid         Google FID. Optional.\n")
	}
	if spec.SortByFlag {
		fmt.Fprint(w, "  --sort-by     Review sort option. Optional.\n")
	}
	fmt.Fprint(w, "\nOutput:\n  Compact single-line JSON with success, meta, data, and credits.\n\n")
}

func printScrapeUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  serper scrape --output <name> --url <url> [--include-markdown]

Parameters:
  --output            Export name. Required. The result is saved as <output>.md in the current directory.
  --url               Target URL to scrape. Required.
  --include-markdown  Request markdown content. Optional. Default is true.

Output:
  true on success. The markdown export is written only after a successful scrape.
  false followed by an error reason on failure. Existing files are not created or overwritten on failure.

`)
}
