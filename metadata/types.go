// Package metadata holds the provider-neutral book record the plugin maps
// Hardcover responses into, plus the identifier helpers Silo needs.
package metadata

// SearchQuery is a metadata lookup request from Silo.
type SearchQuery struct {
	Title       string
	Authors     []string
	Year        int
	ItemType    string
	Language    string
	ProviderIDs map[string]string
}

// Contributor is one person credited on a book. Role is the Hardcover
// contribution string ("Narrator", "Illustrator", ...); an empty Role means
// the primary author credit.
type Contributor struct {
	AuthorID string
	Name     string
	Role     string
	ImageURL string
}

// SeriesEntry is one series a book belongs to. Position is kept as text
// because Hardcover stores fractional positions such as "4.5".
type SeriesEntry struct {
	SeriesID string
	Name     string
	Position string
	Featured bool
}

// Book is everything the plugin pulls for a single Hardcover book.
type Book struct {
	ID              string
	Slug            string
	URL             string
	Title           string
	Subtitle        string
	AlternateTitles []string
	Description     string
	ReleaseDate     string
	PublishYear     int
	Publisher       string
	Language        string
	LanguageName    string
	ISBN13          string
	ISBN10          string
	// ISBNConflict records that the source edition held an ISBN-13 and an
	// ISBN-10 for different books, and that ISBN13 was rebuilt from ISBN10.
	ISBNConflict    bool
	ASIN            string
	EditionID       string
	EditionFormat   string
	PageCount       int
	AudioSeconds    int
	Compilation     bool
	Genres          []string
	Moods           []string
	Tags            []string
	ContentWarnings []string
	Series          []SeriesEntry
	Contributors    []Contributor
	CoverURL        string
	Rating          float64
	RatingsCount    int
}

// ISBN returns the preferred ISBN for the book, favouring ISBN-13.
func (b Book) ISBN() string {
	if isbn := NormalizeISBN(b.ISBN13); isbn != "" {
		return isbn
	}
	return NormalizeISBN(b.ISBN10)
}

// Authors returns the names credited as authors, in Hardcover's order.
func (b Book) Authors() []string {
	names := make([]string, 0, len(b.Contributors))
	for _, contributor := range b.Contributors {
		if contributor.Role == "" && contributor.Name != "" {
			names = append(names, contributor.Name)
		}
	}
	return names
}

// PrimarySeries returns the featured series entry, or the first one when no
// entry is featured.
func (b Book) PrimarySeries() (SeriesEntry, bool) {
	for _, entry := range b.Series {
		if entry.Featured && entry.Name != "" {
			return entry, true
		}
	}
	for _, entry := range b.Series {
		if entry.Name != "" {
			return entry, true
		}
	}
	return SeriesEntry{}, false
}
