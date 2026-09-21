package fbhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/filebrowser/filebrowser/v2/search"
)

const searchPingInterval = 5

var searchHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	response := make(chan map[string]interface{})
	ctx, cancel := context.WithCancelCause(r.Context())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Avoid connection timeout
		timeout := time.NewTimer(searchPingInterval * time.Second)
		defer timeout.Stop()
		for {
			var err error
			var infoBytes []byte
			select {
			case info := <-response:
				if info == nil {
					return
				}
				infoBytes, err = json.Marshal(info)
			case <-timeout.C:
				// Send a heartbeat packet
				infoBytes = nil
			case <-ctx.Done():
				return
			}
			if err != nil {
				cancel(err)
				return
			}
			_, err = w.Write(infoBytes)
			if err == nil {
				_, err = w.Write([]byte("\n"))
			}
			if err != nil {
				cancel(err)
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}()
	query := searchQuery(r)

	err := search.Search(ctx, d.user.Fs, r.URL.Path, query, d, func(path string, f os.FileInfo) error {
		select {
		case <-ctx.Done():
		case response <- map[string]interface{}{
			"dir":  f.IsDir(),
			"path": path,
		}:
		}
		return context.Cause(ctx)
	})
	close(response)
	wg.Wait()
	if err == nil {
		err = context.Cause(ctx)
	}
	// ignore cancellation errors from user aborts
	if err != nil && !errors.Is(err, context.Canceled) {
		return http.StatusInternalServerError, err
	}

	return 0, nil
})

// searchQuery builds the search query string from the request, appending
// the optional limit and offset pagination parameters so large result
// sets can be truncated and paged instead of overwhelming the frontend.
func searchQuery(r *http.Request) string {
	query := r.URL.Query().Get("query")

	if raw := r.URL.Query().Get("limit"); raw != "" {
		if limit, err := strconv.Atoi(raw); err == nil && limit > 0 {
			query = fmt.Sprintf("%s limit:%d", query, limit)
		}
	}

	if raw := r.URL.Query().Get("offset"); raw != "" {
		if offset, err := strconv.Atoi(raw); err == nil && offset >= 0 {
			query = fmt.Sprintf("%s offset:%d", query, offset)
		}
	}

	return strings.TrimSpace(query)
}
