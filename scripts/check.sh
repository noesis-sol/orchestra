#!/bin/sh
# The project's full check: vet, the race tests and the linters in .golangci.yml. Orchestra runs it
# before merging a rebased ticket (.orchestra/settings.json), and workers run it before closing one.
# golangci-lint runs through go run at a pinned version, so a machine needs only Go.
set -e
go vet ./...
go test -race ./...
# Built with the project's own Go: golangci-lint refuses to check code that targets a newer Go than it
# was built with, and go run would otherwise build it with the older Go its own module asks for.
GOTOOLCHAIN="$(go env GOVERSION)" go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
