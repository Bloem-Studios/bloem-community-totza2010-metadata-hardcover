package metadata

import "strings"

// CapabilityID is the metadata_provider.v1 capability ID declared in the
// manifest. Silo stores the plugin's identifier under this key.
const CapabilityID = "hardcover"

// SourceID is the provider name used in the composite capability identifier.
const SourceID = "hardcover"

// BookURL returns the public Hardcover page for a slug.
func BookURL(slug string) string {
	if slug = strings.TrimSpace(slug); slug == "" {
		return ""
	}
	return "https://hardcover.app/books/" + slug
}

// ProviderIDsFromBook maps a book onto the identifier set Silo persists.
func ProviderIDsFromBook(book Book) map[string]string {
	ids := make(map[string]string)

	if id := strings.TrimSpace(book.ID); id != "" {
		ids[SourceID] = id
		ids[CapabilityID] = id
	}
	if slug := strings.TrimSpace(book.Slug); slug != "" {
		ids["hardcover_slug"] = slug
	}
	if isbn := NormalizeISBN(book.ISBN13); isbn != "" {
		ids["isbn"] = isbn
		ids["isbn13"] = isbn
	}
	if isbn := NormalizeISBN(book.ISBN10); isbn != "" {
		ids["isbn10"] = isbn
		if ids["isbn"] == "" {
			ids["isbn"] = isbn
		}
	}
	if asin := strings.TrimSpace(book.ASIN); asin != "" {
		ids["asin"] = asin
	}
	if edition := strings.TrimSpace(book.EditionID); edition != "" {
		ids["hardcover_edition"] = edition
	}

	return ids
}

// Lookup describes how a GetMetadata request should be resolved.
type Lookup struct {
	BookID string
	Slug   string
	ISBN   string
	ASIN   string
}

// Empty reports whether the lookup carries no usable identifier.
func (l Lookup) Empty() bool {
	return l.BookID == "" && l.Slug == "" && l.ISBN == "" && l.ASIN == ""
}

// LookupFromProviderIDs picks the identifiers the plugin can resolve out of
// the map Silo sends. Values are checked in the order the plugin can use them:
// a Hardcover book ID is exact, a slug is exact, an ISBN or ASIN needs a
// lookup through editions.
func LookupFromProviderIDs(ids map[string]string) Lookup {
	var lookup Lookup

	for _, key := range []string{CapabilityID, SourceID, "hardcover_id", "hardcover_book"} {
		value := strings.TrimSpace(ids[key])
		// Older records may carry a "hardcover:1234" style composite value.
		if _, rest, ok := strings.Cut(value, ":"); ok {
			value = strings.TrimSpace(rest)
		}
		if isDigits(value) {
			lookup.BookID = value
			break
		}
		if lookup.Slug == "" && value != "" {
			lookup.Slug = value
		}
	}

	if slug := strings.TrimSpace(ids["hardcover_slug"]); slug != "" {
		lookup.Slug = slug
	}
	for _, key := range []string{"isbn", "isbn13", "isbn_13", "isbn10", "isbn_10"} {
		if isbn := NormalizeISBN(ids[key]); isbn != "" {
			lookup.ISBN = isbn
			break
		}
	}
	lookup.ASIN = strings.TrimSpace(ids["asin"])

	return lookup
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
