# Contributing to the Hardcover Metadata Plugin

The [Silo contribution guide](https://github.com/Silo-Server/.github/blob/main/CONTRIBUTING.md)
covers project-wide coordination, focused changes, evidence, AI disclosure, and
pull request expectations. Those requirements apply here; this guide adds the
plugin-specific workflow.

## Before you start

Open an [issue](https://github.com/totza2010/silo-plugin-metadata-hardcover/issues)
before changing the GraphQL queries, edition selection, identifier routing,
configuration, or the advertised capability. This repository owns Hardcover
provider behavior; plugin contracts belong in
[`silo-plugin-sdk`](https://github.com/Silo-Server/silo-plugin-sdk), and host
metadata orchestration belongs in
[`silo-server`](https://github.com/Silo-Server/silo-server).

This plugin deliberately has one source. Proposals to add a second provider
belong in [`silo-plugin-metadata-ebook`](https://github.com/Silo-Server/silo-plugin-metadata-ebook),
which is the multi-source plugin.

## Development setup

Use the Go version declared in `go.mod`. A local `go.work` may point at a
sibling SDK checkout while developing both repositories, but committed code and
CI must resolve the tagged SDK dependency with `GOWORK=off`. Never commit an API
token or a local filesystem `replace` directive.

Tests run entirely against `httptest` fixtures in `hardcover/testdata`; no test
reaches Hardcover.

## Validate your change

```sh
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
GOWORK=off go mod tidy -diff
GOWORK=off go build ./...
gofmt -l .
```

`gofmt -l .` should print nothing. Add focused coverage when you change response
parsing, edition selection, identifier routing, rate limiting, or error
handling.

## Checking a query against the real API

Hardcover's schema is in beta and changes. Before changing a query document,
run it in the [GraphQL console](https://cloud.hasura.io/public/graphiql?endpoint=https://api.hardcover.app/v1/graphql)
with your own token and paste the result shape into the pull request. Update
the fixtures in `hardcover/testdata` to match what the API actually returned.

## Open the pull request

Use a Conventional Commit title, explain any matching or upstream-service risk,
and paste the actual validation results. Read the
[AI-assisted contribution policy](https://github.com/Silo-Server/silo-server/blob/main/docs/ai-contributions.md)
and include its disclosure block.
