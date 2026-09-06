package metadata

import (
	"reflect"
	"testing"
)

func TestNormalizeISBN(t *testing.T) {
	cases := map[string]string{
		"978-0-7653-2635-5": "9780765326355",
		"9780765326355":     "9780765326355",
		" 0765326353 ":      "0765326353",
		"080442957X":        "080442957X",
		"9780765326356":     "", // bad check digit
		"12345":             "",
		"not-an-isbn":       "",
		"":                  "",
	}
	for input, want := range cases {
		if got := NormalizeISBN(input); got != want {
			t.Errorf("NormalizeISBN(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestISBN13FromISBN10(t *testing.T) {
	cases := map[string]string{
		"0441172717":    "9780441172719",
		"0765326353":    "9780765326355",
		"080442957X":    "9780804429573",
		"9780441172719": "", // already an ISBN-13
		"bad":           "",
		"":              "",
	}
	for input, want := range cases {
		if got := ISBN13FromISBN10(input); got != want {
			t.Errorf("ISBN13FromISBN10(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestISBNsAgree(t *testing.T) {
	if !ISBNsAgree("9780441172719", "0441172717") {
		t.Error("matching pair reported as a conflict")
	}
	// The pair Hardcover holds on one Dune edition: a German ISBN-13 next to
	// an American ISBN-10.
	if ISBNsAgree("9783423026185", "0441172717") {
		t.Error("mismatched pair reported as agreeing")
	}
	if !ISBNsAgree("9780441172719", "") || !ISBNsAgree("", "0441172717") {
		t.Error("a half-filled pair should not count as a conflict")
	}
}

func TestProviderIDsFromBook(t *testing.T) {
	ids := ProviderIDsFromBook(Book{
		ID:        "240029",
		Slug:      "the-way-of-kings",
		ISBN13:    "978-0-7653-2635-5",
		ISBN10:    "0765326353",
		ASIN:      "B003P2WO5E",
		EditionID: "771002",
	})

	want := map[string]string{
		"hardcover":         "240029",
		"hardcover_slug":    "the-way-of-kings",
		"hardcover_edition": "771002",
		"isbn":              "9780765326355",
		"isbn13":            "9780765326355",
		"isbn10":            "0765326353",
		"asin":              "B003P2WO5E",
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ProviderIDsFromBook() = %#v, want %#v", ids, want)
	}
}

func TestLookupFromProviderIDs(t *testing.T) {
	t.Run("numeric id", func(t *testing.T) {
		got := LookupFromProviderIDs(map[string]string{"hardcover": "240029"})
		if got.BookID != "240029" {
			t.Fatalf("BookID = %q", got.BookID)
		}
	})

	t.Run("composite id from an older record", func(t *testing.T) {
		got := LookupFromProviderIDs(map[string]string{"hardcover": "hardcover:240029"})
		if got.BookID != "240029" {
			t.Fatalf("BookID = %q", got.BookID)
		}
	})

	t.Run("slug", func(t *testing.T) {
		got := LookupFromProviderIDs(map[string]string{"hardcover_slug": "the-way-of-kings"})
		if got.BookID != "" || got.Slug != "the-way-of-kings" {
			t.Fatalf("Lookup = %#v", got)
		}
	})

	t.Run("isbn is normalized", func(t *testing.T) {
		got := LookupFromProviderIDs(map[string]string{"isbn": "978-0-7653-2635-5"})
		if got.ISBN != "9780765326355" {
			t.Fatalf("ISBN = %q", got.ISBN)
		}
	})

	t.Run("empty", func(t *testing.T) {
		if got := LookupFromProviderIDs(map[string]string{"tmdb": "1234"}); !got.Empty() {
			t.Fatalf("Lookup = %#v, want empty", got)
		}
	})
}

func TestBookHelpers(t *testing.T) {
	book := Book{
		ISBN10: "0765326353",
		Contributors: []Contributor{
			{Name: "Brandon Sanderson"},
			{Name: "Michael Kramer", Role: "Narrator"},
		},
		Series: []SeriesEntry{
			{Name: "The Cosmere", Position: "5.5"},
			{Name: "The Stormlight Archive", Position: "1", Featured: true},
		},
	}

	if got := book.ISBN(); got != "0765326353" {
		t.Errorf("ISBN() = %q", got)
	}
	if got := book.Authors(); len(got) != 1 || got[0] != "Brandon Sanderson" {
		t.Errorf("Authors() = %#v", got)
	}
	series, ok := book.PrimarySeries()
	if !ok || series.Name != "The Stormlight Archive" {
		t.Errorf("PrimarySeries() = %#v, %v; want the featured entry", series, ok)
	}
	if _, ok := (Book{}).PrimarySeries(); ok {
		t.Error("PrimarySeries() on a book with no series returned ok")
	}
}

func TestBookURL(t *testing.T) {
	if got := BookURL("the-way-of-kings"); got != "https://hardcover.app/books/the-way-of-kings" {
		t.Errorf("BookURL() = %q", got)
	}
	if got := BookURL("  "); got != "" {
		t.Errorf("BookURL(blank) = %q", got)
	}
}
