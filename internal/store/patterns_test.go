package store

import "testing"

// A user-supplied term must never be able to widen or reshape the LIKE pattern.
// Without escaping, "%" matches everything and "_" becomes a one-character probe.
func TestEscapeLikeNeutralizesWildcards(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"%", "!%"},
		{"_", "!_"},
		{"a%b", "a!%b"},
		{"a_b", "a!_b"},
		{"!", "!!"},
		{"100%", "100!%"},
		// Underscores are common in Kubernetes names, so they must be escaped
		// too: searching "web_01" must not match "web-01".
		{"prod_web-01", "prod!_web-01"},
		{"web-01", "web-01"},
		{"正常事件", "正常事件"},
	}
	for _, item := range cases {
		if got := escapeLike(item.input); got != item.want {
			t.Fatalf("escapeLike(%q) = %q, want %q", item.input, got, item.want)
		}
	}
}

func TestLikeContainsClauseEscapesAndBindsOnce(t *testing.T) {
	columns := []string{"reason", "message"}
	clause := likeContainsClause(columns)
	// Every column must declare the same ESCAPE character, otherwise SQLite
	// treats a trailing backslash as an incomplete escape sequence.
	if want := "(reason LIKE ? ESCAPE '!' OR message LIKE ? ESCAPE '!')"; clause != want {
		t.Fatalf("clause = %q, want %q", clause, want)
	}
	args := likeContainsArgs(columns, "100%")
	if len(args) != len(columns) {
		t.Fatalf("args = %d, want %d", len(args), len(columns))
	}
	if args[0] != "%100!%%" {
		t.Fatalf("pattern = %q, want %q", args[0], "%100!%%")
	}
	// Both columns share one pattern so the binding stays aligned with the clause.
	if args[0] != args[1] {
		t.Fatal("columns must share the same bound pattern")
	}
}

func TestClampEventSearchRejectsUnboundedTerm(t *testing.T) {
	if !clampEventSearch("") {
		t.Fatal("empty search should be allowed")
	}
	if !clampEventSearch("BackOff") {
		t.Fatal("normal term should be allowed")
	}
	if clampEventSearch(string(make([]rune, maxEventSearchChars+1))) {
		t.Fatal("oversized term should be rejected instead of truncated")
	}
}
