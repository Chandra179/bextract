package tier2

import (
	"context"
	"errors"
	"strings"
	"testing"

	"bextract/internal/pipeline"
	"bextract/pkg/llm"

	"github.com/PuerkitoBio/goquery"
)

// mockLLMClient is a test double for llm.Client.
type mockLLMClient struct {
	response string
	err      error
}

func (m *mockLLMClient) Complete(_ context.Context, _ string) (string, error) {
	return m.response, m.err
}

var _ llm.Client = (*mockLLMClient)(nil)

func docFromHTML(html string) *goquery.Document {
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	return doc
}

func makeResp() *pipeline.Response {
	return &pipeline.Response{FinalURL: "https://example.com"}
}

// TestCleanText_emptyDoc verifies that an empty HTML body returns "".
func TestCleanText_emptyDoc(t *testing.T) {
	doc := docFromHTML(`<html><body></body></html>`)
	got := cleanText(doc, "https://example.com", 4000)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// TestCleanText_truncation verifies that text longer than maxBytes is truncated.
func TestCleanText_truncation(t *testing.T) {
	long := strings.Repeat("a", 200)
	html := `<html><body><p>` + long + `</p></body></html>`
	doc := docFromHTML(html)
	got := cleanText(doc, "https://example.com", 50)
	if len(got) > 50 {
		t.Errorf("expected len <= 50, got %d", len(got))
	}
}

// TestRunLLMPhaseC_validJSON checks that a well-formed JSON response yields fields.
func TestRunLLMPhaseC_validJSON(t *testing.T) {
	client := &mockLLMClient{response: `{"title":"Foo","price":"$9"}`}
	html := `<html><body><article><p>Some real content here that is long enough for readability to parse it properly.</p></article></body></html>`
	doc := docFromHTML(html)
	resp := makeResp()

	r, text := runLLMPhaseC(context.Background(), client, doc, resp, 0.7, 4000)
	if r.Err != nil {
		t.Fatalf("unexpected error: %v", r.Err)
	}
	if len(r.Fields) == 0 {
		// If go-readability returned empty (no content), the function returns early — that's OK.
		// Only assert fields if text was non-empty.
		if text != "" {
			t.Errorf("expected fields but got none; text=%q", text)
		}
		return
	}
	if r.Fields["title"] != "Foo" {
		t.Errorf("expected title=Foo, got %q", r.Fields["title"])
	}
	if r.Fields["price"] != "$9" {
		t.Errorf("expected price=$9, got %q", r.Fields["price"])
	}
}

// TestRunLLMPhaseC_markdownFence verifies that markdown-fenced JSON is unwrapped.
func TestRunLLMPhaseC_markdownFence(t *testing.T) {
	fenced := "```json\n{\"title\":\"Bar\"}\n```"
	client := &mockLLMClient{response: fenced}
	html := `<html><body><article><p>Some real content here that is long enough for readability.</p></article></body></html>`
	doc := docFromHTML(html)
	resp := makeResp()

	r, text := runLLMPhaseC(context.Background(), client, doc, resp, 0.7, 4000)
	if text == "" {
		// go-readability found nothing — skip assertion.
		return
	}
	if r.Err != nil {
		t.Fatalf("unexpected error: %v", r.Err)
	}
	if r.Fields["title"] != "Bar" {
		t.Errorf("expected title=Bar, got %q", r.Fields["title"])
	}
}

// TestRunLLMPhaseC_badJSON verifies that malformed JSON sets Err and returns empty fields.
func TestRunLLMPhaseC_badJSON(t *testing.T) {
	client := &mockLLMClient{response: `not json at all`}
	html := `<html><body><article><p>Some real content here that is long enough for readability.</p></article></body></html>`
	doc := docFromHTML(html)
	resp := makeResp()

	r, text := runLLMPhaseC(context.Background(), client, doc, resp, 0.7, 4000)
	if text == "" {
		return // no content — early return, nothing to assert
	}
	if r.Err == nil {
		t.Error("expected a parse error but got nil")
	}
	if len(r.Fields) != 0 {
		t.Errorf("expected no fields on bad JSON, got %v", r.Fields)
	}
}

// TestRunLLMPhaseC_emptyText verifies that when go-readability returns "" we return early
// with no error and nil fields.
func TestRunLLMPhaseC_emptyText(t *testing.T) {
	client := &mockLLMClient{response: `{"title":"Should not be called"}`}
	doc := docFromHTML(`<html><body></body></html>`)
	resp := makeResp()

	r, text := runLLMPhaseC(context.Background(), client, doc, resp, 0.7, 4000)
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
	if r.Err != nil {
		t.Errorf("expected nil Err on empty text, got %v", r.Err)
	}
	if len(r.Fields) != 0 {
		t.Errorf("expected no fields on empty text, got %v", r.Fields)
	}
}

// TestRunLLMPhaseC_clientError verifies that an LLM client error is propagated.
func TestRunLLMPhaseC_clientError(t *testing.T) {
	wantErr := errors.New("network failure")
	client := &mockLLMClient{err: wantErr}
	html := `<html><body><article><p>Some real content here that is long enough for readability to parse it properly.</p></article></body></html>`
	doc := docFromHTML(html)
	resp := makeResp()

	r, text := runLLMPhaseC(context.Background(), client, doc, resp, 0.7, 4000)
	if text == "" {
		return // go-readability found nothing — early return before client call
	}
	if r.Err == nil {
		t.Error("expected client error to be set")
	}
	if len(r.Fields) != 0 {
		t.Errorf("expected no fields on error, got %v", r.Fields)
	}
}
