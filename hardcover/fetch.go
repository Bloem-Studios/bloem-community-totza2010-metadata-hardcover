package hardcover

import (
	"context"
	"strconv"
	"strings"

	"github.com/totza2010/silo-plugin-metadata-hardcover/metadata"
)

// Fetch resolves a GetMetadata request to a single book. It returns a nil book
// and no error when Hardcover has nothing under the given identifiers.
func (c *Client) Fetch(ctx context.Context, query metadata.SearchQuery) (*metadata.Book, error) {
	if !c.Configured() {
		return nil, ErrNoAPIKey
	}

	lookup := metadata.LookupFromProviderIDs(query.ProviderIDs)
	pref := preferenceFor(query)
	if !lookup.Empty() {
		return c.Lookup(ctx, lookup, pref)
	}

	// Nothing identified the book, so fall back to the best search hit for
	// the title Silo passed.
	books, err := c.Search(ctx, query)
	if err != nil || len(books) == 0 {
		return nil, err
	}
	return c.FetchByID(ctx, books[0].ID, pref)
}

// Lookup resolves the first identifier that answers, in order of precision: a
// Hardcover book ID, a slug, an ISBN, then an ASIN.
func (c *Client) Lookup(ctx context.Context, lookup metadata.Lookup, pref preference) (*metadata.Book, error) {
	if lookup.BookID != "" {
		book, err := c.FetchByID(ctx, lookup.BookID, pref)
		if book != nil || err != nil {
			return book, err
		}
	}
	if lookup.Slug != "" {
		book, err := c.fetchOne(ctx, bookBySlugQuery, map[string]any{"slug": lookup.Slug}, pref)
		if book != nil || err != nil {
			return book, err
		}
	}
	if lookup.ISBN != "" {
		book, err := c.FetchByISBN(ctx, lookup.ISBN, pref)
		if book != nil || err != nil {
			return book, err
		}
	}
	if lookup.ASIN != "" {
		return c.fetchOne(ctx, bookByASINQuery, map[string]any{"asin": lookup.ASIN}, pref)
	}
	return nil, nil
}

// FetchByID resolves a numeric Hardcover book ID.
func (c *Client) FetchByID(ctx context.Context, id string, pref preference) (*metadata.Book, error) {
	bookID, err := strconv.Atoi(strings.TrimSpace(id))
	if err != nil || bookID <= 0 {
		return nil, nil
	}

	var response struct {
		Book *book `json:"books_by_pk"`
	}
	if err := c.execute(ctx, bookByIDQuery, map[string]any{"id": bookID}, &response); err != nil {
		return nil, err
	}
	if response.Book == nil {
		return nil, nil
	}
	mapped := response.Book.toBook(pref)
	return &mapped, nil
}

// FetchByISBN resolves either ISBN form through the book's editions. The ISBN
// is normalized first, so a hyphenated value from a file still matches.
func (c *Client) FetchByISBN(ctx context.Context, isbn string, pref preference) (*metadata.Book, error) {
	normalized := metadata.NormalizeISBN(isbn)
	if normalized == "" {
		return nil, nil
	}
	return c.fetchOne(ctx, bookByISBNQuery, map[string]any{"isbn": normalized}, pref)
}

// fetchOne runs a query whose data holds a books list and returns its first
// entry.
func (c *Client) fetchOne(ctx context.Context, query string, variables map[string]any, pref preference) (*metadata.Book, error) {
	var response struct {
		Books []book `json:"books"`
	}
	if err := c.execute(ctx, query, variables, &response); err != nil {
		return nil, err
	}
	if len(response.Books) == 0 {
		return nil, nil
	}
	mapped := response.Books[0].toBook(pref)
	return &mapped, nil
}
