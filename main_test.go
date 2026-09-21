package main

import (
	"testing"

	pluginv1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	sdkmanifest "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginsdk/manifest"
	"github.com/Bloem-Studios/bloem-community-totza2010-metadata-hardcover/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

func sampleBook() metadata.Book {
	return metadata.Book{
		ID:              "240029",
		Slug:            "the-way-of-kings",
		URL:             "https://hardcover.app/books/the-way-of-kings",
		Title:           "The Way of Kings",
		Subtitle:        "Book One of the Stormlight Archive",
		Description:     "Roshar is a world of stone and storms.",
		ReleaseDate:     "2010-08-31",
		PublishYear:     2010,
		Publisher:       "Tor Books",
		Language:        "en",
		LanguageName:    "English",
		ISBN13:          "9780765326355",
		ISBN10:          "0765326353",
		ASIN:            "B003P2WO5E",
		EditionID:       "771002",
		EditionFormat:   "Hardcover",
		PageCount:       1007,
		AudioSeconds:    161640,
		Genres:          []string{"Fantasy", "Epic Fantasy"},
		Moods:           []string{"Adventurous"},
		ContentWarnings: []string{"War"},
		AlternateTitles: []string{"El camino de los reyes"},
		CoverURL:        "https://images.hardcover.app/way-of-kings.jpg",
		Rating:          4.42,
		RatingsCount:    18422,
		Series: []metadata.SeriesEntry{
			{SeriesID: "4110", Name: "The Stormlight Archive", Position: "1", Featured: true},
			{SeriesID: "9001", Name: "The Cosmere", Position: "5.5"},
		},
		Contributors: []metadata.Contributor{
			{AuthorID: "34211", Name: "Brandon Sanderson", ImageURL: "https://images.hardcover.app/sanderson.jpg"},
			{AuthorID: "51001", Name: "Michael Kramer", Role: "Narrator"},
		},
	}
}

func TestManifestLoads(t *testing.T) {
	// LoadWithChecksum also validates the manifest, so a malformed config
	// schema or capability fails here rather than at install time.
	m, err := sdkmanifest.LoadWithChecksum(manifestJSON, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if m.GetPluginId() != "silo.metadata.hardcover" {
		t.Errorf("plugin_id = %q", m.GetPluginId())
	}
	if len(m.GetCapabilities()) != 1 || m.GetCapabilities()[0].GetId() != metadata.CapabilityID {
		t.Errorf("capabilities = %#v", m.GetCapabilities())
	}

	keys := make(map[string]bool)
	for _, schema := range m.GetGlobalConfigSchema() {
		keys[schema.GetKey()] = true
	}
	for _, key := range []string{"api_key", "search_limit", "detailed_search", "default_language"} {
		if !keys[key] {
			t.Errorf("manifest is missing config key %q", key)
		}
	}
}

func TestMetadataItemMapsBook(t *testing.T) {
	item, err := metadataItem(sampleBook(), "book")
	if err != nil {
		t.Fatal(err)
	}

	if item.GetProviderId() != "240029" || item.GetTitle() != "The Way of Kings" {
		t.Errorf("identity = %q / %q", item.GetProviderId(), item.GetTitle())
	}
	if item.GetYear() != 2010 || item.GetReleaseDate() != "2010-08-31" {
		t.Errorf("release = %d / %q", item.GetYear(), item.GetReleaseDate())
	}
	if item.GetTagline() != "Book One of the Stormlight Archive" {
		t.Errorf("Tagline = %q", item.GetTagline())
	}
	if got := item.GetGenres(); len(got) != 2 || got[0] != "Fantasy" {
		t.Errorf("Genres = %#v", got)
	}
	if got := item.GetStudios(); len(got) != 1 || got[0] != "Tor Books" {
		t.Errorf("Studios = %#v", got)
	}
	if item.GetOriginalLanguage() != "en" {
		t.Errorf("OriginalLanguage = %q", item.GetOriginalLanguage())
	}
	if item.GetPosterPath() != "https://images.hardcover.app/way-of-kings.jpg" {
		t.Errorf("PosterPath = %q", item.GetPosterPath())
	}
	// Runtime is reported in minutes.
	if item.GetRuntime() != 2694 {
		t.Errorf("Runtime = %d", item.GetRuntime())
	}
	if got := item.GetSortTitle(); got != "The Stormlight Archive 0001 The Way of Kings" {
		t.Errorf("SortTitle = %q", got)
	}
	if aliases := item.GetTitleAliases(); len(aliases) != 1 || aliases[0].GetTitle() != "El camino de los reyes" {
		t.Errorf("TitleAliases = %#v", aliases)
	}

	people := item.GetPeople()
	if len(people) != 2 {
		t.Fatalf("People = %#v", people)
	}
	if people[0].GetKind() != "author" || people[0].GetName() != "Brandon Sanderson" {
		t.Errorf("People[0] = %#v", people[0])
	}
	if people[0].GetPhotoPath() == "" {
		t.Error("People[0] lost its photo")
	}
	if people[1].GetKind() != "narrator" {
		t.Errorf("People[1].Kind = %q", people[1].GetKind())
	}

	ids := item.GetProviderIds().AsMap()
	if ids["hardcover"] != "240029" || ids["isbn"] != "9780765326355" || ids["asin"] != "B003P2WO5E" {
		t.Errorf("ProviderIds = %#v", ids)
	}

	ratings := item.GetRatings().AsMap()
	if ratings["hardcover"] != 4.42 || ratings["hardcover_count"] != float64(18422) {
		t.Errorf("Ratings = %#v", ratings)
	}

	extras := item.GetMetadata().AsMap()
	if extras["series_name"] != "The Stormlight Archive" || extras["series_position"] != "1" {
		t.Errorf("series extras = %#v", extras)
	}
	if extras["publisher"] != "Tor Books" || extras["edition_format"] != "Hardcover" {
		t.Errorf("edition extras = %#v", extras)
	}
	if extras["page_count"] != float64(1007) {
		t.Errorf("page_count = %#v", extras["page_count"])
	}
	if extras["hardcover_url"] != "https://hardcover.app/books/the-way-of-kings" {
		t.Errorf("hardcover_url = %#v", extras["hardcover_url"])
	}
	narrators, ok := extras["narrators"].([]any)
	if !ok || len(narrators) != 1 || narrators[0] != "Michael Kramer" {
		t.Errorf("narrators = %#v", extras["narrators"])
	}
	seriesAll, ok := extras["series_all"].([]any)
	if !ok || len(seriesAll) != 2 || seriesAll[1] != "The Cosmere #5.5" {
		t.Errorf("series_all = %#v", extras["series_all"])
	}
}

func TestMetadataItemWithoutOptionalFields(t *testing.T) {
	item, err := metadataItem(metadata.Book{ID: "1", Title: "Bare"}, "book")
	if err != nil {
		t.Fatal(err)
	}
	if item.GetRatings() != nil {
		t.Errorf("Ratings = %#v, want nil for an unrated book", item.GetRatings())
	}
	if item.GetRuntime() != 0 || item.GetSortTitle() != "" || item.GetTitleAliases() != nil {
		t.Errorf("optional fields leaked: %#v", item)
	}
}

func TestSearchResultSummarizesTheBook(t *testing.T) {
	result, err := searchResult(sampleBook(), "book")
	if err != nil {
		t.Fatal(err)
	}
	if result.GetProviderId() != "240029" || result.GetYear() != 2010 {
		t.Errorf("result = %#v", result)
	}
	want := "Book One of the Stormlight Archive • The Stormlight Archive #1 • Brandon Sanderson"
	if got := result.GetOverview(); got != want {
		t.Errorf("Overview = %q, want %q", got, want)
	}
	if result.GetImageUrl() == "" {
		t.Error("ImageUrl is empty")
	}
}

func TestSearchResultFallsBackToDescription(t *testing.T) {
	result, err := searchResult(metadata.Book{ID: "1", Title: "Bare", Description: "A description."}, "book")
	if err != nil {
		t.Fatal(err)
	}
	if result.GetOverview() != "A description." {
		t.Errorf("Overview = %q", result.GetOverview())
	}
}

func TestOptionsFromConfig(t *testing.T) {
	entry := func(key string, value *structpb.Value) *pluginv1.ConfigEntry {
		return &pluginv1.ConfigEntry{
			Key:   key,
			Value: &structpb.Struct{Fields: map[string]*structpb.Value{"value": value}},
		}
	}

	got := optionsFromConfig([]*pluginv1.ConfigEntry{
		entry("api_key", structpb.NewStringValue(" token ")),
		entry("search_limit", structpb.NewNumberValue(35)),
		entry("detailed_search", structpb.NewBoolValue(true)),
		entry("default_language", structpb.NewStringValue("th")),
		entry("unknown", structpb.NewStringValue("ignored")),
		nil,
	})

	want := options{APIKey: "token", SearchLimit: 35, DetailedSearch: true, DefaultLanguage: "th"}
	if got != want {
		t.Fatalf("optionsFromConfig() = %#v, want %#v", got, want)
	}
}

func TestOptionsFromConfigIgnoresUnusableValues(t *testing.T) {
	got := optionsFromConfig([]*pluginv1.ConfigEntry{
		{Key: "search_limit", Value: &structpb.Struct{Fields: map[string]*structpb.Value{
			"value": structpb.NewStringValue("not a number"),
		}}},
		{Key: "api_key"},
	})
	if got != (options{}) {
		t.Fatalf("optionsFromConfig() = %#v, want the zero value", got)
	}
}

func TestUnconfiguredPluginReturnsEmptyResponses(t *testing.T) {
	rs := &runtimeServer{}
	server := &metadataServer{runtime: rs}

	search, err := server.Search(t.Context(), &pluginv1.SearchMetadataRequest{Query: "kings"})
	if err != nil || len(search.GetResults()) != 0 {
		t.Fatalf("Search() = %#v, %v", search, err)
	}
	item, err := server.GetMetadata(t.Context(), &pluginv1.GetMetadataRequest{ProviderId: "240029"})
	if err != nil || item.GetItem() != nil {
		t.Fatalf("GetMetadata() = %#v, %v", item, err)
	}
}

func TestProviderIDsPrefersTheExplicitMap(t *testing.T) {
	ids, err := structpb.NewStruct(map[string]any{"hardcover": "240029", "isbn": "9780765326355"})
	if err != nil {
		t.Fatal(err)
	}

	got := providerIDs(ids, "fallback")
	if got["hardcover"] != "240029" || got["isbn"] != "9780765326355" {
		t.Fatalf("providerIDs() = %#v", got)
	}

	got = providerIDs(nil, "240029")
	if got[metadata.CapabilityID] != "240029" {
		t.Fatalf("providerIDs() fallback = %#v", got)
	}
}

func TestPaddedPosition(t *testing.T) {
	cases := map[string]string{"1": "0001", "10": "0010", "4.5": "0004.5", "": "", "x": "x"}
	for input, want := range cases {
		if got := paddedPosition(input); got != want {
			t.Errorf("paddedPosition(%q) = %q, want %q", input, got, want)
		}
	}
}
