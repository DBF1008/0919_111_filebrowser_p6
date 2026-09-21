package search

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

type allowAllChecker struct{}

func (allowAllChecker) Check(string) bool { return true }

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

func collect(t *testing.T, fs afero.Fs, query string, limit, offset int) ([]string, error) {
	t.Helper()
	var results []string
	err := Search(context.Background(), fs, "/", query, limit, offset, allowAllChecker{},
		func(path string, _ os.FileInfo) error {
			results = append(results, path)
			return nil
		})
	sort.Strings(results)
	return results, err
}

func TestSearchByFileName(t *testing.T) {
	fs := newTestFs(t, map[string]string{
		"/docs/report.txt": "nothing relevant",
		"/docs/other.txt":  "nothing relevant",
	})

	results, err := collect(t, fs, "report", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != "docs/report.txt" {
		t.Fatalf("expected [docs/report.txt], got %v", results)
	}
}

func TestSearchByContent(t *testing.T) {
	fs := newTestFs(t, map[string]string{
		"/alpha.txt":      "the quick brown fox",
		"/beta.txt":       "unrelated text",
		"/dir/gamma.md":   "another fox in a nested dir",
		"/delta.txt":      "",
		"/dir/epsilon.go": "package main // fox",
	})

	results, err := collect(t, fs, "fox", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha.txt", "dir/epsilon.go", "dir/gamma.md"}
	if strings.Join(results, ",") != strings.Join(want, ",") {
		t.Fatalf("expected %v, got %v", want, results)
	}
}

func TestSearchContentCaseInsensitive(t *testing.T) {
	fs := newTestFs(t, map[string]string{
		"/a.txt": "Hello World",
	})

	results, err := collect(t, fs, "hello", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != "a.txt" {
		t.Fatalf("expected [a.txt], got %v", results)
	}
}

func TestSearchContentCaseSensitive(t *testing.T) {
	fs := newTestFs(t, map[string]string{
		"/a.txt": "Hello World",
	})

	results, err := collect(t, fs, "case:sensitive hello", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %v", results)
	}

	results, err = collect(t, fs, "case:sensitive Hello", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != "a.txt" {
		t.Fatalf("expected [a.txt], got %v", results)
	}
}

func TestSearchSkipsBinaryFiles(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/bin.dat", []byte{'f', 'o', 'x', 0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := collect(t, fs, "fox", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results for binary content, got %v", results)
	}
}

func TestSearchContentReadSizeIsCapped(t *testing.T) {
	old := maxContentSearchSize
	maxContentSearchSize = 16
	defer func() { maxContentSearchSize = old }()

	fs := newTestFs(t, map[string]string{
		// The keyword sits beyond the read cap and must not be found.
		"/big.txt": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa needle",
		// The keyword is inside the first bytes and must be found.
		"/small.txt": "needle aaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})

	results, err := collect(t, fs, "needle", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != "small.txt" {
		t.Fatalf("expected [small.txt], got %v", results)
	}
}

func TestSearchLimit(t *testing.T) {
	fs := newTestFs(t, map[string]string{
		"/a.txt": "fox",
		"/b.txt": "fox",
		"/c.txt": "fox",
	})

	results, err := collect(t, fs, "fox", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %v", results)
	}
}

func TestSearchOffset(t *testing.T) {
	fs := newTestFs(t, map[string]string{
		"/a.txt": "fox",
		"/b.txt": "fox",
		"/c.txt": "fox",
	})

	all, err := collect(t, fs, "fox", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 results, got %v", all)
	}

	paged, err := collect(t, fs, "fox", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(paged) != 1 || paged[0] != all[1] {
		t.Fatalf("expected [%s], got %v", all[1], paged)
	}
}

func TestSearchInterruptibleWalk(t *testing.T) {
	fs := newTestFs(t, map[string]string{
		"/a.txt": "fox",
		"/b.txt": "fox",
		"/c.txt": "fox",
	})

	t.Run("canceled before start", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := Search(ctx, fs, "/", "fox", 0, 0, allowAllChecker{},
			func(string, os.FileInfo) error { return nil })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("canceled during walk", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0

		err := Search(ctx, fs, "/", "fox", 0, 0, allowAllChecker{},
			func(string, os.FileInfo) error {
				calls++
				cancel()
				return nil
			})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if calls > 1 {
			t.Fatalf("walk did not stop after cancellation, found called %d times", calls)
		}
	})

	t.Run("cancel cause is propagated", func(t *testing.T) {
		cause := errors.New("client disconnected")
		ctx, cancel := context.WithCancelCause(context.Background())

		err := Search(ctx, fs, "/", "fox", 0, 0, allowAllChecker{},
			func(string, os.FileInfo) error {
				cancel(cause)
				return nil
			})
		if !errors.Is(err, cause) {
			t.Fatalf("expected %v, got %v", cause, err)
		}
	})
}
