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

// maxContentSearchSize caps how many bytes of a single file are read into
// memory while searching its contents. It prevents out-of-memory crashes
// when the search walks into very large files.
var maxContentSearchSize int64 = 1 << 20 // 1 MB

// errLimitReached is a sentinel used to abort the walk early once the
// requested number of results has been emitted.
var errLimitReached = errors.New("search: result limit reached")

type searchOptions struct {
	CaseSensitive bool
	Conditions    []condition
	Terms         []string
	Limit         int
	Offset        int
}

// Search searches for a query in a fs.
//
// Besides matching file names, the contents of regular (non-binary) files
// are also scanned for the search terms. At most maxContentSearchSize bytes
// are read per file to bound memory usage.
//
// The traversal is interruptible: it stops as soon as ctx is canceled and
// returns the context cause. limit caps the number of emitted results
// (<= 0 means no limit) and offset skips the first matches, allowing
// clients to paginate instead of receiving unbounded result sets.
func Search(ctx context.Context,
	fs afero.Fs, scope, query string, limit, offset int, checker rules.Checker, found func(path string, f os.FileInfo) error) error {
	search := parseSearch(query)
	if limit > 0 {
		search.Limit = limit
	}
	if offset > 0 {
		search.Offset = offset
	}

	scope = filepath.ToSlash(filepath.Clean(scope))
	scope = path.Join("/", scope)

	matched := 0
	emitted := 0
	emit := func(path string, f os.FileInfo) error {
		matched++
		if matched <= search.Offset {
			return nil
		}
		if err := found(path, f); err != nil {
			return err
		}
		emitted++
		if search.Limit > 0 && emitted >= search.Limit {
			return errLimitReached
		}
		return nil
	}

	err := walk(ctx, fs, scope, func(fPath string, f os.FileInfo, _ error) error {
		osPath := fPath
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
					term = strings.ToLower(term)
				}
				if strings.Contains(fileName, term) {
					return emit(relativePath, f)
				}
				if contentMatches(fs, osPath, f, term, search.CaseSensitive) {
					return emit(relativePath, f)
				}
			}
			return nil
		}

		return emit(relativePath, f)
	})
	if errors.Is(err, errLimitReached) {
		return nil
	}
	return err
}

// contentMatches reports whether the contents of the regular file at fPath
// contain term. Binary files and files that cannot be read are skipped, and
// at most maxContentSearchSize bytes are read to bound memory usage.
func contentMatches(fs afero.Fs, fPath string, f os.FileInfo, term string, caseSensitive bool) bool {
	if f == nil || !f.Mode().IsRegular() {
		return false
	}

	file, err := fs.Open(fPath)
	if err != nil {
		return false
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxContentSearchSize))
	if err != nil || len(data) == 0 {
		return false
	}

	// Skip binary files: a NUL byte is a reliable indicator of non-text data.
	if bytes.IndexByte(data, 0) >= 0 {
		return false
	}

	content := string(data)
	if !caseSensitive {
		content = strings.ToLower(content)
		term = strings.ToLower(term)
	}
	return strings.Contains(content, term)
}

// walk is an interruptible replacement for afero.Walk. Unlike afero.Walk,
// which never looks at the context, walk checks ctx before visiting every
// entry and before reading every directory, so a canceled context (e.g. an
// aborted SSE client) stops the traversal immediately.
func walk(ctx context.Context, fs afero.Fs, root string, walkFn filepath.WalkFunc) error {
	info, err := lstatIfPossible(fs, root)
	if err != nil {
		return walkFn(root, nil, err)
	}
	return walkEntry(ctx, fs, root, info, walkFn)
}

func walkEntry(ctx context.Context, fs afero.Fs, name string, info os.FileInfo, walkFn filepath.WalkFunc) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}

	err := walkFn(name, info, nil)
	if !info.IsDir() {
		return err
	}
	if err == filepath.SkipDir {
		return nil
	}
	if err != nil {
		return err
	}

	names, err := readDirNames(fs, name)
	if err != nil {
		return walkFn(name, info, err)
	}

	for _, entryName := range names {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		childPath := filepath.Join(name, entryName)
		childInfo, err := lstatIfPossible(fs, childPath)
		if err != nil {
			if err := walkFn(childPath, nil, err); err != nil {
				return err
			}
			continue
		}
		if err := walkEntry(ctx, fs, childPath, childInfo, walkFn); err != nil {
			if err == filepath.SkipDir {
				return nil
			}
			return err
		}
	}
	return nil
}

func readDirNames(fs afero.Fs, dirname string) ([]string, error) {
	f, err := fs.Open(dirname)
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

func lstatIfPossible(fs afero.Fs, name string) (os.FileInfo, error) {
	if lstater, ok := fs.(afero.Lstater); ok {
		info, _, err := lstater.LstatIfPossible(name)
		return info, err
	}
	return fs.Stat(name)
}
