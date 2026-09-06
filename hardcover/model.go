package hardcover

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/totza2010/silo-plugin-metadata-hardcover/metadata"
)

// book mirrors the BookFields fragment.
type book struct {
	ID                     int             `json:"id"`
	Slug                   string          `json:"slug"`
	Title                  string          `json:"title"`
	Subtitle               string          `json:"subtitle"`
	Description            string          `json:"description"`
	ReleaseDate            string          `json:"release_date"`
	ReleaseYear            *int            `json:"release_year"`
	Pages                  *int            `json:"pages"`
	AudioSeconds           *int            `json:"audio_seconds"`
	Compilation            bool            `json:"compilation"`
	Rating                 *float64        `json:"rating"`
	RatingsCount           int             `json:"ratings_count"`
	AlternativeTitles      json.RawMessage `json:"alternative_titles"`
	CachedTags             json.RawMessage `json:"cached_tags"`
	Image                  *image          `json:"image"`
	Contributions          []contribution  `json:"contributions"`
	BookSeries             []bookSeries    `json:"book_series"`
	DefaultEbookEdition    *edition        `json:"default_ebook_edition"`
	DefaultPhysicalEdition *edition        `json:"default_physical_edition"`
	DefaultAudioEdition    *edition        `json:"default_audio_edition"`
	DefaultCoverEdition    *edition        `json:"default_cover_edition"`
	Editions               []edition       `json:"editions"`
}

type image struct {
	URL string `json:"url"`
}

type contribution struct {
	Contribution string `json:"contribution"`
	Author       *struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Image *image `json:"image"`
	} `json:"author"`
}

type bookSeries struct {
	Featured bool     `json:"featured"`
	Position *float64 `json:"position"`
	Series   *struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"series"`
}

type edition struct {
	ID             int    `json:"id"`
	ISBN10         string `json:"isbn_10"`
	ISBN13         string `json:"isbn_13"`
	ASIN           string `json:"asin"`
	EditionFormat  string `json:"edition_format"`
	PhysicalFormat string `json:"physical_format"`
	Pages          *int   `json:"pages"`
	AudioSeconds   *int   `json:"audio_seconds"`
	ReleaseDate    string `json:"release_date"`
	Publisher      *struct {
		Name string `json:"name"`
	} `json:"publisher"`
	Language *struct {
		Code2    string `json:"code2"`
		Code3    string `json:"code3"`
		Language string `json:"language"`
	} `json:"language"`
	Image         *image         `json:"image"`
	Contributions []contribution `json:"contributions"`
}

// preference describes which edition to prefer when a book has several. It
// comes from the item type Silo is scanning and the library language.
type preference struct {
	// format is "ebook", "audiobook", or "" for no preference.
	format string
	// language is a two- or three-letter code, or a language name.
	language string
}

// toBook maps a Hardcover book onto the plugin's record. The book row carries
// identity, description, tags, contributors and series; publisher, ISBN,
// language and format live on editions, so one edition is chosen to fill them.
func (b book) toBook(pref preference) metadata.Book {
	out := metadata.Book{
		ID:              strconv.Itoa(b.ID),
		Slug:            strings.TrimSpace(b.Slug),
		Title:           strings.TrimSpace(b.Title),
		Subtitle:        strings.TrimSpace(b.Subtitle),
		Description:     strings.TrimSpace(b.Description),
		ReleaseDate:     strings.TrimSpace(b.ReleaseDate),
		Compilation:     b.Compilation,
		RatingsCount:    b.RatingsCount,
		AlternateTitles: parseAlternativeTitles(b.AlternativeTitles),
		Contributors:    nil,
		Series:          b.series(),
	}
	out.URL = metadata.BookURL(out.Slug)

	out.PublishYear = intValue(b.ReleaseYear)
	if out.PublishYear == 0 {
		out.PublishYear = firstYear(out.ReleaseDate)
	}
	out.PageCount = intValue(b.Pages)
	out.AudioSeconds = intValue(b.AudioSeconds)
	if b.Rating != nil && !math.IsNaN(*b.Rating) {
		out.Rating = *b.Rating
	}

	tags := parseCachedTags(b.CachedTags)
	out.Genres = capTags(tags["genre"])
	out.Moods = capTags(tags["mood"])
	out.Tags = capTags(tags["tag"])
	out.ContentWarnings = capTags(tags["content warning"])

	if b.Image != nil {
		out.CoverURL = strings.TrimSpace(b.Image.URL)
	}

	chosen, hasChosen := b.chooseEdition(pref)
	out.Contributors = b.contributors(chosen, hasChosen)
	if hasChosen {
		out.EditionID = strconv.Itoa(chosen.ID)
		out.ISBN13, out.ISBN10, out.ISBNConflict = reconcileISBNs(chosen.ISBN13, chosen.ISBN10)
		out.ASIN = strings.TrimSpace(chosen.ASIN)
		out.EditionFormat = firstNonEmpty(chosen.EditionFormat, chosen.PhysicalFormat)
		if chosen.Publisher != nil {
			out.Publisher = strings.TrimSpace(chosen.Publisher.Name)
		}
		if chosen.Language != nil {
			out.Language = firstNonEmpty(chosen.Language.Code2, chosen.Language.Code3)
			out.LanguageName = strings.TrimSpace(chosen.Language.Language)
		}
		if out.PageCount == 0 {
			out.PageCount = intValue(chosen.Pages)
		}
		if out.AudioSeconds == 0 {
			out.AudioSeconds = intValue(chosen.AudioSeconds)
		}
		if out.ReleaseDate == "" {
			out.ReleaseDate = strings.TrimSpace(chosen.ReleaseDate)
		}
		if out.PublishYear == 0 {
			out.PublishYear = firstYear(out.ReleaseDate)
		}
		if out.CoverURL == "" && chosen.Image != nil {
			out.CoverURL = strings.TrimSpace(chosen.Image.URL)
		}
	}

	// An ISBN or cover missing from the chosen edition is still worth having.
	// Editions of the same language are searched first: the ISBN of a German
	// translation does not belong on an English record.
	for _, candidate := range b.fallbackEditions(out.Language) {
		if out.ISBN13 == "" {
			out.ISBN13 = strings.TrimSpace(candidate.ISBN13)
		}
		if out.ISBN10 == "" {
			out.ISBN10 = strings.TrimSpace(candidate.ISBN10)
		}
		if out.CoverURL == "" && candidate.Image != nil {
			out.CoverURL = strings.TrimSpace(candidate.Image.URL)
		}
		if out.ISBN13 != "" && out.ISBN10 != "" && out.CoverURL != "" {
			break
		}
	}

	return out
}

// fallbackEditions orders editions for field-by-field fallback: those matching
// the record's language first, then the rest.
func (b book) fallbackEditions(language string) []edition {
	all := b.allEditions()
	if language == "" {
		return all
	}

	ordered := make([]edition, 0, len(all))
	for _, candidate := range all {
		if matchesLanguage(candidate, language) {
			ordered = append(ordered, candidate)
		}
	}
	for _, candidate := range all {
		if !matchesLanguage(candidate, language) {
			ordered = append(ordered, candidate)
		}
	}
	return ordered
}

// reconcileISBNs guards against a Hardcover edition that carries two ISBNs
// belonging to different books, which happens when records are merged. An
// ISBN-13 that does not convert back from the ISBN-10 is dropped in favour of
// the one derived from it, and the conflict is reported so the record can say
// the data was corrected.
func reconcileISBNs(isbn13, isbn10 string) (string, string, bool) {
	isbn13, isbn10 = strings.TrimSpace(isbn13), strings.TrimSpace(isbn10)
	if metadata.ISBNsAgree(isbn13, isbn10) {
		return isbn13, isbn10, false
	}
	if derived := metadata.ISBN13FromISBN10(isbn10); derived != "" {
		return derived, isbn10, true
	}
	return isbn13, isbn10, true
}

// allEditions lists the default editions ahead of the general edition list, so
// callers scanning for a first non-empty value see the curated ones first.
func (b book) allEditions() []edition {
	editions := make([]edition, 0, len(b.Editions)+4)
	for _, candidate := range []*edition{
		b.DefaultEbookEdition,
		b.DefaultPhysicalEdition,
		b.DefaultAudioEdition,
		b.DefaultCoverEdition,
	} {
		if candidate != nil {
			editions = append(editions, *candidate)
		}
	}
	return append(editions, b.Editions...)
}

// chooseEdition picks the edition whose publisher, ISBN and language describe
// the file Silo is scanning. Format preference wins, then language, then
// Hardcover's own default ordering.
func (b book) chooseEdition(pref preference) (edition, bool) {
	candidates := b.allEditions()
	if len(candidates) == 0 {
		return edition{}, false
	}

	var preferred []edition
	switch pref.format {
	case "audiobook":
		preferred = presentEditions(b.DefaultAudioEdition)
	case "ebook":
		preferred = presentEditions(b.DefaultEbookEdition, b.DefaultPhysicalEdition)
	}

	// Within the preferred set, and then across every edition, prefer one
	// that matches the requested language and carries an ISBN.
	for _, set := range [][]edition{preferred, candidates} {
		if chosen, ok := bestEdition(set, pref.language); ok {
			return chosen, true
		}
	}
	return candidates[0], true
}

func presentEditions(values ...*edition) []edition {
	editions := make([]edition, 0, len(values))
	for _, value := range values {
		if value != nil {
			editions = append(editions, *value)
		}
	}
	return editions
}

// bestEdition scores editions on language match first, then on how much of the
// record they can fill. Hardcover's editions are unevenly populated, so the
// default edition for a book is often missing the publisher or format that a
// sibling edition carries.
func bestEdition(editions []edition, language string) (edition, bool) {
	var best edition
	bestScore := -1
	for _, candidate := range editions {
		score := 0
		if matchesLanguage(candidate, language) {
			score += 10
		}
		if strings.TrimSpace(candidate.ISBN13) != "" || strings.TrimSpace(candidate.ISBN10) != "" {
			score += 3
		}
		if candidate.Publisher != nil && strings.TrimSpace(candidate.Publisher.Name) != "" {
			score += 2
		}
		if firstNonEmpty(candidate.EditionFormat, candidate.PhysicalFormat) != "" {
			score++
		}
		if candidate.Language != nil {
			score++
		}
		if score > bestScore {
			best, bestScore = candidate, score
		}
	}
	return best, bestScore >= 0
}

func matchesLanguage(candidate edition, language string) bool {
	language = strings.TrimSpace(strings.ToLower(language))
	if language == "" || candidate.Language == nil {
		return false
	}
	// Silo may pass "en", "eng", "en-US", or a language name.
	base, _, _ := strings.Cut(language, "-")
	for _, value := range []string{candidate.Language.Code2, candidate.Language.Code3, candidate.Language.Language} {
		value = strings.TrimSpace(strings.ToLower(value))
		if value != "" && (value == language || value == base) {
			return true
		}
	}
	return false
}

// contributors merges the book's credits with those of the chosen edition.
// Hardcover records the author on the book but the narrator, translator and
// illustrator on the edition that has them, so an audiobook's cast is only
// visible once both are read.
func (b book) contributors(chosen edition, hasChosen bool) []metadata.Contributor {
	contributors := appendContributors(nil, b.Contributions)
	if hasChosen {
		contributors = appendContributors(contributors, chosen.Contributions)
	}
	return contributors
}

func appendContributors(contributors []metadata.Contributor, entries []contribution) []metadata.Contributor {
	for _, entry := range entries {
		if entry.Author == nil {
			continue
		}
		name := strings.TrimSpace(entry.Author.Name)
		if name == "" {
			continue
		}
		role := normalizeRole(entry.Contribution)
		if containsContributor(contributors, name, role) {
			continue
		}
		contributor := metadata.Contributor{
			AuthorID: strconv.Itoa(entry.Author.ID),
			Name:     name,
			Role:     role,
		}
		if entry.Author.Image != nil {
			contributor.ImageURL = strings.TrimSpace(entry.Author.Image.URL)
		}
		contributors = append(contributors, contributor)
	}
	return contributors
}

func containsContributor(contributors []metadata.Contributor, name, role string) bool {
	for _, existing := range contributors {
		if strings.EqualFold(existing.Name, name) && strings.EqualFold(existing.Role, role) {
			return true
		}
	}
	return false
}

// normalizeRole treats Hardcover's empty and "Author" contributions alike:
// both mean the primary author credit.
func normalizeRole(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "author") {
		return ""
	}
	return value
}

func (b book) series() []metadata.SeriesEntry {
	entries := make([]metadata.SeriesEntry, 0, len(b.BookSeries))
	for _, entry := range b.BookSeries {
		if entry.Series == nil {
			continue
		}
		name := strings.TrimSpace(entry.Series.Name)
		if name == "" {
			continue
		}
		entries = append(entries, metadata.SeriesEntry{
			SeriesID: strconv.Itoa(entry.Series.ID),
			Name:     name,
			Position: formatPosition(entry.Position),
			Featured: entry.Featured,
		})
	}
	return entries
}

// formatPosition renders a series position without a trailing ".0", so book 4
// reads as "4" and book 4.5 as "4.5".
func formatPosition(value *float64) string {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

// parseAlternativeTitles reads Hardcover's alternative_titles JSON, which is
// either a list of strings or a list of objects keyed by title.
func parseAlternativeTitles(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	var strs []string
	if err := json.Unmarshal(raw, &strs); err == nil {
		return trimmedNonEmpty(strs)
	}

	var objects []map[string]any
	if err := json.Unmarshal(raw, &objects); err == nil {
		titles := make([]string, 0, len(objects))
		for _, object := range objects {
			for _, key := range []string{"title", "name", "value"} {
				if text, ok := object[key].(string); ok {
					titles = append(titles, text)
					break
				}
			}
		}
		return trimmedNonEmpty(titles)
	}

	return nil
}

// cachedTag is one entry of Hardcover's cached_tags payload.
type cachedTag struct {
	Tag      string `json:"tag"`
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// parseCachedTags reads cached_tags into lower-cased category buckets
// ("genre", "mood", "tag", "content warning"). The payload is an object keyed
// by category name; a flat list is also accepted, using each entry's own
// category field.
func parseCachedTags(raw json.RawMessage) map[string][]string {
	buckets := make(map[string][]string)
	if len(raw) == 0 {
		return buckets
	}

	add := func(category string, tags []cachedTag) {
		category = strings.ToLower(strings.TrimSpace(category))
		for _, entry := range tags {
			key := category
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(entry.Category))
			}
			if key == "" {
				continue
			}
			for _, name := range splitTagValue(strings.TrimSpace(entry.Tag)) {
				if name == "" {
					continue
				}
				buckets[key] = appendUnique(buckets[key], name)
			}
		}
	}

	var byCategory map[string][]cachedTag
	if err := json.Unmarshal(raw, &byCategory); err == nil {
		for category, tags := range byCategory {
			add(category, tags)
		}
		return buckets
	}

	var flat []cachedTag
	if err := json.Unmarshal(raw, &flat); err == nil {
		add("", flat)
	}
	return buckets
}

// maxTagsPerCategory bounds each tag bucket. Hardcover's cached_tags are
// crowd-sourced and ordered by how many people applied them, so the long tail
// holds contradictions ("Diverse Characters" next to "Not Diverse Characters")
// that would only add noise to a library.
const maxTagsPerCategory = 10

func capTags(values []string) []string {
	if len(values) > maxTagsPerCategory {
		return values[:maxTagsPerCategory]
	}
	return values
}

// splitTagValue splits a tag that arrived as a semicolon-joined list, which is
// how tags imported from external catalogs reach Hardcover.
func splitTagValue(value string) []string {
	if !strings.Contains(value, ";") {
		return []string{value}
	}
	parts := strings.Split(value, ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}

func trimmedNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func intValue(value *int) int {
	if value == nil || *value < 0 {
		return 0
	}
	return *value
}

// firstYear pulls the first four-digit year out of a date string.
func firstYear(value string) int {
	for i := 0; i+4 <= len(value); i++ {
		year, err := strconv.Atoi(value[i : i+4])
		if err == nil && year > 0 {
			return year
		}
	}
	return 0
}
