package fbhttp

import (
	"net/http/httptest"
	"testing"
)

func TestSearchQuery(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		expected string
	}{
		{
			name:     "plain query without pagination",
			target:   "/api/search/?query=foo",
			expected: "foo",
		},
		{
			name:     "limit and offset are appended",
			target:   "/api/search/?query=foo&limit=50&offset=10",
			expected: "foo limit:50 offset:10",
		},
		{
			name:     "invalid limit is ignored",
			target:   "/api/search/?query=foo&limit=abc&offset=-3",
			expected: "foo",
		},
		{
			name:     "zero limit is ignored",
			target:   "/api/search/?query=foo&limit=0",
			expected: "foo",
		},
		{
			name:     "pagination works with an empty query",
			target:   "/api/search/?limit=25",
			expected: "limit:25",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tt.target, nil)
			if got := searchQuery(r); got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}
