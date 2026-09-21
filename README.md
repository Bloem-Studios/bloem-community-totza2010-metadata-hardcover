## Bloem community build

This is Bloem's community build of [totza2010/silo-plugin-metadata-hardcover](https://github.com/totza2010/silo-plugin-metadata-hardcover) by **totza2010**
(contributors: totza2010). It is ported to the Bloem plugin SDK and listed in the
Bloem community plugin catalog. All credit for the plugin goes to its author; please
report plugin behavior issues upstream. See [NOTICE](NOTICE) for provenance.

---

# Silo Hardcover Metadata Plugin

A single-source metadata provider for [Silo](https://github.com/Silo-Server/silo-server)
book libraries, backed entirely by [Hardcover](https://hardcover.app).

One provider, one API token, and every field Hardcover has for a book.

## Capability

- `metadata_provider.v1`
- Capability ID: `hardcover`
- Default priority: `ebook = 1`, `audiobook = 1`

## What it returns

| Field | Source |
| --- | --- |
| Title, subtitle, alternate titles | `books.title`, `subtitle`, `alternative_titles` |
| Authors | `books.contributions` |
| Narrators, translators, illustrators, editors | `editions.contributions` on the chosen edition — Hardcover credits these per edition, not per book |
| Series name and position | `book_series`, featured entry first, fractional positions kept (`4.5`) |
| Release date and year | `books.release_date` / `release_year`, falling back to the chosen edition |
| Cover | `books.image`, falling back to an edition's cover |
| Publisher, language, ISBN-13, ISBN-10, ASIN, edition format | the chosen edition |
| Page count, audiobook runtime | `books.pages`, `audio_seconds` |
| Genres, moods, tags, content warnings | `cached_tags` |
| Rating and rating count | `books.rating`, `ratings_count` |

Hardcover stores publisher, language, ISBN, format, and the narrator and
translator credits on editions rather than on the book, so the plugin picks one
edition per lookup. It prefers an edition matching the library's item type
(audiobook or ebook) and language, then the one that fills in the most fields.
Asking for the audiobook of a book therefore returns the audio publisher, the
runtime, and the narrators. The full field list lands in
`MetadataItem.metadata`, so nothing is dropped for want of a dedicated Silo
column.

### Data corrections

Hardcover's catalog is community-edited, and two of its quirks are handled here
rather than passed through:

- **Conflicting ISBNs.** Some editions hold an ISBN-13 and an ISBN-10 that
  belong to different books, which would attach the wrong identifier to a
  library item. Because every ISBN-10 maps to exactly one ISBN-13, a pair that
  does not agree is detected; the ISBN-13 is rebuilt from the ISBN-10 and
  `isbn_conflict` is set in `metadata`.
- **Tag noise.** `cached_tags` is crowd-sourced and ordered by how many people
  applied each tag, so its long tail holds contradictions. Each category is
  capped at its ten most-applied tags, and tags that arrived as a
  semicolon-joined list are split into separate values.

## Matching

Searches go to Hardcover's own search index, which covers titles, ISBNs, series
names, author names, and alternate titles. Hasura pattern operators such as
`_ilike` are disabled for API tokens, so this is the only text search available.

Only the title is sent. That index weights the title far above the author name,
so appending an author makes a compilation whose *title* contains that name
outrank the book itself: searching "The Way of Kings Brandon Sanderson" returns
the *Brandon Sanderson Sampler* first. The author and year Silo already knows
are applied afterwards, as a re-ranking pass over the hits. Nothing is dropped
by that pass, so a wrong guess reorders the picker rather than emptying it.

An item Silo has already matched is resolved by, in order: Hardcover book ID,
Hardcover slug, ISBN-13 or ISBN-10, then ASIN. ISBNs are normalized and
checksum-validated first, so a hyphenated value from a file still matches.

## Setup

1. Sign in at [hardcover.app](https://hardcover.app) and open
   **Account settings → Hardcover API**.
2. Create a new API key and copy the token.
3. Install the plugin, paste the token into **API token**, and add
   **Hardcover Metadata** to a library's metadata provider chain.

The plugin makes no network requests until a token is configured, and returns
empty results rather than failing a scan while it is unconfigured.

## Configuration

| Key | Meaning |
| --- | --- |
| `api_key` | Hardcover personal access token. Required. Accepts a raw token or one prefixed with `Bearer`. |
| `search_limit` | How many candidates a search returns. Defaults to 20. |
| `detailed_search` | Load publisher, language, and full series data for every search result, at the cost of one extra request per search. Details are always loaded when an item is matched. |
| `default_language` | Language code such as `en` or `th`, used to pick an edition. The library's own language wins when it is set. |

## Rate limits

Hardcover's free plan allows 60 requests per minute with a burst of 10, and
5,000 per day. The plugin paces itself to that budget with a token bucket, and
backs off on `429` and `503` using the `Retry-After` and `RateLimit` headers.
A search costs one request; matching an item costs one more.

## Identity

Books are identified by Hardcover book ID, with the slug, ISBNs, ASIN, and
edition ID stored alongside. Contributors are mapped as people with their
Hardcover role as the kind.

## Development

```sh
GOWORK=off go test ./...
```

The default suite runs entirely against fixtures. To check the query documents
against the live schema, supply your own token:

```sh
HARDCOVER_API_TOKEN=... GOWORK=off go test ./hardcover/ -run TestLive -v
```

```sh
make build
```

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request.

## License

[Apache-2.0](LICENSE).
