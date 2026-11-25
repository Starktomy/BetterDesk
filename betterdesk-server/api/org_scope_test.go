package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/peer"
)

func TestOrganizationCredentialsUseCurrentMembership(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()

	user := &db.User{Username: "tenant-user", PasswordHash: "unused", Role: auth.RoleViewer}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	for _, orgID := range []string{"org-a", "org-b"} {
		if err := database.CreateOrganization(&db.Organization{ID: orgID, Name: orgID, Slug: orgID}); err != nil {
			t.Fatal(err)
		}
		if err := database.CreateOrgUser(&db.OrgUser{
			ID: orgID + "-member", OrgID: orgID, ServerUserID: user.ID,
			Username: user.Username, Role: db.OrgRoleAdmin,
		}); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.DefaultConfig()
	cfg.APIPort = 0
	server := New(cfg, database, peer.NewMap(), nil, "test")
	jwt := auth.NewJWTManager("organization-scope-test-secret", time.Hour)
	server.SetJWTManager(jwt)
	if err := server.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	token, err := jwt.GenerateOrgToken(user.Username, db.OrgRoleAdmin, "org-a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	check := func(method, path string, want int) {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		server.httpSrv.Handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s got %d, want %d", method, path, rec.Code, want)
		}
	}
	check(http.MethodGet, "/api/org/org-a", http.StatusOK)
	check(http.MethodGet, "/api/org/org-b", http.StatusForbidden)
	check(http.MethodGet, "/api/peers", http.StatusOK)
	check(http.MethodGet, "/api/peers/online", http.StatusForbidden)
	check(http.MethodGet, "/api/keys", http.StatusForbidden)
	check(http.MethodDelete, "/api/org/org-a/users/org-b-member", http.StatusNotFound)

	if err := database.DeleteOrgUser("org-a-member"); err != nil {
		t.Fatal(err)
	}
	check(http.MethodGet, "/api/org/org-a", http.StatusForbidden)
}
