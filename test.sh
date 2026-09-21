#!/bin/sh
# Manual unit test script for the search feature changes:
#   - search/search.go: content search, interruptible walk, limit/offset
#   - http/search.go:   SSE handler passes limit/offset
#   - files/file.go:    TruncateResults pagination helper
set -e

echo "==> go build ./..."
go build ./...

echo "==> go vet ./search/... ./files/... ./http/..."
go vet ./search/... ./files/... ./http/...

echo "==> go test ./search/... -v"
go test ./search/... -v

echo "==> go test ./files/... -run TestTruncateResults -v"
go test ./files/... -run TestTruncateResults -v

echo "==> go test ./http/..."
go test ./http/...

echo "All tests passed."
