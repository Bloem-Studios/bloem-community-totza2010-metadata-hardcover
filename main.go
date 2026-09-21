// Command plugin serves Silo's metadata_provider.v1 capability backed by
// Hardcover.
package main

import (
	"context"
	_ "embed"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"

	pluginv1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	sdkmanifest "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginsdk/manifest"
	sdkruntime "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginsdk/runtimedefault"
	"github.com/Bloem-Studios/bloem-community-totza2010-metadata-hardcover/hardcover"
	"github.com/Bloem-Studios/bloem-community-totza2010-metadata-hardcover/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

// version is set at build time via -ldflags "-X main.version=...".
var version string

//go:embed manifest.json
var manifestJSON []byte

// userAgent identifies the plugin to Hardcover, which asks integrations to
// send one.
const userAgent = "silo-plugins-metadata-hardcover"

// options are the settings an administrator can change.
type options struct {
	APIKey          string
	SearchLimit     int
	DetailedSearch  bool
	DefaultLanguage string
}

type runtimeServer struct {
	runtimedefault.Server

	manifest *pluginv1.PluginManifest
	mu       sync.RWMutex
	client   *hardcover.Client
	options  options
}

type metadataServer struct {
	pluginv1.UnimplementedMetadataProviderServer
	runtime *runtimeServer
}

func (s *runtimeServer) GetManifest(context.Context, *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	return &pluginv1.GetManifestResponse{Manifest: s.manifest}, nil
}

func (s *runtimeServer) Configure(_ context.Context, req *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	resolved := optionsFromConfig(req.GetConfig())
	client := hardcover.New(hardcover.Options{
		APIKey:      resolved.APIKey,
		UserAgent:   userAgent + "/" + firstText(version, "dev"),
		SearchLimit: resolved.SearchLimit,
	})

	s.mu.Lock()
	s.client, s.options = client, resolved
	s.mu.Unlock()

	return &pluginv1.ConfigureResponse{}, nil
}

// state returns the configured client and options for one request.
func (s *runtimeServer) state() (*hardcover.Client, options) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client, s.options
}

func (s *metadataServer) Search(ctx context.Context, req *pluginv1.SearchMetadataRequest) (*pluginv1.SearchMetadataResponse, error) {
	client, resolved := s.runtime.state()
	if client == nil || !client.Configured() {
		// An unconfigured provider returns nothing rather than failing the
		// whole scan for the other providers in the chain.
		return &pluginv1.SearchMetadataResponse{}, nil
	}

	query := metadata.SearchQuery{
		Title:       req.GetQuery(),
		Year:        int(req.GetYear()),
		ItemType:    req.GetItemType(),
		Language:    firstText(req.GetLanguage(), resolved.DefaultLanguage),
		ProviderIDs: stringMap(req.GetProviderIds()),
	}

	search := client.Search
	if resolved.DetailedSearch {
		search = client.SearchDetailed
	}
	books, err := search(ctx, query)
	if err != nil {
		if errors.Is(err, hardcover.ErrNoAPIKey) {
			return &pluginv1.SearchMetadataResponse{}, nil
		}
		return nil, err
	}

	response := &pluginv1.SearchMetadataResponse{
		Results: make([]*pluginv1.ProviderSearchResult, 0, len(books)),
	}
	for _, book := range books {
		result, err := searchResult(book, req.GetItemType())
		if err != nil {
			return nil, err
		}
		response.Results = append(response.Results, result)
	}
	return response, nil
}

func (s *metadataServer) GetMetadata(ctx context.Context, req *pluginv1.GetMetadataRequest) (*pluginv1.GetMetadataResponse, error) {
	client, resolved := s.runtime.state()
	if client == nil || !client.Configured() {
		return &pluginv1.GetMetadataResponse{}, nil
	}

	book, err := client.Fetch(ctx, metadata.SearchQuery{
		ItemType:    req.GetItemType(),
		Language:    firstText(req.GetLanguage(), resolved.DefaultLanguage),
		ProviderIDs: providerIDs(req.GetProviderIds(), req.GetProviderId()),
	})
	if err != nil {
		if errors.Is(err, hardcover.ErrNoAPIKey) {
			return &pluginv1.GetMetadataResponse{}, nil
		}
		return nil, err
	}
	if book == nil {
		return &pluginv1.GetMetadataResponse{}, nil
	}

	item, err := metadataItem(*book, req.GetItemType())
	if err != nil {
		return nil, err
	}
	return &pluginv1.GetMetadataResponse{Item: item}, nil
}

func searchResult(book metadata.Book, itemType string) (*pluginv1.ProviderSearchResult, error) {
	ids, err := stringStruct(metadata.ProviderIDsFromBook(book))
	if err != nil {
		return nil, err
	}

	return &pluginv1.ProviderSearchResult{
		ProviderId:       book.ID,
		ItemType:         strings.TrimSpace(itemType),
		Title:            book.Title,
		OriginalTitle:    book.Title,
		Year:             int32(book.PublishYear),
		Overview:         searchOverview(book),
		ProviderIds:      ids,
		ImageUrl:         book.CoverURL,
		TitleAliases:     titleAliases(book),
		OriginalLanguage: book.Language,
	}, nil
}

func metadataItem(book metadata.Book, itemType string) (*pluginv1.MetadataItem, error) {
	ids, err := stringStruct(metadata.ProviderIDsFromBook(book))
	if err != nil {
		return nil, err
	}

	item := &pluginv1.MetadataItem{
		ProviderId:       book.ID,
		ItemType:         strings.TrimSpace(itemType),
		Title:            book.Title,
		OriginalTitle:    book.Title,
		SortTitle:        sortTitle(book),
		Tagline:          book.Subtitle,
		Year:             int32(book.PublishYear),
		ReleaseDate:      book.ReleaseDate,
		Overview:         book.Description,
		Genres:           book.Genres,
		Studios:          nonEmpty(book.Publisher),
		OriginalLanguage: book.Language,
		PosterPath:       book.CoverURL,
		People:           people(book),
		ProviderIds:      ids,
		Ratings:          ratings(book),
		Metadata:         extras(book),
		TitleAliases:     titleAliases(book),
	}
	// Silo reports runtime in minutes; only an audiobook edition has one.
	if book.AudioSeconds > 0 {
		item.Runtime = int32(book.AudioSeconds / 60)
	}
	return item, nil
}

// searchOverview keeps the picker readable: the subtitle and series carry more
// signal than the first line of a long description.
func searchOverview(book metadata.Book) string {
	parts := make([]string, 0, 3)
	if book.Subtitle != "" {
		parts = append(parts, book.Subtitle)
	}
	if series, ok := book.PrimarySeries(); ok {
		parts = append(parts, seriesLabel(series))
	}
	if authors := book.Authors(); len(authors) > 0 {
		parts = append(parts, strings.Join(authors, ", "))
	}
	if len(parts) == 0 {
		return book.Description
	}
	return strings.Join(parts, " • ")
}

func seriesLabel(series metadata.SeriesEntry) string {
	if series.Position == "" {
		return series.Name
	}
	return series.Name + " #" + series.Position
}

// sortTitle puts a book next to its siblings when a library sorts by title.
func sortTitle(book metadata.Book) string {
	series, ok := book.PrimarySeries()
	if !ok || series.Position == "" {
		return ""
	}
	return series.Name + " " + paddedPosition(series.Position) + " " + book.Title
}

// paddedPosition left-pads the integer part so "2" sorts before "10".
func paddedPosition(position string) string {
	whole, rest, hasRest := strings.Cut(position, ".")
	if _, err := strconv.Atoi(whole); err != nil {
		return position
	}
	for len(whole) < 4 {
		whole = "0" + whole
	}
	if hasRest {
		return whole + "." + rest
	}
	return whole
}

func people(book metadata.Book) []*pluginv1.PersonRecord {
	records := make([]*pluginv1.PersonRecord, 0, len(book.Contributors))
	for _, contributor := range book.Contributors {
		records = append(records, &pluginv1.PersonRecord{
			Name:      contributor.Name,
			Kind:      personKind(contributor.Role),
			SortOrder: int32(len(records)),
			PhotoPath: contributor.ImageURL,
		})
	}
	if len(records) == 0 {
		return nil
	}
	return records
}

// personKind maps a Hardcover contribution onto Silo's person kinds. Roles
// Silo has no kind for are passed through lower-cased.
func personKind(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "":
		return "author"
	case "narrator", "translator", "illustrator", "editor":
		return role
	default:
		return role
	}
}

func ratings(book metadata.Book) *structpb.Struct {
	if book.Rating <= 0 {
		return nil
	}
	fields := map[string]*structpb.Value{
		"hardcover": structpb.NewNumberValue(book.Rating),
	}
	if book.RatingsCount > 0 {
		fields["hardcover_count"] = structpb.NewNumberValue(float64(book.RatingsCount))
	}
	return &structpb.Struct{Fields: fields}
}

// extras carries the book fields Silo has no dedicated column for.
func extras(book metadata.Book) *structpb.Struct {
	fields := make(map[string]*structpb.Value)
	addString := func(key, value string) {
		if value = strings.TrimSpace(value); value != "" {
			fields[key] = structpb.NewStringValue(value)
		}
	}
	addNumber := func(key string, value int) {
		if value > 0 {
			fields[key] = structpb.NewNumberValue(float64(value))
		}
	}
	addList := func(key string, values []string) {
		if len(values) == 0 {
			return
		}
		list := make([]*structpb.Value, 0, len(values))
		for _, value := range values {
			list = append(list, structpb.NewStringValue(value))
		}
		fields[key] = structpb.NewListValue(&structpb.ListValue{Values: list})
	}

	addString("subtitle", book.Subtitle)
	addString("isbn", book.ISBN())
	addString("isbn_13", metadata.NormalizeISBN(book.ISBN13))
	addString("isbn_10", metadata.NormalizeISBN(book.ISBN10))
	addString("asin", book.ASIN)
	addString("publisher", book.Publisher)
	addString("language", book.Language)
	addString("language_name", book.LanguageName)
	addString("edition_format", book.EditionFormat)
	addString("hardcover_url", book.URL)
	addString("hardcover_slug", book.Slug)
	addNumber("page_count", book.PageCount)
	addNumber("audio_seconds", book.AudioSeconds)
	addList("moods", book.Moods)
	addList("tags", book.Tags)
	addList("content_warnings", book.ContentWarnings)
	if book.Compilation {
		fields["compilation"] = structpb.NewBoolValue(true)
	}
	if book.ISBNConflict {
		// Hardcover held two ISBNs for different books on this edition; the
		// ISBN-13 above was rebuilt from the ISBN-10 rather than trusted.
		fields["isbn_conflict"] = structpb.NewBoolValue(true)
	}

	if series, ok := book.PrimarySeries(); ok {
		addString("series_name", series.Name)
		addString("series_position", series.Position)
		addString("series_id", series.SeriesID)
	}
	if len(book.Series) > 1 {
		names := make([]string, 0, len(book.Series))
		for _, entry := range book.Series {
			names = append(names, seriesLabel(entry))
		}
		addList("series_all", names)
	}

	for _, kind := range []string{"narrator", "translator", "illustrator", "editor"} {
		var names []string
		for _, contributor := range book.Contributors {
			if strings.EqualFold(contributor.Role, kind) {
				names = append(names, contributor.Name)
			}
		}
		addList(kind+"s", names)
	}

	if len(fields) == 0 {
		return nil
	}
	return &structpb.Struct{Fields: fields}
}

func titleAliases(book metadata.Book) []*pluginv1.TitleAlias {
	aliases := make([]*pluginv1.TitleAlias, 0, len(book.AlternateTitles))
	for _, title := range book.AlternateTitles {
		if strings.EqualFold(title, book.Title) {
			continue
		}
		aliases = append(aliases, &pluginv1.TitleAlias{Title: title, Kind: "alternative"})
	}
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}

func optionsFromConfig(entries []*pluginv1.ConfigEntry) options {
	var resolved options
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		value := configValue(entry.GetValue())
		switch strings.TrimSpace(entry.GetKey()) {
		case "api_key":
			resolved.APIKey = value
		case "search_limit":
			if limit, err := strconv.Atoi(value); err == nil && limit > 0 {
				resolved.SearchLimit = limit
			}
		case "detailed_search":
			resolved.DetailedSearch = truthy(value)
		case "default_language":
			resolved.DefaultLanguage = value
		}
	}
	return resolved
}

func truthy(value string) bool {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	return err == nil && parsed
}

// configValue reads the scalar out of a config entry, which Silo wraps in an
// object under one of a few conventional keys.
func configValue(value *structpb.Struct) string {
	if value == nil {
		return ""
	}
	fields := value.GetFields()
	for _, key := range []string{"value", "string", "text"} {
		if raw := fields[key]; raw != nil {
			return scalarText(raw)
		}
	}
	for _, raw := range fields {
		if text := scalarText(raw); text != "" {
			return text
		}
	}
	return ""
}

func scalarText(value *structpb.Value) string {
	switch kind := value.GetKind().(type) {
	case *structpb.Value_StringValue:
		return strings.TrimSpace(kind.StringValue)
	case *structpb.Value_NumberValue:
		return strconv.FormatFloat(kind.NumberValue, 'f', -1, 64)
	case *structpb.Value_BoolValue:
		return strconv.FormatBool(kind.BoolValue)
	default:
		return ""
	}
}

func stringMap(value *structpb.Struct) map[string]string {
	result := make(map[string]string)
	if value == nil {
		return result
	}
	for key, raw := range value.GetFields() {
		if text := scalarText(raw); text != "" {
			result[key] = text
		}
	}
	return result
}

// providerIDs merges the request's identifier map with the standalone
// provider_id field, which Silo sends when it already knows this plugin's ID.
func providerIDs(value *structpb.Struct, providerID string) map[string]string {
	ids := stringMap(value)
	if providerID = strings.TrimSpace(providerID); providerID != "" && ids[metadata.CapabilityID] == "" {
		ids[metadata.CapabilityID] = providerID
	}
	return ids
}

func stringStruct(values map[string]string) (*structpb.Struct, error) {
	fields := make(map[string]any, len(values))
	for key, value := range values {
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key != "" && value != "" {
			fields[key] = value
		}
	}
	return structpb.NewStruct(fields)
}

func nonEmpty(value string) []string {
	if value = strings.TrimSpace(value); value == "" {
		return nil
	}
	return []string{value}
}

func firstText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func main() {
	manifest, err := sdkmanifest.LoadWithChecksum(manifestJSON, version)
	if err != nil {
		log.Fatalf("load manifest: %v", err)
	}

	rs := &runtimeServer{manifest: manifest}
	sdkruntime.Serve(sdkruntime.ServeConfig{
		Servers: sdkruntime.CapabilityServers{
			Runtime:          rs,
			MetadataProvider: &metadataServer{runtime: rs},
		},
	})
}
