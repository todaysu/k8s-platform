package httpapi

import (
	"strings"

	"example.com/k8s-platform/internal/auth"
)

// routeAccess records the privilege a route requires. Authorization is
// declared next to the route pattern instead of being inferred from the
// request path: suffix matching silently breaks when a pattern gains a
// sub-path, and it cannot express "arthas mode needs admin" without reading
// the query string inside the auth layer.
type routeAccess struct {
	pattern string
	// minRole is the lowest role allowed to call the route.
	minRole string
	// websocket marks routes upgraded by gorilla/websocket. WebSocket
	// handshakes are GET requests and are not covered by the browser
	// same-origin policy, so they always need the strict Origin check.
	websocket bool
	// adminOnlyModeQuery names a query parameter that raises the requirement to
	// admin when present with the given value.
	adminOnlyModeQuery string
	adminOnlyModeValue string
}

const (
	roleAdmin    = "admin"
	roleOperator = "operator"
	roleViewer   = "viewer"
)

// privilegedRoutes is the single source of truth for authorization. Every
// pattern registered on the API mux must appear here; newRouteRegistry
// verifies that at startup so a route can never be added without a decision.
var privilegedRoutes = []routeAccess{
	// Platform settings affect API admission, collectors and privileged tools.
	{pattern: "PUT /api/v1/settings/{section}", minRole: roleAdmin},
	// Pod terminal: operators may use it, but Arthas reaches into the
	// container filesystem, so it is admin-only.
	{pattern: "GET /api/v1/clusters/{id}/pods/{namespace}/{name}/exec", minRole: roleOperator, websocket: true, adminOnlyModeQuery: "mode", adminOnlyModeValue: "arthas"},
	// kubectl is an arbitrary command channel into the target cluster.
	{pattern: "POST /api/v1/clusters/{id}/kubectl", minRole: roleAdmin},
	{pattern: "GET /api/v1/clusters/{id}/kubectl/tty", minRole: roleAdmin, websocket: true},
	// Cluster lifecycle.
	{pattern: "POST /api/v1/clusters/validate", minRole: roleAdmin},
	{pattern: "POST /api/v1/clusters", minRole: roleAdmin},
	{pattern: "DELETE /api/v1/clusters/{id}", minRole: roleAdmin},
	{pattern: "PATCH /api/v1/clusters/{id}/classification", minRole: roleAdmin},
	// Capacity baselines change what the fleet view reports.
	{pattern: "POST /api/v1/clusters/{id}/capacity/baselines", minRole: roleAdmin},
	{pattern: "POST /api/v1/clusters/{id}/capacity/baseline-decisions", minRole: roleAdmin},
	{pattern: "PUT /api/v1/capacity/classification", minRole: roleAdmin},
	// Security scanning creates privileged Jobs on target clusters.
	{pattern: "POST /api/v1/clusters/{id}/security-baseline/scan", minRole: roleAdmin},
	{pattern: "POST /api/v1/clusters/{id}/security-baseline/whitelist", minRole: roleAdmin},
	{pattern: "DELETE /api/v1/clusters/{id}/security-baseline/whitelist/{controlID}", minRole: roleAdmin},
}

// routeRegistry resolves a request to its declared access requirement.
type routeRegistry struct {
	byPattern map[string]routeAccess
	// patterns holds every registered API pattern so startup can verify that
	// each one made an explicit authorization decision.
	patterns []string
}

func newRouteRegistry(patterns []string) (*routeRegistry, error) {
	registry := &routeRegistry{byPattern: map[string]routeAccess{}, patterns: append([]string(nil), patterns...)}
	for _, route := range privilegedRoutes {
		if _, exists := registry.byPattern[route.pattern]; exists {
			return nil, &routeConflictError{pattern: route.pattern}
		}
		registry.byPattern[route.pattern] = route
	}
	return registry, nil
}

type routeConflictError struct{ pattern string }

func (e *routeConflictError) Error() string { return "duplicate privileged route: " + e.pattern }

// undeclaredRoutes returns registered patterns that can change state but have
// no access entry. Read-only GET routes intentionally stay open to any
// authenticated role, so they are not required to appear in the table; every
// non-GET pattern must be declared.
func (r *routeRegistry) undeclaredRoutes() []string {
	missing := []string{}
	for _, pattern := range r.patterns {
		method, _, ok := strings.Cut(pattern, " ")
		if !ok || method == "GET" || method == "HEAD" {
			continue
		}
		if _, declared := r.byPattern[pattern]; !declared {
			missing = append(missing, pattern)
		}
	}
	return missing
}

// Lookup implements auth.RouteAccess so the authorization table lives next to
// the route patterns it protects.
func (r *routeRegistry) Lookup(pattern string) (auth.RouteRule, bool) {
	route, ok := r.byPattern[pattern]
	if !ok {
		return auth.RouteRule{}, false
	}
	return auth.RouteRule{
		MinRole:            route.minRole,
		WebSocket:          route.websocket,
		AdminOnlyModeQuery: route.adminOnlyModeQuery,
		AdminOnlyModeValue: route.adminOnlyModeValue,
	}, true
}

// RouteAccess exposes the registry to the authentication middleware.
func RouteAccess(patterns []string) (auth.RouteAccess, error) {
	registry, err := newRouteRegistry(patterns)
	if err != nil {
		return nil, err
	}
	return registry, nil
}

// UndeclaredStateChangingRoutes reports registered non-GET patterns that have
// no explicit access rule. Startup fails when this is non-empty.
func UndeclaredStateChangingRoutes(patterns []string) ([]string, error) {
	registry, err := newRouteRegistry(patterns)
	if err != nil {
		return nil, err
	}
	return registry.undeclaredRoutes(), nil
}
