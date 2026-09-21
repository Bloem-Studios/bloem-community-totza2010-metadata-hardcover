package hardcover

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bloem-Studios/bloem-community-totza2010-metadata-hardcover/metadata"
)

const testAPIKey = "test-token"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// recordedRequest is one GraphQL request the fake server received.
type recordedRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// fakeServer answers GraphQL requests by matching the operation name in the
// query document, which is how the real endpoint is addressed too.
type fakeServer struct {
	server   *httptest.Server
	requests []recordedRequest
	count    atomic.Int64
}

func newFakeServer(t *testing.T, handler func(t *testing.T, req recordedRequest) (int, string)) (*fakeServer, *Client) {
	t.Helper()

	fake := &fakeServer{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.count.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"invalid_token","error_description":"bad token"}`)
			return
		}

		body, _ := io.ReadAll(r.Body)
		var req recordedRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		fake.requests = append(fake.requests, req)

		status, payload := handler(t, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(fake.server.Close)

	client := New(Options{
		BaseURL:    fake.server.URL,
		APIKey:     testAPIKey,
		HTTPClient: fake.server.Client(),
		// A test must not wait on the real per-minute pacing.
		RequestsPerMinute: 60000,
		Burst:             100,
	})
	return fake, client
}

// bookHandler serves the book fixture for any book query and an empty result
// for anything it does not recognise.
func bookHandler(t *testing.T, req recordedRequest) (int, string) {
	t.Helper()
	switch {
	case strings.Contains(req.Query, "SiloSearchBooks"):
		return http.StatusOK, string(fixture(t, "search.json"))
	case strings.Contains(req.Query, "SiloBookByID"):
		if id, _ := req.Variables["id"].(float64); id == 240029 {
			return http.StatusOK, string(fixture(t, "book_by_id.json"))
		}
		return http.StatusOK, `{"data":{"books_by_pk":null}}`
	case strings.Contains(req.Query, "SiloBookBySlug"),
		strings.Contains(req.Query, "SiloBookByISBN"),
		strings.Contains(req.Query, "SiloBookByASIN"),
		strings.Contains(req.Query, "SiloBooksByIDs"):
		// These queries return a list; reuse the same book row.
		var payload struct {
			Data struct {
				Book json.RawMessage `json:"books_by_pk"`
			} `json:"data"`
		}
		if err := json.Unmarshal(fixture(t, "book_by_id.json"), &payload); err != nil {
			t.Fatalf("decode fixture: %v", err)
		}
		return http.StatusOK, `{"data":{"books":[` + string(payload.Data.Book) + `]}}`
	default:
		t.Fatalf("unexpected query: %s", req.Query)
		return http.StatusInternalServerError, ""
	}
}

func TestUnconfiguredClientMakesNoRequests(t *testing.T) {
	fake, _ := newFakeServer(t, bookHandler)
	client := New(Options{BaseURL: fake.server.URL, HTTPClient: fake.server.Client()})

	if client.Configured() {
		t.Fatal("Configured() = true without an API key")
	}
	if _, err := client.Search(context.Background(), metadata.SearchQuery{Title: "kings"}); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("Search() error = %v, want ErrNoAPIKey", err)
	}
	if _, err := client.Fetch(context.Background(), metadata.SearchQuery{}); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("Fetch() error = %v, want ErrNoAPIKey", err)
	}
	if got := fake.count.Load(); got != 0 {
		t.Fatalf("made %d requests without a key, want 0", got)
	}
}

func TestAPIKeyAcceptsBearerPrefix(t *testing.T) {
	fake, _ := newFakeServer(t, bookHandler)

	client := New(Options{
		BaseURL:    fake.server.URL,
		APIKey:     "  Bearer " + testAPIKey + "  ",
		HTTPClient: fake.server.Client(),
	})

	// The fake server rejects any Authorization header it does not expect, so
	// a successful fetch is the assertion.
	book, err := client.FetchByID(context.Background(), "240029", preference{})
	if err != nil {
		t.Fatal(err)
	}
	if book == nil || book.Title != "The Way of Kings" {
		t.Fatalf("FetchByID() = %#v", book)
	}
	if got := fake.count.Load(); got != 1 {
		t.Fatalf("made %d requests, want 1", got)
	}
}

func TestFetchByIDMapsEveryField(t *testing.T) {
	_, client := newFakeServer(t, bookHandler)

	book, err := client.FetchByID(context.Background(), "240029", preference{format: "ebook", language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if book == nil {
		t.Fatal("FetchByID() returned nil")
	}

	if book.ID != "240029" || book.Slug != "the-way-of-kings" {
		t.Errorf("identity = %q / %q", book.ID, book.Slug)
	}
	if book.URL != "https://hardcover.app/books/the-way-of-kings" {
		t.Errorf("URL = %q", book.URL)
	}
	if book.Title != "The Way of Kings" || book.Subtitle != "Book One of the Stormlight Archive" {
		t.Errorf("title = %q / %q", book.Title, book.Subtitle)
	}
	if book.PublishYear != 2010 || book.ReleaseDate != "2010-08-31" {
		t.Errorf("release = %d / %q", book.PublishYear, book.ReleaseDate)
	}
	if book.CoverURL != "https://images.hardcover.app/way-of-kings.jpg" {
		t.Errorf("CoverURL = %q", book.CoverURL)
	}
	if book.PageCount != 1007 {
		t.Errorf("PageCount = %d", book.PageCount)
	}
	if book.Rating != 4.42 || book.RatingsCount != 18422 {
		t.Errorf("rating = %v / %d", book.Rating, book.RatingsCount)
	}

	// The ebook preference selects the Kindle edition, which supplies the
	// publisher, ISBNs, format and language.
	if book.Publisher != "Tor Books" || book.EditionFormat != "Kindle Edition" {
		t.Errorf("edition = %q / %q", book.Publisher, book.EditionFormat)
	}
	if book.ISBN13 != "9780765365279" || book.ISBN10 != "0765365278" {
		t.Errorf("isbn = %q / %q", book.ISBN13, book.ISBN10)
	}
	if book.ISBN() != "9780765365279" {
		t.Errorf("ISBN() = %q", book.ISBN())
	}
	if book.Language != "en" || book.LanguageName != "English" {
		t.Errorf("language = %q / %q", book.Language, book.LanguageName)
	}
	if book.ASIN != "B003P2WO5E" {
		t.Errorf("ASIN = %q", book.ASIN)
	}

	authors := book.Authors()
	if len(authors) != 1 || authors[0] != "Brandon Sanderson" {
		t.Errorf("Authors() = %#v", authors)
	}
	// The ebook edition credits only the author; the book row alone would
	// otherwise be the whole cast.
	if len(book.Contributors) != 1 {
		t.Errorf("Contributors = %#v", book.Contributors)
	}
	if book.Contributors[0].ImageURL != "https://images.hardcover.app/sanderson.jpg" {
		t.Errorf("author image = %q", book.Contributors[0].ImageURL)
	}

	series, ok := book.PrimarySeries()
	if !ok || series.Name != "The Stormlight Archive" || series.Position != "1" {
		t.Errorf("PrimarySeries() = %#v, %v", series, ok)
	}
	if len(book.Series) != 2 || book.Series[1].Position != "5.5" {
		t.Errorf("Series = %#v", book.Series)
	}

	if len(book.Genres) != 2 || book.Genres[0] != "Fantasy" {
		t.Errorf("Genres = %#v", book.Genres)
	}
	if len(book.Moods) != 1 || len(book.Tags) != 1 || len(book.ContentWarnings) != 1 {
		t.Errorf("tag buckets = %#v / %#v / %#v", book.Moods, book.Tags, book.ContentWarnings)
	}
	if len(book.AlternateTitles) != 1 || book.AlternateTitles[0] != "El camino de los reyes" {
		t.Errorf("AlternateTitles = %#v", book.AlternateTitles)
	}
}

func TestEditionPreferenceSelectsFormatAndLanguage(t *testing.T) {
	_, client := newFakeServer(t, bookHandler)

	audio, err := client.FetchByID(context.Background(), "240029", preference{format: "audiobook", language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if audio.Publisher != "Macmillan Audio" || audio.EditionFormat != "Audible Audio" {
		t.Errorf("audiobook edition = %q / %q", audio.Publisher, audio.EditionFormat)
	}
	if audio.AudioSeconds != 161640 {
		t.Errorf("AudioSeconds = %d", audio.AudioSeconds)
	}
	// Narrators are credited on the audio edition, not on the book, so they
	// only appear once that edition is the chosen one.
	narrators := 0
	for _, contributor := range audio.Contributors {
		if contributor.Role == "Narrator" {
			narrators++
		}
	}
	if narrators != 2 {
		t.Errorf("audiobook contributors = %#v, want two narrators", audio.Contributors)
	}
	if len(audio.Authors()) != 1 || audio.Authors()[0] != "Brandon Sanderson" {
		t.Errorf("audiobook authors = %#v", audio.Authors())
	}
	// The audio edition has no ISBN-10, so the fallback fills it from another
	// edition rather than leaving it blank.
	if audio.ISBN13 != "9781441880413" || audio.ISBN10 == "" {
		t.Errorf("audiobook isbn = %q / %q", audio.ISBN13, audio.ISBN10)
	}

	spanish, err := client.FetchByID(context.Background(), "240029", preference{language: "es"})
	if err != nil {
		t.Fatal(err)
	}
	if spanish.Publisher != "Ediciones B" || spanish.Language != "es" {
		t.Errorf("spanish edition = %q / %q", spanish.Publisher, spanish.Language)
	}
	if len(spanish.Contributors) != 2 || spanish.Contributors[1].Role != "Translator" {
		t.Errorf("spanish contributors = %#v", spanish.Contributors)
	}
}

func TestReconcileISBNsCorrectsAMixedPair(t *testing.T) {
	// Hardcover's Penguin edition of Dune pairs a German ISBN-13 with an
	// American ISBN-10.
	isbn13, isbn10, conflict := reconcileISBNs("9783423026185", "0441172717")
	if !conflict {
		t.Fatal("mixed pair was not reported as a conflict")
	}
	if isbn13 != "9780441172719" || isbn10 != "0441172717" {
		t.Fatalf("reconcileISBNs() = %q / %q, want the ISBN-13 rebuilt from the ISBN-10", isbn13, isbn10)
	}

	isbn13, _, conflict = reconcileISBNs("9780441172719", "0441172717")
	if conflict || isbn13 != "9780441172719" {
		t.Fatalf("a matching pair was altered: %q, conflict=%v", isbn13, conflict)
	}
}

func TestFetchMissingBookReturnsNil(t *testing.T) {
	_, client := newFakeServer(t, bookHandler)

	book, err := client.FetchByID(context.Background(), "999999", preference{})
	if err != nil {
		t.Fatalf("FetchByID() error = %v, want nil", err)
	}
	if book != nil {
		t.Fatalf("FetchByID() = %#v, want nil", book)
	}
}

func TestFetchByIDRejectsNonNumericWithoutRequest(t *testing.T) {
	fake, client := newFakeServer(t, bookHandler)

	book, err := client.FetchByID(context.Background(), "not-a-number", preference{})
	if err != nil || book != nil {
		t.Fatalf("FetchByID() = %#v, %v; want nil, nil", book, err)
	}
	if got := fake.count.Load(); got != 0 {
		t.Fatalf("made %d requests, want 0", got)
	}
}

func TestSearchMapsHits(t *testing.T) {
	fake, client := newFakeServer(t, bookHandler)

	books, err := client.Search(context.Background(), metadata.SearchQuery{
		Title:   "the way of kings",
		Authors: []string{"Brandon Sanderson"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 {
		t.Fatalf("Search() returned %d books, want 2", len(books))
	}

	first := books[0]
	if first.ID != "240029" || first.Title != "The Way of Kings" {
		t.Errorf("first hit = %#v", first)
	}
	if first.PublishYear != 2010 || first.CoverURL == "" {
		t.Errorf("first hit fields = %#v", first)
	}
	if first.ISBN13 != "9780765326355" || first.ISBN10 != "0765326353" {
		t.Errorf("first hit isbn = %q / %q", first.ISBN13, first.ISBN10)
	}
	series, ok := first.PrimarySeries()
	if !ok || series.Name != "The Stormlight Archive" || series.Position != "1" {
		t.Errorf("first hit series = %#v", first.Series)
	}
	if len(first.Series) != 2 {
		t.Errorf("Series = %#v, want the featured entry plus The Cosmere", first.Series)
	}
	if authors := first.Authors(); len(authors) != 1 || authors[0] != "Brandon Sanderson" {
		t.Errorf("first hit authors = %#v", authors)
	}

	// Only the title is sent. Appending the author makes a compilation whose
	// title contains that name outrank the book itself.
	if got := fake.requests[0].Variables["query"]; got != "the way of kings" {
		t.Errorf("search query = %q", got)
	}
}

func TestRankBooksPrefersTheRequestedAuthorAndTitle(t *testing.T) {
	// The order here is what Hardcover returns for "the way of kings": a
	// sampler whose title carries the author's name outranks the book.
	hits := []metadata.Book{
		{ID: "543687", Title: "Brandon Sanderson Sampler: The Way of Kings and Mistborn", PublishYear: 2014},
		{
			ID:           "386446",
			Title:        "The Way of Kings",
			PublishYear:  2010,
			Contributors: []metadata.Contributor{{Name: "Brandon Sanderson"}},
		},
	}

	ranked := rankBooks(hits, metadata.SearchQuery{
		Title:   "The Way of Kings",
		Authors: []string{"Brandon Sanderson"},
	})
	if len(ranked) != 2 {
		t.Fatalf("rankBooks() dropped hits: %#v", ranked)
	}
	if ranked[0].ID != "386446" {
		t.Errorf("rankBooks() top hit = %q, want the exact title by the requested author", ranked[0].ID)
	}
}

func TestRankBooksKeepsHardcoverOrderWithoutSignals(t *testing.T) {
	hits := []metadata.Book{{ID: "1", Title: "First"}, {ID: "2", Title: "Second"}}

	ranked := rankBooks(hits, metadata.SearchQuery{Title: "something else"})
	if ranked[0].ID != "1" || ranked[1].ID != "2" {
		t.Fatalf("rankBooks() reordered an unscored list: %#v", ranked)
	}
}

func TestNormalizeTitleIgnoresArticlesAndPunctuation(t *testing.T) {
	if normalizeTitle("The Way of Kings") != normalizeTitle("Way of Kings!") {
		t.Error("leading article or punctuation changed the normalized title")
	}
	if normalizeTitle("A") != "a" {
		t.Error("a one-word title lost its only word to article stripping")
	}
}

func TestCachedTagsAreSplitAndCapped(t *testing.T) {
	buckets := parseCachedTags(json.RawMessage(`{"Genre":[
		{"tag":"Coming of Age; Epic; Action & Adventure","category":"Genre"},
		{"tag":"Fantasy","category":"Genre"}
	]}`))
	genres := buckets["genre"]
	if len(genres) != 4 || genres[0] != "Coming of Age" || genres[2] != "Action & Adventure" {
		t.Fatalf("genre bucket = %#v, want the semicolon list split", genres)
	}

	long := make([]string, 0, 25)
	for i := range 25 {
		long = append(long, string(rune('a'+i)))
	}
	if got := capTags(long); len(got) != maxTagsPerCategory {
		t.Fatalf("capTags() kept %d tags, want %d", len(got), maxTagsPerCategory)
	}
}

func TestSearchPrefersRequestedYear(t *testing.T) {
	_, client := newFakeServer(t, bookHandler)

	books, err := client.Search(context.Background(), metadata.SearchQuery{Title: "kings", Year: 2021})
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 || books[0].ID != "97844" {
		t.Fatalf("Search() order = %#v", books)
	}
}

func TestSearchByProviderIDSkipsTextSearch(t *testing.T) {
	fake, client := newFakeServer(t, bookHandler)

	books, err := client.Search(context.Background(), metadata.SearchQuery{
		Title:       "ignored",
		ProviderIDs: map[string]string{"hardcover": "240029"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 || books[0].ID != "240029" {
		t.Fatalf("Search() = %#v", books)
	}
	if len(fake.requests) != 1 || !strings.Contains(fake.requests[0].Query, "SiloBookByID") {
		t.Fatalf("requests = %#v", fake.requests)
	}
}

func TestSearchDetailedHydratesHits(t *testing.T) {
	fake, client := newFakeServer(t, bookHandler)

	books, err := client.SearchDetailed(context.Background(), metadata.SearchQuery{Title: "kings"})
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 {
		t.Fatalf("SearchDetailed() returned %d books", len(books))
	}
	// The hydrated hit gains the publisher the search index does not carry.
	if books[0].Publisher != "Tor Books" {
		t.Errorf("hydrated publisher = %q", books[0].Publisher)
	}
	// The unmatched hit keeps its search-index values.
	if books[1].ID != "97844" || books[1].Title != "Project Hail Mary" {
		t.Errorf("second hit = %#v", books[1])
	}
	if len(fake.requests) != 2 {
		t.Fatalf("made %d requests, want 2", len(fake.requests))
	}
}

func TestFetchResolvesISBNThroughEditions(t *testing.T) {
	fake, client := newFakeServer(t, bookHandler)

	book, err := client.Fetch(context.Background(), metadata.SearchQuery{
		ProviderIDs: map[string]string{"isbn": "978-0-7653-2635-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if book == nil || book.ID != "240029" {
		t.Fatalf("Fetch() = %#v", book)
	}
	if got := fake.requests[0].Variables["isbn"]; got != "9780765326355" {
		t.Errorf("isbn variable = %q, want the normalized form", got)
	}
}

func TestFetchFallsBackToSearchWithoutIdentifiers(t *testing.T) {
	fake, client := newFakeServer(t, bookHandler)

	book, err := client.Fetch(context.Background(), metadata.SearchQuery{Title: "the way of kings"})
	if err != nil {
		t.Fatal(err)
	}
	if book == nil || book.ID != "240029" {
		t.Fatalf("Fetch() = %#v", book)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("made %d requests, want a search then a fetch", len(fake.requests))
	}
}

func TestGraphQLErrorsSurface(t *testing.T) {
	_, client := newFakeServer(t, func(*testing.T, recordedRequest) (int, string) {
		return http.StatusOK, `{"errors":[{"message":"field \"nope\" not found"}]}`
	})

	_, err := client.FetchByID(context.Background(), "240029", preference{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("FetchByID() error = %v", err)
	}
}

func TestBareErrorBodySurfaces(t *testing.T) {
	_, client := newFakeServer(t, func(*testing.T, recordedRequest) (int, string) {
		return http.StatusForbidden, `{"error":"insufficient_scope","error_description":"token is missing books"}`
	})

	_, err := client.FetchByID(context.Background(), "240029", preference{})
	if err == nil || !strings.Contains(err.Error(), "insufficient_scope: token is missing books") {
		t.Fatalf("FetchByID() error = %v", err)
	}
}

func TestThrottledRequestIsRetried(t *testing.T) {
	var attempts atomic.Int64
	fake, client := newFakeServer(t, func(t *testing.T, req recordedRequest) (int, string) {
		if attempts.Add(1) == 1 {
			return http.StatusTooManyRequests, `{"error":"Too Many Requests"}`
		}
		return bookHandler(t, req)
	})

	// The retry waits on the RateLimit reset, so keep the fixture's delay off
	// the default one-second floor by supplying Retry-After through the body's
	// absence: the client falls back to one second, which the test tolerates.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	book, err := client.FetchByID(ctx, "240029", preference{})
	if err != nil {
		t.Fatal(err)
	}
	if book == nil {
		t.Fatal("FetchByID() returned nil after a retry")
	}
	if got := fake.count.Load(); got != 2 {
		t.Fatalf("made %d requests, want 2", got)
	}
}

func TestParseRateLimitReset(t *testing.T) {
	if got := parseRateLimitReset(`"Free";r=0;t=3, "daily";r=4231;t=51234`); got != 3*time.Second {
		t.Fatalf("parseRateLimitReset() = %v, want 3s", got)
	}
	if got := parseRateLimitReset(""); got != 0 {
		t.Fatalf("parseRateLimitReset(\"\") = %v, want 0", got)
	}
}

func TestParseCachedTagsAcceptsFlatList(t *testing.T) {
	buckets := parseCachedTags(json.RawMessage(`[{"tag":"Fantasy","category":"Genre"},{"tag":"Tense","category":"Mood"}]`))
	if len(buckets["genre"]) != 1 || buckets["genre"][0] != "Fantasy" {
		t.Fatalf("genre bucket = %#v", buckets["genre"])
	}
	if len(buckets["mood"]) != 1 {
		t.Fatalf("mood bucket = %#v", buckets["mood"])
	}
}
