package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/totza2010/silo-plugin-metadata-hardcover/metadata"
)

// foldedSearchHandler models what Hardcover returns for the two queries the
// plugin issues when Silo folds an author into the title. The folded query
// ranks a study guide first and the novel last; the title alone ranks the
// novel first.
func foldedSearchHandler(t *testing.T, req recordedRequest) (int, string) {
	t.Helper()
	if !strings.Contains(req.Query, "SiloSearchBooks") {
		return bookHandler(t, req)
	}

	query, _ := req.Variables["query"].(string)
	hit := func(id, title, author string, year int) string {
		doc := map[string]any{
			"id": id, "title": title, "release_year": year,
			"author_names": []string{author},
		}
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return `{"document":` + string(encoded) + `}`
	}

	var hits []string
	switch query {
	case "Dune Frank Herbert":
		hits = []string{
			hit("2050944", "Summary & Study Guide Dune by Frank Herbert", "BookRags", 2011),
			hit("490427", "Frank Herbert's Dune Saga Collection", "Frank Herbert", 2012),
			hit("312460", "Dune", "Frank Herbert", 1965),
		}
	case "dune", "Dune":
		hits = []string{
			hit("312460", "Dune", "Frank Herbert", 1965),
			hit("427393", "Dune Messiah", "Frank Herbert", 1969),
		}
	default:
		t.Fatalf("unexpected search query %q", query)
	}
	return http.StatusOK, `{"data":{"search":{"ids":[],"results":{"hits":[` + strings.Join(hits, ",") + `]}}}}`
}

func TestSearchRecoversTheTitleFromAFoldedAuthor(t *testing.T) {
	fake, client := newFakeServer(t, foldedSearchHandler)

	books, err := client.Search(context.Background(), metadata.SearchQuery{Title: "Dune Frank Herbert"})
	if err != nil {
		t.Fatal(err)
	}
	if len(books) == 0 {
		t.Fatal("Search() returned no books")
	}
	if books[0].ID != "312460" {
		t.Errorf("top hit = %s %q, want the novel", books[0].ID, books[0].Title)
	}

	// Two searches: the folded query, then the recovered title.
	if len(fake.requests) != 2 {
		t.Fatalf("made %d searches, want 2", len(fake.requests))
	}
	if got := fake.requests[1].Variables["query"]; got != "dune" {
		t.Errorf("second search query = %q, want the title alone", got)
	}

	// Nothing is dropped: the study guide and the collection are still offered.
	if len(books) != 4 {
		t.Errorf("Search() returned %d books, want every hit from both searches", len(books))
	}
}

func TestSearchWithoutAnAuthorSuffixMakesOneRequest(t *testing.T) {
	fake, client := newFakeServer(t, foldedSearchHandler)

	if _, err := client.Search(context.Background(), metadata.SearchQuery{Title: "Dune"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("made %d searches, want 1", len(fake.requests))
	}
}

func TestSplitAuthorSuffix(t *testing.T) {
	hits := []metadata.Book{{
		ID:           "1",
		Title:        "Dune",
		Contributors: []metadata.Contributor{{Name: "Frank Herbert"}},
	}}

	t.Run("author at the end is recovered", func(t *testing.T) {
		title, author, ok := splitAuthorSuffix("Dune Frank Herbert", hits)
		if !ok || title != "dune" || author != "frank herbert" {
			t.Fatalf("splitAuthorSuffix() = %q, %q, %v", title, author, ok)
		}
	})

	t.Run("no author in the query", func(t *testing.T) {
		if _, _, ok := splitAuthorSuffix("Dune", hits); ok {
			t.Fatal("splitAuthorSuffix() found an author that is not there")
		}
	})

	t.Run("query that is only the author keeps its title", func(t *testing.T) {
		if _, _, ok := splitAuthorSuffix("Frank Herbert", hits); ok {
			t.Fatal("splitAuthorSuffix() stripped the whole query")
		}
	})

	t.Run("longest credited name wins", func(t *testing.T) {
		long := []metadata.Book{
			{ID: "1", Contributors: []metadata.Contributor{{Name: "Guin"}}},
			{ID: "2", Contributors: []metadata.Contributor{{Name: "Ursula K. Le Guin"}}},
		}
		title, author, ok := splitAuthorSuffix("The Dispossessed Ursula K. Le Guin", long)
		if !ok || title != "dispossessed" || author != "ursula k le guin" {
			t.Fatalf("splitAuthorSuffix() = %q, %q, %v", title, author, ok)
		}
	})
}

func TestDerivativeTitlesRankBelowTheBook(t *testing.T) {
	hits := []metadata.Book{
		{ID: "guide", Title: "Summary & Study Guide Dune by Frank Herbert"},
		{ID: "novel", Title: "Dune"},
	}
	ranked := rankBooks(hits, metadata.SearchQuery{Title: "Dune"})
	if ranked[0].ID != "novel" {
		t.Fatalf("top hit = %q, want the novel", ranked[0].ID)
	}
}

func TestDerivativeQueryStillFindsItsBook(t *testing.T) {
	hits := []metadata.Book{
		{ID: "novel", Title: "Dune"},
		{ID: "trilogy", Title: "The Great Dune Trilogy"},
	}
	ranked := rankBooks(hits, metadata.SearchQuery{Title: "The Great Dune Trilogy"})
	if ranked[0].ID != "trilogy" {
		t.Fatalf("top hit = %q, want the omnibus the query named", ranked[0].ID)
	}
}

func TestMergeBooksKeepsTheFirstRanking(t *testing.T) {
	merged := mergeBooks(
		[]metadata.Book{{ID: "a"}, {ID: "b"}},
		[]metadata.Book{{ID: "b"}, {ID: "c"}},
	)
	got := []string{merged[0].ID, merged[1].ID, merged[2].ID}
	if len(merged) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("mergeBooks() = %v", got)
	}
}
