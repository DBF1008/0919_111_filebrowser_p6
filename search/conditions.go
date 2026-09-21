package search

import (
	"mime"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	typeRegexp   = regexp.MustCompile(`type:(\w+)`)
	limitRegexp  = regexp.MustCompile(`limit:(\d+)`)
	offsetRegexp = regexp.MustCompile(`offset:(\d+)`)
)

type condition func(path string) bool

func extensionCondition(extension string) condition {
	return func(path string) bool {
		return filepath.Ext(path) == "."+extension
	}
}

func imageCondition(path string) bool {
	extension := filepath.Ext(path)
	mimetype := mime.TypeByExtension(extension)

	return strings.HasPrefix(mimetype, "image")
}

func audioCondition(path string) bool {
	extension := filepath.Ext(path)
	mimetype := mime.TypeByExtension(extension)

	return strings.HasPrefix(mimetype, "audio")
}

func videoCondition(path string) bool {
	extension := filepath.Ext(path)
	mimetype := mime.TypeByExtension(extension)

	return strings.HasPrefix(mimetype, "video")
}

func parseSearch(value string) *searchOptions {
	opts := &searchOptions{
		CaseSensitive: strings.Contains(value, "case:sensitive"),
		Conditions:    []condition{},
		Terms:         []string{},
	}

	// removes the options from the value
	value = strings.ReplaceAll(value, "case:insensitive", "")
	value = strings.ReplaceAll(value, "case:sensitive", "")
	value = strings.TrimSpace(value)

	types := typeRegexp.FindAllStringSubmatch(value, -1)
	for _, t := range types {
		if len(t) == 1 {
			continue
		}

		switch t[1] {
		case "image":
			opts.Conditions = append(opts.Conditions, imageCondition)
		case "audio", "music":
			opts.Conditions = append(opts.Conditions, audioCondition)
		case "video":
			opts.Conditions = append(opts.Conditions, videoCondition)
		default:
			opts.Conditions = append(opts.Conditions, extensionCondition(t[1]))
		}
	}

	if len(types) > 0 {
		// Remove the fields from the search value.
		value = typeRegexp.ReplaceAllString(value, "")
	}

	// Extract the pagination options, if any.
	if m := limitRegexp.FindStringSubmatch(value); len(m) == 2 {
		if limit, err := strconv.Atoi(m[1]); err == nil && limit > 0 {
			opts.Limit = limit
		}
		value = limitRegexp.ReplaceAllString(value, "")
	}
	if m := offsetRegexp.FindStringSubmatch(value); len(m) == 2 {
		if offset, err := strconv.Atoi(m[1]); err == nil && offset >= 0 {
			opts.Offset = offset
		}
		value = offsetRegexp.ReplaceAllString(value, "")
	}

	// If it's case insensitive, put everything in lowercase.
	if !opts.CaseSensitive {
		value = strings.ToLower(value)
	}

	// Remove the spaces from the search value.
	value = strings.TrimSpace(value)

	if value == "" {
		return opts
	}

	// if the value starts with " and finishes what that character, we will
	// only search for that term
	if value[0] == '"' && value[len(value)-1] == '"' {
		unique := strings.TrimPrefix(value, "\"")
		unique = strings.TrimSuffix(unique, "\"")

		opts.Terms = []string{unique}
		return opts
	}

	opts.Terms = strings.Split(value, " ")
	return opts
}
