package hardcover

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/totza2010/silo-plugin-metadata-hardcover/metadata"
)

// Search resolves a query to Hardcover books. Identifiers in the query are
// tried first, since an ISBN or Hardcover ID is an exact answer; otherwise the
// title and authors go to Hardcover's search endpoint.
func (c *Client) Search(ctx context.Context, query metadata.SearchQuery) ([]metadata.Book, error) {
	if !c.Configured() {
		return nil, ErrNoAPIKey
	}

	if lookup := metadata.LookupFromProviderIDs(query.ProviderIDs); !lookup.Empty() {
		book, err := c.Lookup(ctx, lookup, preferenceFor(query))
		if err != nil {
			return nil, err
		}
		if book != nil {
			return []metadata.Book{*book}, nil
		}
	}

	text := searchText(query)
	if text == "" {
		return nil, nil
	}

	hits, err := c.searchOnce(ctx, text)
	if err != nil {
		return nil, err
	}

	// Silo has no author field in the plugin search contract, so it appends
	// the author to the title and sends one string. Hardcover's index weights
	// the title far above the author name, which pushes summaries, samplers
	// and box sets whose *titles* carry the author's name above the book
	// itself: "Dune Frank Herbert" returns the novel twentieth. Recover the
	// title by finding an author from these hits at the end of the query, then
	// search again for the title alone.
	effective := query
	if title, author, ok := splitAuthorSuffix(text, hits); ok {
		effective.Title = title
		effective.Authors = append(append([]string(nil), query.Authors...), author)

		byTitle, err := c.searchOnce(ctx, title)
		if err != nil {
			return nil, err
		}
		hits = mergeBooks(byTitle, hits)
	}

	return rankBooks(hits, effective), nil
}

// searchOnce runs one Typesense search and maps its hits.
func (c *Client) searchOnce(ctx context.Context, text string) ([]metadata.Book, error) {
	var response struct {
		Search *struct {
			IDs     []int           `json:"ids"`
			Results json.RawMessage `json:"results"`
		} `json:"search"`
	}
	if err := c.execute(ctx, searchQuery, map[string]any{
		"query":   text,
		"perPage": c.searchLimit,
	}, &response); err != nil {
		return nil, err
	}
	if response.Search == nil {
		return nil, nil
	}
	return booksFromSearchResults(response.Search.Results, response.Search.IDs), nil
}

// mergeBooks concatenates two hit lists, keeping the first occurrence of each
// book so the leading list's ranking is preserved.
func mergeBooks(first, second []metadata.Book) []metadata.Book {
	merged := make([]metadata.Book, 0, len(first)+len(second))
	seen := make(map[string]struct{}, len(first)+len(second))
	for _, list := range [][]metadata.Book{first, second} {
		for _, book := range list {
			if _, ok := seen[book.ID]; ok {
				continue
			}
			seen[book.ID] = struct{}{}
			merged = append(merged, book)
		}
	}
	return merged
}

// splitAuthorSuffix reports the title and author hidden in a "Title Author"
// query. An author is only believed when a book in the hits is credited to
// exactly that name and the name sits at the end of the query, so a title that
// merely ends in a person's name is not truncated without evidence. The
// longest such name wins, so "Ursula K. Le Guin" beats "Guin".
func splitAuthorSuffix(text string, hits []metadata.Book) (string, string, bool) {
	normalizedQuery := normalizeTitle(text)
	if normalizedQuery == "" {
		return "", "", false
	}

	bestTitle, bestAuthor := "", ""
	for _, hit := range hits {
		for _, contributor := range hit.Contributors {
			name := normalizeTitle(contributor.Name)
			if name == "" || len(name) <= len(bestAuthor) {
				continue
			}
			prefix, ok := strings.CutSuffix(normalizedQuery, " "+name)
			// A query that is only the author's name leaves no title, and a
			// suffix that is the whole query means the two are the same.
			if !ok || strings.TrimSpace(prefix) == "" {
				continue
			}
			bestTitle, bestAuthor = strings.TrimSpace(prefix), name
		}
	}
	if bestAuthor == "" {
		return "", "", false
	}
	return bestTitle, bestAuthor, true
}

// SearchDetailed runs Search and then refetches each hit through the book
// query, so results carry publisher, language and series data the search index
// does not return. It costs one extra request per search.
func (c *Client) SearchDetailed(ctx context.Context, query metadata.SearchQuery) ([]metadata.Book, error) {
	books, err := c.Search(ctx, query)
	if err != nil || len(books) == 0 {
		return books, err
	}

	ids := make([]int, 0, len(books))
	for _, book := range books {
		if id, err := strconv.Atoi(book.ID); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return books, nil
	}

	var response struct {
		Books []book `json:"books"`
	}
	if err := c.execute(ctx, booksByIDsQuery, map[string]any{"ids": ids}, &response); err != nil {
		return nil, err
	}

	pref := preferenceFor(query)
	detailed := make(map[string]metadata.Book, len(response.Books))
	for _, entry := range response.Books {
		mapped := entry.toBook(pref)
		detailed[mapped.ID] = mapped
	}

	// Keep the search ranking; only the contents of each hit change.
	for i, book := range books {
		if full, ok := detailed[book.ID]; ok {
			books[i] = full
		}
	}
	return books, nil
}

// searchText is the title alone. Hardcover's index weights the title far above
// the author name, so appending an author makes a compilation whose title
// contains that name outrank the book itself. Authors are applied afterwards,
// by rankBooks.
func searchText(query metadata.SearchQuery) string {
	return strings.TrimSpace(query.Title)
}

func preferenceFor(query metadata.SearchQuery) preference {
	return preference{
		format:   normalizeItemType(query.ItemType),
		language: query.Language,
	}
}

// normalizeItemType maps Silo's item types onto the edition format the plugin
// prefers when a book has several editions.
func normalizeItemType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "audio"):
		return "audiobook"
	case strings.Contains(value, "book"):
		return "ebook"
	default:
		return ""
	}
}

// rankBooks reorders search hits by how well they answer the query. Hardcover
// ranks on text match and popularity alone, which puts samplers, omnibuses and
// companion volumes above the book being looked for, so the author and year
// Silo already knows are applied here. Nothing is dropped: a wrong guess about
// relevance should reorder the picker, not empty it.
func rankBooks(books []metadata.Book, query metadata.SearchQuery) []metadata.Book {
	if len(books) < 2 {
		return books
	}

	title := normalizeTitle(query.Title)
	scored := make([]metadata.Book, len(books))
	copy(scored, books)

	scores := make(map[string]int, len(scored))
	for index, book := range scored {
		score := 0
		candidate := normalizeTitle(book.Title)
		switch {
		case title == "":
		case candidate == title:
			score += 100
		case strings.HasPrefix(candidate, title):
			score += 40
		case strings.Contains(candidate, title):
			score += 20
		}
		if matchesAnyAuthor(book, query.Authors) {
			score += 60
		}
		if query.Year > 0 && book.PublishYear == query.Year {
			score += 25
		}
		// A search for a novel returns its study guides, summaries, samplers
		// and omnibus editions alongside it, and their longer titles often
		// outrank it. Penalise them unless the query asked for one.
		if isDerivativeTitle(candidate) && !isDerivativeTitle(title) {
			score -= 70
		}
		// Hardcover's own order is the tie-break, so an unscored list comes
		// back exactly as the index ranked it.
		scores[book.ID] = score*1000 - index
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scores[scored[i].ID] > scores[scored[j].ID]
	})
	return scored
}

// normalizeTitle lowercases and strips punctuation and articles so "The Way of
// Kings" and "Way of Kings" compare equal.
func normalizeTitle(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
		case unicode.IsSpace(r):
			builder.WriteRune(' ')
		}
	}
	fields := strings.Fields(builder.String())
	if len(fields) > 1 {
		switch fields[0] {
		case "the", "a", "an":
			fields = fields[1:]
		}
	}
	return strings.Join(fields, " ")
}

// derivativeMarkers name the kinds of book a catalog search returns alongside
// the novel that was asked for: works *about* it, and editions that bundle it
// with others. Each is a real book someone may own, so these only lower a
// candidate's rank; a query naming one still finds it.
var derivativeMarkers = []string{
	"summary", "summaries", "study guide", "studyguide", "book guide",
	"analysis of", "companion", "sampler", "essays on", "reading order",
	"boxed set", "box set", "omnibus", "collection", "trilogy", "saga",
	"anthology", "graphic novel", "dramatized adaptation",
	"dramatised adaptation", "notebooks of", "annotated",
}

func isDerivativeTitle(normalized string) bool {
	if normalized == "" {
		return false
	}
	for _, marker := range derivativeMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func matchesAnyAuthor(book metadata.Book, authors []string) bool {
	if len(authors) == 0 {
		return false
	}
	for _, wanted := range authors {
		wanted = normalizeTitle(wanted)
		if wanted == "" {
			continue
		}
		for _, contributor := range book.Contributors {
			if normalizeTitle(contributor.Name) == wanted {
				return true
			}
		}
	}
	return false
}

// searchDocument is one Typesense hit. The search index flattens a book, so
// these fields do not line up with the GraphQL book type.
type searchDocument struct {
	ID                     json.RawMessage `json:"id"`
	Title                  string          `json:"title"`
	Subtitle               string          `json:"subtitle"`
	Description            string          `json:"description"`
	Slug                   string          `json:"slug"`
	AuthorNames            []string        `json:"author_names"`
	Genres                 []string        `json:"genres"`
	Moods                  []string        `json:"moods"`
	Tags                   []string        `json:"tags"`
	ContentWarnings        []string        `json:"content_warnings"`
	SeriesNames            []string        `json:"series_names"`
	FeaturedSeries         json.RawMessage `json:"featured_series"`
	FeaturedSeriesPosition *float64        `json:"featured_series_position"`
	Image                  json.RawMessage `json:"image"`
	ISBNs                  []string        `json:"isbns"`
	Pages                  *int            `json:"pages"`
	AudioSeconds           *int            `json:"audio_seconds"`
	Rating                 *float64        `json:"rating"`
	RatingsCount           int             `json:"ratings_count"`
	ReleaseYear            *int            `json:"release_year"`
	Compilation            bool            `json:"compilation"`
	AlternativeTitles      json.RawMessage `json:"alternative_titles"`
}

// booksFromSearchResults maps Typesense hits onto books. ids carries the same
// books in the same order and is used when a hit has no usable id field.
func booksFromSearchResults(raw json.RawMessage, ids []int) []metadata.Book {
	if len(raw) == 0 {
		return nil
	}

	var results struct {
		Hits []struct {
			Document searchDocument `json:"document"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil
	}

	books := make([]metadata.Book, 0, len(results.Hits))
	for i, hit := range results.Hits {
		book := hit.Document.toBook()
		if book.ID == "" && i < len(ids) {
			book.ID = strconv.Itoa(ids[i])
		}
		if book.ID == "" || book.Title == "" {
			continue
		}
		if book.URL == "" {
			book.URL = metadata.BookURL(book.Slug)
		}
		books = append(books, book)
	}
	return books
}

func (d searchDocument) toBook() metadata.Book {
	book := metadata.Book{
		ID:              rawID(d.ID),
		Slug:            strings.TrimSpace(d.Slug),
		Title:           strings.TrimSpace(d.Title),
		Subtitle:        strings.TrimSpace(d.Subtitle),
		Description:     strings.TrimSpace(d.Description),
		PublishYear:     intValue(d.ReleaseYear),
		PageCount:       intValue(d.Pages),
		AudioSeconds:    intValue(d.AudioSeconds),
		RatingsCount:    d.RatingsCount,
		Compilation:     d.Compilation,
		Genres:          trimmedNonEmpty(d.Genres),
		Moods:           trimmedNonEmpty(d.Moods),
		Tags:            trimmedNonEmpty(d.Tags),
		ContentWarnings: trimmedNonEmpty(d.ContentWarnings),
		AlternateTitles: parseAlternativeTitles(d.AlternativeTitles),
		CoverURL:        parseImageURL(d.Image),
	}
	book.URL = metadata.BookURL(book.Slug)
	if d.Rating != nil {
		book.Rating = *d.Rating
	}

	for _, name := range trimmedNonEmpty(d.AuthorNames) {
		book.Contributors = append(book.Contributors, metadata.Contributor{Name: name})
	}
	book.Series = seriesFromSearchDocument(d)

	for _, isbn := range d.ISBNs {
		normalized := metadata.NormalizeISBN(isbn)
		switch len(normalized) {
		case 13:
			if book.ISBN13 == "" {
				book.ISBN13 = normalized
			}
		case 10:
			if book.ISBN10 == "" {
				book.ISBN10 = normalized
			}
		}
	}

	return book
}

func seriesFromSearchDocument(d searchDocument) []metadata.SeriesEntry {
	featured := strings.TrimSpace(parseSeriesName(d.FeaturedSeries))
	entries := make([]metadata.SeriesEntry, 0, len(d.SeriesNames)+1)
	if featured != "" {
		entries = append(entries, metadata.SeriesEntry{
			Name:     featured,
			Position: formatPosition(d.FeaturedSeriesPosition),
			Featured: true,
		})
	}
	for _, name := range trimmedNonEmpty(d.SeriesNames) {
		if strings.EqualFold(name, featured) {
			continue
		}
		entries = append(entries, metadata.SeriesEntry{Name: name})
	}
	if len(entries) == 0 {
		return nil
	}
	return entries
}

// parseSeriesName reads the series name out of the search index's
// featured_series object, whose shape has changed between index versions.
func parseSeriesName(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	for _, key := range []string{"name", "series_name", "title"} {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	if nested, ok := object["series"].(map[string]any); ok {
		if value, ok := nested["name"].(string); ok {
			return value
		}
	}
	return ""
}

// parseImageURL reads a Typesense image field, which is either a URL string or
// an object carrying one.
func parseImageURL(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}

	var object struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		return strings.TrimSpace(object.URL)
	}
	return ""
}

// rawID reads a Typesense document id, which is a string in the index even
// though it holds a Hardcover book ID.
func rawID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}

	var number int
	if err := json.Unmarshal(raw, &number); err == nil && number > 0 {
		return strconv.Itoa(number)
	}
	return ""
}
