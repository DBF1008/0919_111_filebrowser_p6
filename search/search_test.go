package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

type allowAllChecker struct{}

func (allowAllChecker) Check(string) bool { return true }

func collectResults(t *testing.T, fs afero.Fs, scope, query string) []string {
	t.Helper()
	results := []string{}
	err := Search(context.Background(), fs, scope, query, allowAllChecker{}, func(path string, _ os.FileInfo) error {
		results = append(results, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Search(%q) returned error: %v", query, err)
	}
	sort.Strings(results)
	return results
}

func newTestFs(t *testing.T, files map[string]string) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	for name, content := range files {
		if err := afero.WriteFile(fs, name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return fs
}

func TestParseSearchLimitOffset(t *testing.T) {
	opts := parseSearch("foo limit:10 offset:20")
	if opts.Limit != 10 {
		t.Errorf("expected Limit=10, got %d", opts.Limit)
	}
	if opts.Offset != 20 {
		t.Errorf("expected Offset=20, got %d", opts.Offset)
	}
	if len(opts.Terms) != 1 || opts.Terms[0] != "foo" {
		t.Errorf("expected Terms=[foo], got %v", opts.Terms)
	}
}

func TestSearchByFileName(t *testing.T) {
	fs := newTestFs(t, map[string]string{
		"/scope/report-final.txt": "nothing here",
		"/scope/notes.md":         "nothing here either",
	})

	results := collectResults(t, fs, "/scope", "report")
	if len(results) != 1 || results[0] != "report-final.txt" {
		t.Fatalf("expected [report-final.txt], got %v", results)
	}
}

func TestSearchByContent(t *testing.T) {
	fs := newTestFs(t, map[string]string{
		"/scope/a.txt":     "the quick brown fox",
		"/scope/b.txt":     "nothing relevant",
		"/scope/sub/c.txt": "foxes are cute",
	})

	results := collectResults(t, fs, "/scope", "fox")
	if len(results) != 2 || results[0] != "a.txt" || results[1] != "sub/c.txt" {
		t.Fatalf("expected [a.txt sub/c.txt], got %v", results)
	}
}

func TestSearchContentCaseInsensitive(t *testing.T) {
	fs := newTestFs(t, map[string]string{
		"/scope/a.txt": "Hello World",
	})

	results := collectResults(t, fs, "/scope", "hello")
	if len(results) != 1 || results[0] != "a.txt" {
		t.Fatalf("expected [a.txt], got %v", results)
	}
}

func TestSearchContentCaseSensitive(t *testing.T) {
	fs := newTestFs(t, map[string]string{
		"/scope/a.txt": "Hello World",
	})

	results := collectResults(t, fs, "/scope", "case:sensitive hello")
	if len(results) != 0 {
		t.Fatalf("expected no results, got %v", results)
	}

	results = collectResults(t, fs, "/scope", "case:sensitive Hello")
	if len(results) != 1 || results[0] != "a.txt" {
		t.Fatalf("expected [a.txt], got %v", results)
	}
}

func TestSearchContentSkipsBinaryFiles(t *testing.T) {
	fs := afero.NewMemMapFs()
	binary := append([]byte("needle"), 0x00, 0x01, 0x02)
	binary = append(binary, []byte("needle")...)
	if err := afero.WriteFile(fs, "/scope/bin.dat", binary, 0o644); err != nil {
		t.Fatal(err)
	}

	results := collectResults(t, fs, "/scope", "needle")
	if len(results) != 0 {
		t.Fatalf("expected binary file to be skipped, got %v", results)
	}
}

func TestSearchContentReadSizeLimit(t *testing.T) {
	fs := afero.NewMemMapFs()
	// The term sits beyond maxContentReadSize and must not be found.
	content := strings.Repeat("a", maxContentReadSize) + "needle"
	if err := afero.WriteFile(fs, "/scope/big.txt", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	results := collectResults(t, fs, "/scope", "needle")
	if len(results) != 0 {
		t.Fatalf("expected no results beyond the read cap, got %v", results)
	}

	// A term within the first maxContentReadSize bytes is found.
	content = "needle" + strings.Repeat("a", maxContentReadSize)
	if err := afero.WriteFile(fs, "/scope/big.txt", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	results = collectResults(t, fs, "/scope", "needle")
	if len(results) != 1 || results[0] != "big.txt" {
		t.Fatalf("expected [big.txt], got %v", results)
	}
}

func TestSearchLimit(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 10; i++ {
		files[fmt.Sprintf("/scope/match-%d.txt", i)] = "x"
	}
	fs := newTestFs(t, files)

	results := collectResults(t, fs, "/scope", "match limit:3")
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d: %v", len(results), results)
	}
}

func TestSearchOffset(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 5; i++ {
		files[fmt.Sprintf("/scope/match-%d.txt", i)] = "x"
	}
	fs := newTestFs(t, files)

	results := collectResults(t, fs, "/scope", "match offset:3")
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(results), results)
	}
}

func TestSearchLimitAndOffset(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 10; i++ {
		files[fmt.Sprintf("/scope/match-%02d.txt", i)] = "x"
	}
	fs := newTestFs(t, files)

	// Results are delivered in lexical order; offset skips the first
	// matches and limit caps the page size.
	results := collectResults(t, fs, "/scope", "match limit:2 offset:3")
	if len(results) != 2 || results[0] != "match-03.txt" || results[1] != "match-04.txt" {
		t.Fatalf("expected [match-03.txt match-04.txt], got %v", results)
	}
}

func TestSearchContextCancelledBeforeStart(t *testing.T) {
	fs := newTestFs(t, map[string]string{"/scope/a.txt": "x"})
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(context.Canceled)

	err := Search(ctx, fs, "/scope", "a", allowAllChecker{}, func(string, os.FileInfo) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestSearchStopsWhenContextCancelled(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 100; i++ {
		files[fmt.Sprintf("/scope/match-%03d.txt", i)] = "x"
	}
	fs := newTestFs(t, files)

	ctx, cancel := context.WithCancelCause(context.Background())
	calls := 0
	err := Search(ctx, fs, "/scope", "match", allowAllChecker{}, func(string, os.FileInfo) error {
		calls++
		cancel(errors.New("client disconnected"))
		return context.Cause(ctx)
	})
	if err == nil || err.Error() != "client disconnected" {
		t.Fatalf("expected cancellation cause, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected traversal to stop after 1 result, got %d calls", calls)
	}
}
