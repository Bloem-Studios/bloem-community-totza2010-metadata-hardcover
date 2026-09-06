package hardcover

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/totza2010/silo-plugin-metadata-hardcover/metadata"
)

// liveClient builds a client against the real Hardcover API. Every test in
// this file is skipped unless HARDCOVER_API_TOKEN is set, so the default suite
// stays offline and deterministic.
func liveClient(t *testing.T) *Client {
	t.Helper()

	token := os.Getenv("HARDCOVER_API_TOKEN")
	if token == "" {
		t.Skip("set HARDCOVER_API_TOKEN to run the live Hardcover tests")
	}
	return New(Options{
		APIKey:    token,
		UserAgent: "silo-plugins-metadata-hardcover/live-test",
	})
}

func liveContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestLiveSearch checks that the search document is accepted and that hits map
// onto books with the fields the picker shows.
func TestLiveSearch(t *testing.T) {
	client := liveClient(t)

	books, err := client.Search(liveContext(t), metadata.SearchQuery{
		Title:   "The Way of Kings",
		Authors: []string{"Brandon Sanderson"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(books) == 0 {
		t.Fatal("Search() returned no books")
	}

	first := books[0]
	t.Logf("top hit: id=%s title=%q year=%d cover=%q", first.ID, first.Title, first.PublishYear, first.CoverURL)
	if first.ID == "" || first.Title == "" {
		t.Errorf("top hit is missing identity: %#v", first)
	}
	if len(first.Authors()) == 0 {
		t.Error("top hit has no authors")
	}
}

// TestLiveFetchByID checks the full book document against the live schema.
// Every field the plugin advertises is logged so a schema change shows up as a
// blank value rather than a silent omission.
func TestLiveFetchByID(t *testing.T) {
	client := liveClient(t)

	books, err := client.Search(liveContext(t), metadata.SearchQuery{Title: "The Way of Kings"})
	if err != nil {
		t.Fatal(err)
	}
	if len(books) == 0 {
		t.Fatal("Search() returned no books to fetch")
	}

	book, err := client.FetchByID(liveContext(t), books[0].ID, preference{format: "ebook", language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if book == nil {
		t.Fatalf("FetchByID(%q) returned nil", books[0].ID)
	}

	t.Logf("id=%s slug=%s url=%s", book.ID, book.Slug, book.URL)
	t.Logf("title=%q subtitle=%q", book.Title, book.Subtitle)
	t.Logf("release=%q year=%d pages=%d audio_seconds=%d", book.ReleaseDate, book.PublishYear, book.PageCount, book.AudioSeconds)
	t.Logf("publisher=%q language=%q/%q format=%q", book.Publisher, book.Language, book.LanguageName, book.EditionFormat)
	t.Logf("isbn13=%q isbn10=%q asin=%q edition=%s", book.ISBN13, book.ISBN10, book.ASIN, book.EditionID)
	t.Logf("cover=%q rating=%v/%d", book.CoverURL, book.Rating, book.RatingsCount)
	t.Logf("genres=%v moods=%v tags=%v warnings=%v", book.Genres, book.Moods, book.Tags, book.ContentWarnings)
	t.Logf("series=%+v", book.Series)
	t.Logf("contributors=%+v", book.Contributors)
	t.Logf("alternate_titles=%v", book.AlternateTitles)

	if book.Title == "" {
		t.Error("Title is empty")
	}
	if len(book.Authors()) == 0 {
		t.Error("Authors() is empty")
	}
	if book.CoverURL == "" {
		t.Error("CoverURL is empty")
	}
	if book.PublishYear == 0 {
		t.Error("PublishYear is zero")
	}
	if _, ok := book.PrimarySeries(); !ok {
		t.Error("PrimarySeries() is empty for a book that belongs to a series")
	}
	if len(book.Genres) == 0 {
		t.Error("Genres is empty")
	}
	if book.Publisher == "" {
		t.Error("Publisher is empty")
	}
	if book.ISBN() == "" {
		t.Error("ISBN() is empty")
	}
}

// TestLiveAudiobookCreditsNarrators checks that edition-level credits are read.
// Hardcover records the narrator on the audio edition rather than on the book,
// so this fails if the edition selection stops carrying contributions.
func TestLiveAudiobookCreditsNarrators(t *testing.T) {
	client := liveClient(t)

	books, err := client.Search(liveContext(t), metadata.SearchQuery{Title: "The Way of Kings"})
	if err != nil {
		t.Fatal(err)
	}
	if len(books) == 0 {
		t.Fatal("Search() returned no books")
	}

	book, err := client.FetchByID(liveContext(t), books[0].ID, preference{format: "audiobook", language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if book == nil {
		t.Fatal("FetchByID() returned nil")
	}

	t.Logf("publisher=%q audio_seconds=%d contributors=%+v", book.Publisher, book.AudioSeconds, book.Contributors)
	if book.AudioSeconds == 0 {
		t.Error("the audio edition was not selected: AudioSeconds is zero")
	}
	narrators := 0
	for _, contributor := range book.Contributors {
		if contributor.Role == "Narrator" {
			narrators++
		}
	}
	if narrators == 0 {
		t.Error("no narrator credits, so edition contributions are not being read")
	}
	if len(book.Authors()) == 0 {
		t.Error("the author credit was lost when edition credits were merged")
	}
}

// TestLiveFetchByISBN checks the editions filter used to match an existing
// library item.
func TestLiveFetchByISBN(t *testing.T) {
	client := liveClient(t)

	// Project Hail Mary, Ballantine hardcover.
	book, err := client.FetchByISBN(liveContext(t), "978-0-593-13520-4", preference{})
	if err != nil {
		t.Fatal(err)
	}
	if book == nil {
		t.Fatal("FetchByISBN() returned nil")
	}
	t.Logf("id=%s title=%q authors=%v publisher=%q isbn=%q conflict=%v",
		book.ID, book.Title, book.Authors(), book.Publisher, book.ISBN(), book.ISBNConflict)
	if book.Title == "" {
		t.Error("Title is empty")
	}
	// The ISBN-13 and ISBN-10 on a record must describe the same edition.
	if !metadata.ISBNsAgree(book.ISBN13, book.ISBN10) {
		t.Errorf("ISBNs disagree after reconciliation: %q / %q", book.ISBN13, book.ISBN10)
	}
}

// TestLiveFetchBySlug checks the slug lookup path.
func TestLiveFetchBySlug(t *testing.T) {
	client := liveClient(t)

	book, err := client.Lookup(liveContext(t), metadata.Lookup{Slug: "project-hail-mary"}, preference{})
	if err != nil {
		t.Fatal(err)
	}
	if book == nil {
		t.Fatal("Lookup(slug) returned nil")
	}
	t.Logf("id=%s title=%q", book.ID, book.Title)
}

// TestLiveSearchDetailed checks the hydration query, which selects the same
// book fields through a list rather than a primary key.
func TestLiveSearchDetailed(t *testing.T) {
	client := liveClient(t)

	books, err := client.SearchDetailed(liveContext(t), metadata.SearchQuery{
		Title:    "Project Hail Mary",
		ItemType: "book",
		Language: "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(books) == 0 {
		t.Fatal("SearchDetailed() returned no books")
	}
	t.Logf("top hit: id=%s title=%q publisher=%q language=%q series=%+v",
		books[0].ID, books[0].Title, books[0].Publisher, books[0].Language, books[0].Series)
	if books[0].Publisher == "" {
		t.Error("hydrated hit has no publisher, so the detail query did not apply")
	}
}
