package search

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/rules"
)

// maxContentReadSize caps how many bytes of a single file are read when
// matching search terms against its contents. This prevents excessive
// memory usage when searching inside very large files.
const maxContentReadSize = 1 << 20 // 1 MiB

// errLimitReached is a sentinel error used to abort the traversal as soon
// as the configured result limit has been delivered.
var errLimitReached = errors.New("search: result limit reached")

type searchOptions struct {
	CaseSensitive bool
	Conditions    []condition
	Terms         []string
	Limit         int
	Offset        int
}

// Search searches for a query in a fs.
func Search(ctx context.Context,
	fs afero.Fs, scope, query string, checker rules.Checker, found func(path string, f os.FileInfo) error) error {
	search := parseSearch(query)

	scope = filepath.ToSlash(filepath.Clean(scope))
	scope = path.Join("/", scope)

	matches := 0
	sent := 0

	// deliver applies the offset/limit pagination to every match before
	// forwarding it to the caller.
	deliver := func(relativePath string, f os.FileInfo) error {
		matches++
		if matches <= search.Offset {
			return nil
		}
		if search.Limit > 0 && sent >= search.Limit {
			return errLimitReached
		}
		sent++
		return found(relativePath, f)
	}

	err := walk(ctx, fs, scope, func(fPath string, f os.FileInfo) error {
		fPath = filepath.ToSlash(filepath.Clean(fPath))
		fPath = path.Join("/", fPath)
		relativePath := strings.TrimPrefix(fPath, scope)
		relativePath = strings.TrimPrefix(relativePath, "/")

		if fPath == scope {
			return nil
		}

		if !checker.Check(fPath) {
			return nil
		}

		if len(search.Conditions) > 0 {
			match := false

			for _, t := range search.Conditions {
				if t(fPath) {
					match = true
					break
				}
			}

			if !match {
				return nil
			}
		}

		if len(search.Terms) > 0 {
			for _, term := range search.Terms {
				_, fileName := path.Split(fPath)
				if !search.CaseSensitive {
					fileName = strings.ToLower(fileName)
				}
				if strings.Contains(fileName, term) {
					return deliver(relativePath, f)
				}
			}

			// The file name did not match: fall back to searching the
			// contents of text files.
			if matchContent(fs, fPath, f, search) {
				return deliver(relativePath, f)
			}
			return nil
		}

		return deliver(relativePath, f)
	})
	if errors.Is(err, errLimitReached) {
		return nil
	}
	return err
}

// matchContent reports whether any of the search terms occurs in the
// contents of the file at fPath. Only regular files are inspected and at
// most maxContentReadSize bytes are read, so memory usage stays bounded
// regardless of the file size. Binary files are skipped.
func matchContent(fs afero.Fs, fPath string, f os.FileInfo, opts *searchOptions) bool {
	if f == nil || f.IsDir() || !f.Mode().IsRegular() {
		return false
	}

	file, err := fs.Open(fPath)
	if err != nil {
		return false
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, maxContentReadSize))
	if err != nil {
		return false
	}

	// A NUL byte is a strong indicator of a binary file; skip those.
	if bytes.IndexByte(content, 0) >= 0 {
		return false
	}

	if !opts.CaseSensitive {
		content = bytes.ToLower(content)
	}

	for _, term := range opts.Terms {
		if term == "" {
			continue
		}
		if bytes.Contains(content, []byte(term)) {
			return true
		}
	}
	return false
}

// walk is an interruptible version of afero.Walk: the context is checked
// before visiting every entry and before reading every directory, so a
// cancelled context stops the traversal promptly instead of walking the
// whole tree. Like afero.Walk, entries are visited in lexical order and
// symbolic links are not followed.
func walk(ctx context.Context, fs afero.Fs, root string, walkFn func(path string, info os.FileInfo) error) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	info, err := lstatIfPossible(fs, root)
	if err != nil {
		return err
	}
	return walkEntry(ctx, fs, root, info, walkFn)
}

func walkEntry(ctx context.Context, fs afero.Fs, fPath string, info os.FileInfo, walkFn func(path string, info os.FileInfo) error) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}

	if err := walkFn(fPath, info); err != nil {
		return err
	}

	if !info.IsDir() {
		return nil
	}

	names, err := readDirNames(fs, fPath)
	if err != nil {
		return err
	}

	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		childPath := filepath.Join(fPath, name)
		childInfo, err := lstatIfPossible(fs, childPath)
		if err != nil {
			continue
		}
		if err := walkEntry(ctx, fs, childPath, childInfo, walkFn); err != nil {
			return err
		}
	}
	return nil
}

// lstatIfPossible uses Lstat when the filesystem supports it, so symbolic
// links are not followed, and falls back to Stat otherwise.
func lstatIfPossible(fs afero.Fs, fPath string) (os.FileInfo, error) {
	if lfs, ok := fs.(afero.Lstater); ok {
		fi, _, err := lfs.LstatIfPossible(fPath)
		return fi, err
	}
	return fs.Stat(fPath)
}

// readDirNames reads the directory named by dirname and returns a sorted
// list of directory entries.
func readDirNames(fs afero.Fs, dirname string) ([]string, error) {
	f, err := fs.Open(dirname)
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}
