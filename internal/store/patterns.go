package store

import "strings"

// likeEscapeChar is the explicit ESCAPE character used with LIKE patterns.
// SQLite and MySQL both accept ESCAPE '<char>', so every pattern built from
// user input must declare it; otherwise a trailing backslash is undefined.
const likeEscapeChar = "!"

// escapeLike escapes the LIKE metacharacters so a user-supplied term is matched
// literally. Without this, "%" degrades into "match everything" and "_" turns
// into a single-character probe, which both widens the scan and allows probing
// for strings that should not be discoverable.
//
// It must be paired with `ESCAPE '<char>'` on the SQL side; the escape character
// itself is escaped first so it cannot break out of the literal.
func escapeLike(value string) string {
	replacer := strings.NewReplacer(
		string(likeEscapeChar), string(likeEscapeChar)+string(likeEscapeChar),
		"%", string(likeEscapeChar)+"%",
		"_", string(likeEscapeChar)+"_",
		"\\", string(likeEscapeChar)+"\\",
	)
	return replacer.Replace(value)
}

// likeContainsClause builds a case-insensitive "contains" condition over the
// given columns, e.g. `(reason LIKE ? ESCAPE '!' OR message LIKE ? ESCAPE '!')`.
func likeContainsClause(columns []string) string {
	if len(columns) == 0 {
		return ""
	}
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		parts = append(parts, column+" LIKE ? ESCAPE '"+string(likeEscapeChar)+"'")
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

// likeContainsArgs returns one binding per column, all bound to the same
// escaped "%term%" pattern.
func likeContainsArgs(columns []string, term string) []any {
	pattern := "%" + escapeLike(term) + "%"
	args := make([]any, 0, len(columns))
	for range columns {
		args = append(args, pattern)
	}
	return args
}

// maxEventSearchChars bounds a user-supplied event search term. The value is
// bound into a LIKE pattern repeated across several columns, so an unbounded
// term both widens the scan and enlarges the argument list.
const maxEventSearchChars = 128

// clampEventSearch reports whether the term is within the accepted range.
// Callers reject with CodeSearchQueryInvalid instead of silently truncating,
// so the user is not shown results for a different query than the one typed.
func clampEventSearch(term string) bool {
	return len([]rune(strings.TrimSpace(term))) <= maxEventSearchChars
}
