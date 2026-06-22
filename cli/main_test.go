package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGeneralCommandOutputsCompactMappedJSON(t *testing.T) {
	t.Setenv(apiKeyEnv, "test-key")
	var calls []map[string]any
	setMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		assertRequestTimeout(t, r, defaultTimeoutSecs)
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("X-API-KEY"); got != "test-key" {
			t.Fatalf("X-API-KEY = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, payload)
		page := int(payload["page"].(float64))
		return jsonResponse(200, `{"organic":[{"title":"t","link":"https://example.com/`+string(rune('0'+page))+`","snippet":"s","position":1,"extra":"drop"}],"knowledgeGraph":{"title":"kg"},"peopleAlsoAsk":[],"relatedSearches":[],"credits":1}`), nil
	})
	old := endpoints["search"]
	endpoints["search"] = "https://example.test/search"
	t.Cleanup(func() { endpoints["search"] = old })

	var stdout, stderr bytes.Buffer
	err := run([]string{"general", "--query", "ai", "--country", "United Kingdom", "--language", "en", "--search-num", "15", "--search-time", "month"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run returned error: %v; stderr=%s", err, stderr.String())
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[0]["gl"] != "GB" || calls[0]["hl"] != "en" || calls[0]["tbs"] != "qdr:m" {
		t.Fatalf("payload = %#v", calls[0])
	}
	out := strings.TrimSpace(stdout.String())
	if strings.Contains(out, "\n") || strings.Contains(out, " ") {
		t.Fatalf("expected compact JSON, got %q", out)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	meta := parsed["meta"].(map[string]any)
	if meta["effective_search_num"] != float64(20) {
		t.Fatalf("meta = %#v", meta)
	}
	data := parsed["data"].(map[string]any)
	organic := data["organic"].([]any)[0].(map[string]any)
	if _, ok := organic["extra"]; ok {
		t.Fatalf("unexpected extra field: %#v", organic)
	}
}

func TestImageCommandNormalizesNumToTenOrHundred(t *testing.T) {
	t.Setenv(apiKeyEnv, "test-key")
	setMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["num"] != float64(100) {
			t.Fatalf("num = %#v, want 100", payload["num"])
		}
		return jsonResponse(200, `{"images":[{"title":"i","link":"l","imageUrl":"iu","position":1}],"credits":2}`), nil
	})
	old := endpoints["images"]
	endpoints["images"] = "https://example.test/images"
	t.Cleanup(func() { endpoints["images"] = old })

	var stdout, stderr bytes.Buffer
	err := run([]string{"image", "--query", "logo", "--search-num", "11"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run returned error: %v; stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"effective_search_num":100`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestMapsValidationRequiresLLForMultiPageQuery(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"maps", "--query", "coffee", "--search-num", "25"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected maps validation failure")
	}
	if !strings.Contains(stdout.String(), "requires --ll") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestReviewsValidation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"reviews"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "reviews requires") {
		t.Fatalf("expected reviews validation error, got %v", err)
	}
}

func TestTimeoutValidation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"general", "--query", "ai", "--timeout", "0"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--timeout") {
		t.Fatalf("expected timeout validation error, got %v", err)
	}
}

func TestScrapeCommandWritesMarkdownFileOnSuccess(t *testing.T) {
	t.Setenv(apiKeyEnv, "test-key")
	setMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		assertRequestTimeout(t, r, 7)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["includeMarkdown"] != true {
			t.Fatalf("includeMarkdown = %#v", payload["includeMarkdown"])
		}
		return jsonResponse(200, `{"metadata":{"title":"T","description":"D","og:url":"https://metadata.example/page"},"markdown":"hello%20world\\nnext","credits":3}`), nil
	})
	old := endpoints["scrape"]
	endpoints["scrape"] = "https://example.test/scrape"
	t.Cleanup(func() { endpoints["scrape"] = old })

	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	var stdout, stderr bytes.Buffer
	err = run([]string{"scrape", "--output", "page", "--url", "https://example.com", "--timeout", "7"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run returned error: %v; stderr=%s", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "true" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "page.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{"## title: T", "## description: D", "## url: https://metadata.example/page", "## credits: 3", "hello world\nnext"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
}

func TestScrapeCommandUsesInputURLWhenMetadataURLIsMissing(t *testing.T) {
	t.Setenv(apiKeyEnv, "test-key")
	setMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"metadata":{"title":"T","description":"D"},"markdown":"body","credits":1}`), nil
	})
	old := endpoints["scrape"]
	endpoints["scrape"] = "https://example.test/scrape"
	t.Cleanup(func() { endpoints["scrape"] = old })

	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	var stdout, stderr bytes.Buffer
	err = run([]string{"scrape", "--output", "page", "--url", "https://input.example/page"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run returned error: %v; stderr=%s", err, stderr.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "page.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "## url: https://input.example/page") {
		t.Fatalf("missing fallback url:\n%s", string(content))
	}
}

func TestScrapeCommandCreatesOutputPathBeforeWritingFile(t *testing.T) {
	t.Setenv(apiKeyEnv, "test-key")
	setMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"metadata":{"title":"T"},"markdown":"body","credits":1}`), nil
	})
	old := endpoints["scrape"]
	endpoints["scrape"] = "https://example.test/scrape"
	t.Cleanup(func() { endpoints["scrape"] = old })

	dir := t.TempDir()
	outputDir := filepath.Join(dir, "exports", "nested")

	var stdout, stderr bytes.Buffer
	err := run([]string{"scrape", "--output", "page", "--path", outputDir, "--url", "https://example.com"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run returned error: %v; stderr=%s", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "true" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if _, err := os.Stat(outputDir); err != nil {
		t.Fatalf("output path was not created: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(outputDir, "page.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "body") {
		t.Fatalf("missing markdown body:\n%s", string(content))
	}
}

func TestScrapeCommandFailsBeforeRequestWhenOutputPathCannotBeCreated(t *testing.T) {
	t.Setenv(apiKeyEnv, "test-key")
	requested := false
	setMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		requested = true
		return jsonResponse(200, `{"markdown":"body"}`), nil
	})

	dir := t.TempDir()
	notDir := filepath.Join(dir, "not-dir")
	if err := os.WriteFile(notDir, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := run([]string{"scrape", "--output", "page", "--path", notDir, "--url", "https://example.com"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected output path creation failure")
	}
	if requested {
		t.Fatal("scrape request was made before output path was created")
	}
	if !strings.Contains(stdout.String(), "false") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestScrapeFailureDoesNotOverwriteExistingFile(t *testing.T) {
	t.Setenv(apiKeyEnv, "test-key")
	setMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(500, `{"message":"upstream failed"}`), nil
	})
	old := endpoints["scrape"]
	endpoints["scrape"] = "https://example.test/scrape"
	t.Cleanup(func() { endpoints["scrape"] = old })

	dir := t.TempDir()
	path := filepath.Join(dir, "page.md")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	var stdout, stderr bytes.Buffer
	err = run([]string{"scrape", "--output", "page", "--url", "https://example.com"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected scrape failure")
	}
	if !strings.Contains(stdout.String(), "false") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original" {
		t.Fatalf("file was overwritten: %q", string(content))
	}
}

func TestScrapeNonJSONSuccessReportsResponseContext(t *testing.T) {
	t.Setenv(apiKeyEnv, "test-key")
	setMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		return textResponse(200, "text/plain; charset=utf-8", "\u00e6 not json"), nil
	})
	old := endpoints["scrape"]
	endpoints["scrape"] = "https://example.test/scrape"
	t.Cleanup(func() { endpoints["scrape"] = old })

	var stdout, stderr bytes.Buffer
	err := run([]string{"scrape", "--output", "page", "--url", "https://example.com"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected scrape failure")
	}
	out := stdout.String()
	for _, want := range []string{"false", "scrape response JSON parse failed", "status=200", "text/plain", "\u00e6 not json"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestScrapeNonJSONHTTPErrorReportsStatusAndBody(t *testing.T) {
	t.Setenv(apiKeyEnv, "test-key")
	setMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		return textResponse(502, "text/html", "\u00e6 upstream gateway"), nil
	})
	old := endpoints["scrape"]
	endpoints["scrape"] = "https://example.test/scrape"
	t.Cleanup(func() { endpoints["scrape"] = old })

	var stdout, stderr bytes.Buffer
	err := run([]string{"scrape", "--output", "page", "--url", "https://example.com"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected scrape failure")
	}
	out := stdout.String()
	for _, want := range []string{"false", "scrape HTTP status error: 502", "text/html", "\u00e6 upstream gateway"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestCountryAliases(t *testing.T) {
	cases := map[string]string{
		"U.S.":           "US",
		"United Kingdom": "GB",
		"China":          "CN",
	}
	for input, want := range cases {
		if got := getCountryCodeAlpha2(input); got != want {
			t.Fatalf("getCountryCodeAlpha2(%q) = %q, want %q", input, got, want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func setMockHTTPClient(t *testing.T, fn roundTripFunc) {
	t.Helper()
	old := httpClient
	httpClient = &http.Client{Transport: fn}
	t.Cleanup(func() { httpClient = old })
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func textResponse(status int, contentType string, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func assertRequestTimeout(t *testing.T, r *http.Request, wantSeconds int) {
	t.Helper()
	deadline, ok := r.Context().Deadline()
	if !ok {
		t.Fatal("request context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > time.Duration(wantSeconds)*time.Second {
		t.Fatalf("request timeout = %s, want <= %ds and > 0", remaining, wantSeconds)
	}
}
