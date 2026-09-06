package hardcover

// editionFields is the edition selection reused by every book query. Hardcover
// spreads publisher, language, ISBNs, format, and the narrator and translator
// credits across editions rather than the book, so a book query has to reach
// into them.
const editionFields = `
fragment EditionFields on editions {
  id
  isbn_10
  isbn_13
  asin
  edition_format
  physical_format
  pages
  audio_seconds
  release_date
  publisher { name }
  language { code2 code3 language }
  image { url }
  contributions(order_by: {id: asc}) {
    contribution
    author { id name image { url } }
  }
}`

// bookFields is the full book selection: identity, dates, tags, contributors,
// series, and the editions the plugin picks publisher and ISBN from.
const bookFields = `
fragment BookFields on books {
  id
  slug
  title
  subtitle
  description
  release_date
  release_year
  pages
  audio_seconds
  compilation
  rating
  ratings_count
  alternative_titles
  cached_tags
  image { url }
  contributions(order_by: {id: asc}) {
    contribution
    author { id name image { url } }
  }
  book_series(order_by: [{featured: desc}, {position: asc_nulls_last}]) {
    featured
    position
    series { id name }
  }
  default_ebook_edition { ...EditionFields }
  default_physical_edition { ...EditionFields }
  default_audio_edition { ...EditionFields }
  default_cover_edition { ...EditionFields }
  editions(limit: 25, order_by: [{users_count: desc_nulls_last}]) { ...EditionFields }
}` + editionFields

// searchQuery uses Hardcover's Typesense-backed search endpoint. Hasura
// pattern operators such as `_ilike` are disabled for API tokens, so this is
// the only usable text search.
const searchQuery = `query SiloSearchBooks($query: String!, $perPage: Int!) {
  search(query: $query, query_type: "Book", per_page: $perPage, page: 1) {
    ids
    results
  }
}`

// bookByIDQuery resolves a Hardcover book ID.
const bookByIDQuery = `query SiloBookByID($id: Int!) {
  books_by_pk(id: $id) { ...BookFields }
}` + bookFields

// bookBySlugQuery resolves a hardcover.app/books/<slug> identifier.
const bookBySlugQuery = `query SiloBookBySlug($slug: String!) {
  books(where: {slug: {_eq: $slug}}, limit: 1) { ...BookFields }
}` + bookFields

// bookByISBNQuery resolves either ISBN form through the book's editions.
const bookByISBNQuery = `query SiloBookByISBN($isbn: String!) {
  books(
    where: {editions: {_or: [{isbn_13: {_eq: $isbn}}, {isbn_10: {_eq: $isbn}}]}}
    order_by: [{users_count: desc_nulls_last}]
    limit: 1
  ) { ...BookFields }
}` + bookFields

// bookByASINQuery resolves an Amazon/Audible identifier through editions.
const bookByASINQuery = `query SiloBookByASIN($asin: String!) {
  books(
    where: {editions: {asin: {_eq: $asin}}}
    order_by: [{users_count: desc_nulls_last}]
    limit: 1
  ) { ...BookFields }
}` + bookFields

// booksByIDsQuery hydrates the search hits that came back without enough
// detail to build a full record.
const booksByIDsQuery = `query SiloBooksByIDs($ids: [Int!]!) {
  books(where: {id: {_in: $ids}}) { ...BookFields }
}` + bookFields
