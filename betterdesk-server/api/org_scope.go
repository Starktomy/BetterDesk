package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

func getOrgUserFromCtx(r *http.Request) *db.OrgUser {
	user, _ := r.Context().Value(ctxKeyOrgUser).(*db.OrgUser)
	return user
}

func (s *Server) serveOrgScopedRequest(w http.ResponseWriter, r *http.Request, next http.Handler, orgID, username string) {
	user, err := s.db.GetOrgUserByUsername(orgID, username)
	if err != nil || user == nil || !db.ValidOrgRole(user.Role) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Organization membership required"})
		return
	}
	if !orgScopedRoute(r, next, orgID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Organization credentials cannot access this endpoint"})
		return
	}

	// Older handlers read the server role for presentation and scope checks.
	// Keep the organization role in its own context value for authorization.
	role := auth.RoleViewer
	if user.Role != db.OrgRoleUser {
		role = auth.RoleOperator
	}
	ctx := context.WithValue(r.Context(), ctxKeyOrgID, orgID)
	ctx = context.WithValue(ctx, ctxKeyOrgUser, user)
	ctx = context.WithValue(ctx, ctxKeyRole, role)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// Only routes that explicitly use organization membership or peer scope
// may accept an organization credential.
func orgScopedRoute(r *http.Request, next http.Handler, orgID string) bool {
	mux, ok := next.(*http.ServeMux)
	if !ok {
		return false
	}
	_, pattern := mux.Handler(r)
	switch pattern {
	case "GET /api/org", "GET /api/auth/me", "GET /api/peers",
		"GET /api/peers/{id}", "PATCH /api/peers/{id}", "DELETE /api/peers/{id}",
		"GET /api/peers/{id}/metrics", "GET /api/peers/{id}/availability",
		"GET /api/peers/{id}/connection-sessions", "POST /api/peers/{id}/ban",
		"POST /api/peers/{id}/unban", "POST /api/peers/{id}/restore", "POST /api/peers/{id}/change-id":
		return true
	}
	_, route, _ := strings.Cut(pattern, " ")
	if route != "/api/org/{id}" && !strings.HasPrefix(route, "/api/org/{id}/") {
		return false
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/org/"), "/")
	return id == orgID
}

func orgRoleHasPermission(role, permission string) bool {
	switch permission {
	case auth.PermDeviceView:
		return db.ValidOrgRole(role)
	case auth.PermDeviceEdit, auth.PermOrgManageDevices:
		return db.OrgRoleLevel(role) >= db.OrgRoleLevel(db.OrgRoleOperator)
	case auth.PermDeviceDelete, auth.PermDeviceBan, auth.PermDeviceChangeID,
		auth.PermOrgEdit, auth.PermOrgManageUsers:
		return db.OrgRoleLevel(role) >= db.OrgRoleLevel(db.OrgRoleAdmin)
	}
	return false
}
