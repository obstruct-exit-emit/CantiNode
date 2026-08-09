package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAuthSessionsAndAPIKeyRegen(t *testing.T) {
	a := newTestAPI(t, nil)

	// Auth is disabled until credentials are set.
	var status struct {
		AuthEnabled   bool `json:"authEnabled"`
		Authenticated bool `json:"authenticated"`
	}
	a.want(a.call("GET", "/api/v1/auth/status", nil, &status), http.StatusOK)
	if status.AuthEnabled {
		t.Fatal("auth enabled on a fresh config")
	}

	// Logging in while disabled is a 400; short passwords are rejected.
	a.want(a.call("POST", "/api/v1/auth/login",
		map[string]string{"username": "dan", "password": "whatever1"}, nil), http.StatusBadRequest)
	a.want(a.call("PUT", "/api/v1/auth/credentials",
		map[string]string{"username": "dan", "password": "short"}, nil), http.StatusBadRequest)

	// Enable the account (API-key authenticated).
	a.want(a.call("PUT", "/api/v1/auth/credentials",
		map[string]string{"username": "dan", "password": "secret-pass-1"}, nil), http.StatusOK)
	a.want(a.call("GET", "/api/v1/auth/status", nil, &status), http.StatusOK)
	if !status.AuthEnabled {
		t.Fatal("auth still disabled after setting credentials")
	}

	// Wrong password is rejected; right one issues a session cookie.
	post := func(body string) *http.Response {
		resp, err := http.Post(a.srv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if resp := post(`{"username":"dan","password":"wrong-pass-1"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: status %d, want 401", resp.StatusCode)
	}
	resp := post(`{"username":"dan","password":"secret-pass-1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: status %d, want 200", resp.StatusCode)
	}
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			session = c
		}
	}
	if session == nil || session.Value == "" {
		t.Fatal("login response set no session cookie")
	}

	// The session alone (no API key) authenticates API requests.
	withCookie := func(c *http.Cookie) int {
		req, _ := http.NewRequest("GET", a.srv.URL+"/api/v1/author", nil)
		if c != nil {
			req.AddCookie(c)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if got := withCookie(session); got != http.StatusOK {
		t.Fatalf("session request: status %d, want 200", got)
	}
	if got := withCookie(nil); got != http.StatusUnauthorized {
		t.Fatalf("bare request: status %d, want 401", got)
	}

	// Logout revokes the session.
	req, _ := http.NewRequest("POST", a.srv.URL+"/api/v1/auth/logout", nil)
	req.AddCookie(session)
	lr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	lr.Body.Close()
	if got := withCookie(session); got != http.StatusUnauthorized {
		t.Fatalf("session after logout: status %d, want 401", got)
	}

	// Regenerate the API key: old key dies, new key works.
	oldKey := a.apiKey
	var regen struct {
		APIKey string `json:"apiKey"`
	}
	a.want(a.call("POST", "/api/v1/auth/apikey/regenerate", nil, &regen), http.StatusOK)
	if regen.APIKey == "" || regen.APIKey == oldKey {
		t.Fatalf("regenerated key %q should be fresh", regen.APIKey)
	}
	if r := a.call("GET", "/api/v1/author", nil, nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old API key: status %d, want 401", r.StatusCode)
	}
	a.apiKey = regen.APIKey
	a.want(a.call("GET", "/api/v1/author", nil, nil), http.StatusOK)
}

// Sessions are bound to their account: removing a user ends their open
// sessions immediately, and a password change ends the account's OTHER
// sessions while the browser making the change keeps its own.
func TestSessionUserBinding(t *testing.T) {
	a := newTestAPI(t, nil)

	a.want(a.call("PUT", "/api/v1/auth/credentials",
		map[string]string{"username": "dan", "password": "secret-pass-1"}, nil), http.StatusOK)
	a.want(a.call("POST", "/api/v1/auth/users",
		map[string]string{"username": "guest", "password": "guest-pass-1"}, nil), http.StatusCreated)

	login := func(user, pass string) *http.Cookie {
		resp, err := http.Post(a.srv.URL+"/api/v1/auth/login", "application/json",
			strings.NewReader(`{"username":"`+user+`","password":"`+pass+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("login %s: status %d", user, resp.StatusCode)
		}
		for _, c := range resp.Cookies() {
			if c.Name == sessionCookie {
				return c
			}
		}
		t.Fatalf("login %s: no session cookie", user)
		return nil
	}
	status := func(c *http.Cookie) int {
		req, _ := http.NewRequest("GET", a.srv.URL+"/api/v1/author", nil)
		req.AddCookie(c)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}

	guest1 := login("guest", "guest-pass-1")
	guest2 := login("guest", "guest-pass-1")
	dan := login("dan", "secret-pass-1")

	// Changing guest's password (via API key, not a session) ends BOTH guest
	// sessions; dan's is untouched.
	a.want(a.call("PUT", "/api/v1/auth/users/guest/password",
		map[string]string{"password": "new-guest-pass"}, nil), http.StatusOK)
	if status(guest1) != http.StatusUnauthorized || status(guest2) != http.StatusUnauthorized {
		t.Fatal("guest sessions should end on password change")
	}
	if status(dan) != http.StatusOK {
		t.Fatal("dan's session should survive guest's password change")
	}

	// Removing guest ends their fresh session too.
	guest3 := login("guest", "new-guest-pass")
	a.want(a.call("DELETE", "/api/v1/auth/users/guest", nil, nil), http.StatusOK)
	if status(guest3) != http.StatusUnauthorized {
		t.Fatal("removed user's session should end immediately")
	}
	if status(dan) != http.StatusOK {
		t.Fatal("dan's session should survive guest's removal")
	}

	// Disabling login ends every session.
	a.want(a.call("PUT", "/api/v1/auth/credentials",
		map[string]string{"username": "", "password": ""}, nil), http.StatusOK)
	// With auth disabled the API key still works; the dead cookie is simply
	// no longer a session. (A bare request without key now 401s only via
	// missing key — cookie path must not authenticate.)
	if got := status(dan); got != http.StatusUnauthorized {
		t.Fatalf("session after disable-login: status %d, want 401", got)
	}
}

// TestMemberRoleRestrictions: a member session reaches ordinary library
// endpoints (auth) but is turned away (403) from server-configuration ones
// (requireAdmin) — and can change their own password but not someone
// else's, while an admin can do both.
func TestMemberRoleRestrictions(t *testing.T) {
	a := newTestAPI(t, nil)

	a.want(a.call("PUT", "/api/v1/auth/credentials",
		map[string]string{"username": "admin1", "password": "admin-pass-1"}, nil), http.StatusOK)
	// No role specified — defaults to member (see handleAddUser).
	a.want(a.call("POST", "/api/v1/auth/users",
		map[string]string{"username": "kid", "password": "kid-pass-1"}, nil), http.StatusCreated)

	login := func(user, pass string) *http.Cookie {
		resp, err := http.Post(a.srv.URL+"/api/v1/auth/login", "application/json",
			strings.NewReader(`{"username":"`+user+`","password":"`+pass+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("login %s: status %d", user, resp.StatusCode)
		}
		for _, c := range resp.Cookies() {
			if c.Name == sessionCookie {
				return c
			}
		}
		t.Fatalf("login %s: no session cookie", user)
		return nil
	}
	do := func(c *http.Cookie, method, path, body string) int {
		var bodyReader *strings.Reader
		if body != "" {
			bodyReader = strings.NewReader(body)
		} else {
			bodyReader = strings.NewReader("")
		}
		req, _ := http.NewRequest(method, a.srv.URL+path, bodyReader)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(c)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}

	kid := login("kid", "kid-pass-1")
	admin := login("admin1", "admin-pass-1")

	// auth/status reports the role, for the frontend to gate its own UI.
	var whoami struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	req, _ := http.NewRequest("GET", a.srv.URL+"/api/v1/auth/status", nil)
	req.AddCookie(kid)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&whoami); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if whoami.Username != "kid" || whoami.Role != "member" {
		t.Fatalf("auth/status for kid = %+v, want username kid, role member", whoami)
	}

	// A member reaches ordinary library endpoints fine.
	if got := do(kid, "GET", "/api/v1/author", ""); got != http.StatusOK {
		t.Fatalf("member GET /author = %d, want 200", got)
	}
	// A member is turned away from server configuration — 403, not 401 (they
	// ARE authenticated, just not privileged enough).
	if got := do(kid, "GET", "/api/v1/indexer", ""); got != http.StatusForbidden {
		t.Fatalf("member GET /indexer = %d, want 403", got)
	}
	if got := do(kid, "POST", "/api/v1/rootfolder", `{"mediaType":"ebook","path":"/tmp"}`); got != http.StatusForbidden {
		t.Fatalf("member POST /rootfolder = %d, want 403", got)
	}
	if got := do(kid, "GET", "/api/v1/backup", ""); got != http.StatusForbidden {
		t.Fatalf("member GET /backup = %d, want 403", got)
	}
	// An admin reaches the same endpoint fine.
	if got := do(admin, "GET", "/api/v1/indexer", ""); got != http.StatusOK {
		t.Fatalf("admin GET /indexer = %d, want 200", got)
	}

	// Self-service: a member can change their OWN password...
	if got := do(kid, "PUT", "/api/v1/auth/users/kid/password", `{"password":"kid-pass-2"}`); got != http.StatusOK {
		t.Fatalf("member changing own password = %d, want 200", got)
	}
	// ...but not someone else's.
	kid2 := login("kid", "kid-pass-2")
	if got := do(kid2, "PUT", "/api/v1/auth/users/admin1/password", `{"password":"stolen-pass1"}`); got != http.StatusForbidden {
		t.Fatalf("member changing another user's password = %d, want 403", got)
	}
	// An admin can change anyone's.
	if got := do(admin, "PUT", "/api/v1/auth/users/kid/password", `{"password":"kid-pass-3"}`); got != http.StatusOK {
		t.Fatalf("admin changing member's password = %d, want 200", got)
	}
}

// TestUserRoleChangeAndDefaultInvariant: SetUserRole promotes/demotes and
// revokes the account's sessions; the default user can never be demoted,
// and promoting someone to default makes them an admin in the same step.
func TestUserRoleChangeAndDefaultInvariant(t *testing.T) {
	a := newTestAPI(t, nil)

	a.want(a.call("PUT", "/api/v1/auth/credentials",
		map[string]string{"username": "owner", "password": "owner-pass-1"}, nil), http.StatusOK)
	a.want(a.call("POST", "/api/v1/auth/users",
		map[string]any{"username": "helper", "password": "helper-pass1", "role": "admin"}, nil), http.StatusCreated)

	// The default user ("owner") can't be demoted.
	if r := a.call("PUT", "/api/v1/auth/users/owner/role", map[string]string{"role": "member"}, nil); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("demoting the default user = %d, want 400", r.StatusCode)
	}

	// helper was created as admin (explicit role); demote them to member.
	login := func(user, pass string) *http.Cookie {
		resp, err := http.Post(a.srv.URL+"/api/v1/auth/login", "application/json",
			strings.NewReader(`{"username":"`+user+`","password":"`+pass+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		for _, c := range resp.Cookies() {
			if c.Name == sessionCookie {
				return c
			}
		}
		t.Fatalf("login %s: no session cookie", user)
		return nil
	}
	status := func(c *http.Cookie, path string) int {
		req, _ := http.NewRequest("GET", a.srv.URL+path, nil)
		req.AddCookie(c)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}

	helperSession := login("helper", "helper-pass1")
	if got := status(helperSession, "/api/v1/indexer"); got != http.StatusOK {
		t.Fatalf("helper (admin) GET /indexer = %d, want 200", got)
	}

	a.want(a.call("PUT", "/api/v1/auth/users/helper/role", map[string]string{"role": "member"}, nil), http.StatusOK)
	// Demotion revokes the existing session immediately.
	if got := status(helperSession, "/api/v1/indexer"); got != http.StatusUnauthorized {
		t.Fatalf("helper's session after demotion = %d, want 401 (revoked)", got)
	}

	// Logged back in, helper is now a plain member.
	helperSession2 := login("helper", "helper-pass1")
	if got := status(helperSession2, "/api/v1/indexer"); got != http.StatusForbidden {
		t.Fatalf("demoted helper GET /indexer = %d, want 403", got)
	}

	// Promoting helper to default makes them admin too, even though they're
	// currently a member.
	a.want(a.call("PUT", "/api/v1/auth/users/helper/default", nil, nil), http.StatusOK)
	if got := status(helperSession2, "/api/v1/indexer"); got != http.StatusUnauthorized {
		t.Fatalf("helper's session after becoming default = %d, want 401 (revoked by promotion)", got)
	}
	helperSession3 := login("helper", "helper-pass1")
	if got := status(helperSession3, "/api/v1/indexer"); got != http.StatusOK {
		t.Fatalf("helper (new default) GET /indexer = %d, want 200", got)
	}
}
