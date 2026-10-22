package httpapi

import (
	"strings"
	"testing"

	"example.com/k8s-platform/internal/auth"
)

func TestPrivilegedRoutesAreDeclaredForStateChangingAPIs(t *testing.T) {
	registered := []string{
		"GET /api/v1/clusters",
		"GET /api/v1/clusters/{id}/pods/{namespace}/{name}/logs",
		"POST /api/v1/clusters",
		"POST /api/v1/clusters/validate",
		"DELETE /api/v1/clusters/{id}",
		"PATCH /api/v1/clusters/{id}/classification",
		"PUT /api/v1/capacity/classification",
		"POST /api/v1/clusters/{id}/capacity/baselines",
		"POST /api/v1/clusters/{id}/capacity/baseline-decisions",
		"POST /api/v1/clusters/{id}/security-baseline/scan",
		"POST /api/v1/clusters/{id}/security-baseline/whitelist",
		"DELETE /api/v1/clusters/{id}/security-baseline/whitelist/{controlID}",
	}
	missing, err := UndeclaredStateChangingRoutes(registered)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("state-changing routes without a rule: %v", missing)
	}
}

// A newly added non-GET route must fail the startup check instead of silently
// inheriting a weaker or absent decision.
func TestUndeclaredStateChangingRouteIsReported(t *testing.T) {
	missing, err := UndeclaredStateChangingRoutes([]string{"POST /api/v1/clusters/{id}/new-dangerous-action"})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || !strings.Contains(missing[0], "new-dangerous-action") {
		t.Fatalf("expected the new route to be reported, got %v", missing)
	}
}

func TestRouteAccessLookupRules(t *testing.T) {
	access, err := RouteAccess(nil)
	if err != nil {
		t.Fatal(err)
	}
	execPattern := "GET /api/v1/clusters/{id}/pods/{namespace}/{name}/exec"
	rule, ok := access.Lookup(execPattern)
	if !ok {
		t.Fatal("exec route must be declared")
	}
	if rule.MinRole != "operator" || !rule.WebSocket {
		t.Fatalf("unexpected exec rule: %#v", rule)
	}
	// Arthas reaches into the container filesystem, so it is admin-only even
	// though plain exec is not.
	if rule.AdminOnlyModeQuery != "mode" || rule.AdminOnlyModeValue != "arthas" {
		t.Fatalf("exec rule must raise the bar for arthas mode: %#v", rule)
	}
	kubectl, ok := access.Lookup("POST /api/v1/clusters/{id}/kubectl")
	if !ok || kubectl.MinRole != "admin" || kubectl.WebSocket {
		t.Fatalf("unexpected kubectl rule: %#v", kubectl)
	}
	tty, ok := access.Lookup("GET /api/v1/clusters/{id}/kubectl/tty")
	if !ok || tty.MinRole != "admin" || !tty.WebSocket {
		t.Fatalf("unexpected kubectl tty rule: %#v", tty)
	}
	if _, declared := access.Lookup("GET /api/v1/clusters"); declared {
		t.Fatal("read-only routes must not require a rule")
	}
}

func TestRoleAllows(t *testing.T) {
	cases := []struct {
		user, required string
		allowed        bool
	}{
		{"admin", "operator", true},
		{"operator", "operator", true},
		{"viewer", "operator", false},
		{"viewer", "admin", false},
		{"operator", "admin", false},
		{"viewer", "", true},
	}
	for _, item := range cases {
		if got := auth.RoleAllows(item.user, item.required); got != item.allowed {
			t.Fatalf("RoleAllows(%q,%q)=%v want %v", item.user, item.required, got, item.allowed)
		}
	}
}
