package admin

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/steveljko/edav/internal/auth"
	"github.com/steveljko/edav/internal/storage"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

const adminPassword = "hunter2hunter2"

type harness struct {
	t      *testing.T
	db     *sql.DB
	server *httptest.Server
	client *http.Client
	admin  *storage.User
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("storage.Open() = %v", err)
	}
	t.Cleanup(func() { db.Close() })

	hash, err := auth.HashPassword(adminPassword)
	if err != nil {
		t.Fatalf("HashPassword() = %v", err)
	}
	adminUser, err := storage.EnsureAdmin(t.Context(), db, "admin", hash)
	if err != nil {
		t.Fatalf("EnsureAdmin() = %v", err)
	}

	mux := http.NewServeMux()
	srv := &Server{
		DB:             db,
		Sessions:       &auth.Sessions{DB: db, Path: "/admin"},
		DAVPrefix:      "/dav",
		CalDAVEnabled:  true,
		CardDAVEnabled: true,
	}
	if err := srv.Register(mux); err != nil {
		t.Fatalf("Register() = %v", err)
	}

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}

	h := &harness{
		t: t, db: db, server: ts, admin: adminUser,
		client: &http.Client{
			Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	return h
}

func (h *harness) get(path string) *http.Response {
	h.t.Helper()
	resp, err := h.client.Get(h.server.URL + path)
	if err != nil {
		h.t.Fatalf("GET %s: %v", path, err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (h *harness) post(path string, form url.Values) *http.Response {
	h.t.Helper()
	resp, err := h.client.PostForm(h.server.URL+path, form)
	if err != nil {
		h.t.Fatalf("POST %s: %v", path, err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

var csrfPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// csrf reads the token out of a rendered page, the way a browser submitting the
// form would.
func (h *harness) csrf(path string) string {
	h.t.Helper()
	resp := h.get(path)
	m := csrfPattern.FindStringSubmatch(body(h.t, resp))
	if m == nil {
		h.t.Fatalf("no CSRF token in %s", path)
	}
	return m[1]
}

func (h *harness) login() {
	h.t.Helper()
	resp := h.post("/admin/login", url.Values{
		"username": {"admin"},
		"password": {adminPassword},
	})
	if resp.StatusCode != http.StatusSeeOther {
		h.t.Fatalf("login = %d, want 303: %s", resp.StatusCode, body(h.t, resp))
	}
}

// form builds a submission carrying the CSRF token from the page it came from.
func (h *harness) form(page string, values url.Values) url.Values {
	h.t.Helper()
	if values == nil {
		values = url.Values{}
	}
	values.Set("csrf_token", h.csrf(page))
	return values
}

func TestAdminRequiresSignIn(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{
		"/admin/",
		"/admin/setup",
		"/admin/users/1",
	} {
		t.Run(path, func(t *testing.T) {
			resp := h.get(path)
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", resp.StatusCode)
			}
			if got := resp.Header.Get("Location"); got != "/admin/login" {
				t.Errorf("Location = %q, want /admin/login", got)
			}
		})
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	h := newHarness(t)

	tests := []struct {
		name     string
		username string
		password string
		want     int
	}{
		{"wrong password", "admin", "nope", http.StatusUnauthorized},
		{"unknown user", "mallory", adminPassword, http.StatusUnauthorized},
		{"empty", "", "", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := h.post("/admin/login", url.Values{
				"username": {tt.username}, "password": {tt.password},
			})
			if resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			for _, c := range resp.Cookies() {
				if c.Name == auth.SessionCookieName && c.Value != "" {
					t.Error("a session cookie was issued for a failed login")
				}
			}
		})
	}
}

// A DAV user is not an administrator; having an account is not access to this
// interface.
func TestLoginRejectsNonAdministrator(t *testing.T) {
	h := newHarness(t)

	hash, err := auth.HashPassword(adminPassword)
	if err != nil {
		t.Fatalf("HashPassword() = %v", err)
	}
	if _, err := storage.CreateUser(t.Context(), h.db, &storage.User{
		Username: "bob", PasswordHash: hash,
	}); err != nil {
		t.Fatalf("CreateUser() = %v", err)
	}

	resp := h.post("/admin/login", url.Values{"username": {"bob"}, "password": {adminPassword}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestLoginAndLogout(t *testing.T) {
	h := newHarness(t)
	h.login()

	resp := h.get("/admin/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("after login = %d, want 200", resp.StatusCode)
	}
	if got := body(t, resp); !strings.Contains(got, "admin") {
		t.Errorf("users page does not list the admin account:\n%s", got)
	}

	resp = h.post("/admin/logout", h.form("/admin/", nil))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout = %d, want 303", resp.StatusCode)
	}

	if resp := h.get("/admin/"); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("after logout = %d, want a redirect to the login page", resp.StatusCode)
	}
}

// Every mutating form carries a token, and the session middleware rejects a
// submission without it.
func TestMutationsRequireCSRFToken(t *testing.T) {
	h := newHarness(t)
	h.login()

	tests := []struct {
		name string
		path string
		form url.Values
	}{
		{"create user", "/admin/users", url.Values{"username": {"x"}, "password": {adminPassword}}},
		{"update user", "/admin/users/1", url.Values{"username": {"admin"}}},
		{"reset password", "/admin/users/1/password", url.Values{"password": {adminPassword}}},
		{"delete user", "/admin/users/1/delete", url.Values{}},
		{"create collection", "/admin/users/1/collections", url.Values{"uri": {"x"}, "type": {"calendar"}}},
		{"logout", "/admin/logout", url.Values{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := h.post(tt.path, tt.form)
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want 403 without a CSRF token", resp.StatusCode)
			}
		})
	}
}

func TestCSRFTokenFromAnotherSessionIsRejected(t *testing.T) {
	h := newHarness(t)
	h.login()

	resp := h.post("/admin/users", url.Values{
		"csrf_token": {"not-the-token"},
		"username":   {"bob"},
		"password":   {adminPassword},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestCreateUser(t *testing.T) {
	h := newHarness(t)
	h.login()

	resp := h.post("/admin/users", h.form("/admin/", url.Values{
		"username":     {"bob"},
		"display_name": {"Bob Bobson"},
		"email":        {"bob@example.com"},
		"password":     {adminPassword},
	}))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", resp.StatusCode, body(t, resp))
	}

	u, err := storage.UserByUsername(t.Context(), h.db, "bob")
	if err != nil {
		t.Fatalf("UserByUsername() = %v", err)
	}
	if u.DisplayName != "Bob Bobson" || u.Email != "bob@example.com" {
		t.Errorf("created user = %+v", u)
	}
	if u.IsAdmin {
		t.Error("a new user should not be an administrator by default")
	}
	// The stored password must be a hash, and must verify.
	if err := auth.VerifyPassword(u.PasswordHash, adminPassword); err != nil {
		t.Errorf("stored password does not verify: %v", err)
	}
}

func TestCreateUserRejectsBadInput(t *testing.T) {
	h := newHarness(t)
	h.login()

	tests := []struct {
		name     string
		values   url.Values
		wantText string
	}{
		{"empty username", url.Values{"username": {""}, "password": {adminPassword}}, "username is required"},
		{"username with a slash", url.Values{"username": {"a/b"}, "password": {adminPassword}}, "cannot contain"},
		{"short password", url.Values{"username": {"bob"}, "password": {"short"}}, "at least 8"},
		{"duplicate", url.Values{"username": {"admin"}, "password": {adminPassword}}, "already exists"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := h.post("/admin/users", h.form("/admin/", tt.values))
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", resp.StatusCode)
			}
			if got := body(t, resp); !strings.Contains(got, tt.wantText) {
				t.Errorf("page does not explain the problem (%q):\n%s", tt.wantText, got)
			}
		})
	}
}

func TestUpdateUser(t *testing.T) {
	h := newHarness(t)
	h.login()
	bob := h.createUser("bob")

	page := "/admin/users/" + itoa(bob.ID)
	resp := h.post(page, h.form(page, url.Values{
		"username":     {"robert"},
		"display_name": {"Robert"},
		"email":        {"robert@example.com"},
		"is_admin":     {"1"},
	}))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", resp.StatusCode, body(t, resp))
	}

	got, err := storage.UserByID(t.Context(), h.db, bob.ID)
	if err != nil {
		t.Fatalf("UserByID() = %v", err)
	}
	if got.Username != "robert" || got.DisplayName != "Robert" || !got.IsAdmin {
		t.Errorf("updated user = %+v", got)
	}
}

// The last administrator locking themselves out is not recoverable through the
// interface, so the server refuses regardless of what the form submits.
func TestAdminCannotDemoteThemselves(t *testing.T) {
	h := newHarness(t)
	h.login()

	page := "/admin/users/" + itoa(h.admin.ID)
	resp := h.post(page, h.form(page, url.Values{
		"username": {"admin"},
		"is_admin": {"0"},
	}))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}

	got, err := storage.UserByID(t.Context(), h.db, h.admin.ID)
	if err != nil {
		t.Fatalf("UserByID() = %v", err)
	}
	if !got.IsAdmin {
		t.Error("the signed-in administrator demoted themselves")
	}
}

func TestAdminCannotDeleteThemselves(t *testing.T) {
	h := newHarness(t)
	h.login()

	page := "/admin/users/" + itoa(h.admin.ID)
	resp := h.post(page+"/delete", h.form(page, nil))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if _, err := storage.UserByID(t.Context(), h.db, h.admin.ID); err != nil {
		t.Error("the signed-in administrator deleted their own account")
	}
}

func TestResetPasswordRevokesSessions(t *testing.T) {
	h := newHarness(t)
	h.login()
	bob := h.createUser("bob")

	// Give bob a session of his own, which the reset must invalidate.
	bobSession := &auth.Sessions{DB: h.db, Path: "/admin"}
	rec := httptest.NewRecorder()
	sess, err := bobSession.Create(t.Context(), rec, bob.ID)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	page := "/admin/users/" + itoa(bob.ID)
	resp := h.post(page+"/password", h.form(page, url.Values{"password": {"a new password"}}))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", resp.StatusCode, body(t, resp))
	}

	if _, _, err := storage.SessionByToken(t.Context(), h.db, sess.Token); err == nil {
		t.Error("the user's existing session survived a password reset")
	}

	got, err := storage.UserByID(t.Context(), h.db, bob.ID)
	if err != nil {
		t.Fatalf("UserByID() = %v", err)
	}
	if err := auth.VerifyPassword(got.PasswordHash, "a new password"); err != nil {
		t.Errorf("new password does not verify: %v", err)
	}
}

func TestDeleteUserRemovesTheirCollections(t *testing.T) {
	h := newHarness(t)
	h.login()
	bob := h.createUser("bob")

	if _, err := storage.CreateCollection(t.Context(), h.db, &storage.Collection{
		OwnerID: bob.ID, Type: storage.CollectionCalendar, URI: "work",
	}); err != nil {
		t.Fatalf("CreateCollection() = %v", err)
	}

	page := "/admin/users/" + itoa(bob.ID)
	resp := h.post(page+"/delete", h.form(page, nil))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", resp.StatusCode, body(t, resp))
	}

	if _, err := storage.UserByID(t.Context(), h.db, bob.ID); err == nil {
		t.Error("user survived deletion")
	}
	collections, err := storage.ListCollections(t.Context(), h.db, bob.ID, "")
	if err != nil {
		t.Fatalf("ListCollections() = %v", err)
	}
	if len(collections) != 0 {
		t.Errorf("collections = %d after owner delete, want 0", len(collections))
	}
}

// A destructive action must be confirmable without JavaScript, so the
// confirmation page has to exist and carry a token of its own.
func TestDeleteConfirmationPages(t *testing.T) {
	h := newHarness(t)
	h.login()
	bob := h.createUser("bob")
	c, err := storage.CreateCollection(t.Context(), h.db, &storage.Collection{
		OwnerID: bob.ID, Type: storage.CollectionCalendar, URI: "work", DisplayName: "Work",
	})
	if err != nil {
		t.Fatalf("CreateCollection() = %v", err)
	}

	tests := []struct {
		name     string
		path     string
		wantText []string
	}{
		{"user", "/admin/users/" + itoa(bob.ID) + "/delete", []string{"Delete bob?", "cannot be undone", "csrf_token"}},
		{"collection", "/admin/collections/" + itoa(c.ID) + "/delete", []string{"Delete Work?", "cannot be undone", "csrf_token"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := h.get(tt.path)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			got := body(t, resp)
			for _, want := range tt.wantText {
				if !strings.Contains(got, want) {
					t.Errorf("confirmation page missing %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestCollectionLifecycle(t *testing.T) {
	h := newHarness(t)
	h.login()
	bob := h.createUser("bob")
	userPage := "/admin/users/" + itoa(bob.ID)

	resp := h.post(userPage+"/collections", h.form(userPage, url.Values{
		"uri":          {"personal"},
		"type":         {"calendar"},
		"display_name": {"Personal"},
	}))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create = %d, want 303: %s", resp.StatusCode, body(t, resp))
	}

	c, err := storage.CollectionByURI(t.Context(), h.db, bob.ID, "personal")
	if err != nil {
		t.Fatalf("CollectionByURI() = %v", err)
	}
	if c.Type != storage.CollectionCalendar || c.DisplayName != "Personal" {
		t.Fatalf("created collection = %+v", c)
	}

	page := "/admin/collections/" + itoa(c.ID)
	resp = h.post(page, h.form(page, url.Values{
		"display_name": {"Renamed"},
		"description":  {"Things I do"},
		"color":        {"#ff0000"},
	}))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update = %d, want 303", resp.StatusCode)
	}

	got, err := storage.CollectionByID(t.Context(), h.db, c.ID)
	if err != nil {
		t.Fatalf("CollectionByID() = %v", err)
	}
	if got.DisplayName != "Renamed" || got.Description != "Things I do" || got.Color != "#ff0000" {
		t.Errorf("updated collection = %+v", got)
	}
	// Editing properties is not a member change, so clients must not be sent
	// looking for one.
	if got.CTag != c.CTag {
		t.Errorf("ctag changed on a property edit: %q then %q", c.CTag, got.CTag)
	}

	resp = h.post(page+"/delete", h.form(page, nil))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete = %d, want 303", resp.StatusCode)
	}
	if _, err := storage.CollectionByID(t.Context(), h.db, c.ID); err == nil {
		t.Error("collection survived deletion")
	}
}

func TestCreateCollectionRejectsBadSlug(t *testing.T) {
	h := newHarness(t)
	h.login()
	bob := h.createUser("bob")
	page := "/admin/users/" + itoa(bob.ID)

	for _, slug := range []string{"", "with space", "a/b", "../escape", "?query"} {
		t.Run(slug, func(t *testing.T) {
			resp := h.post(page+"/collections", h.form(page, url.Values{
				"uri": {slug}, "type": {"calendar"},
			}))
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", resp.StatusCode)
			}
		})
	}
}

func TestSetupPageShowsClientURLs(t *testing.T) {
	h := newHarness(t)
	h.login()

	resp := h.get("/admin/setup")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	got := body(t, resp)
	for _, want := range []string{
		"/.well-known/caldav",
		"/.well-known/carddav",
		"/dav/principals/admin/",
		"/dav/calendars/admin/",
		"/dav/addressbooks/admin/",
		"DAVx",
		"Thunderbird",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("setup page does not mention %q", want)
		}
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	h := newHarness(t)

	tests := []struct {
		path     string
		contains string
	}{
		{"/admin/static/htmx.min.js", "htmx"},
		{"/admin/static/style.css", "prefers-color-scheme"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp := h.get(tt.path)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if got := body(t, resp); !strings.Contains(got, tt.contains) {
				t.Errorf("asset does not contain %q", tt.contains)
			}
		})
	}
}

// The pages must render from a vendored file rather than a CDN, since the
// server is meant to work without outbound network access.
func TestNoExternalAssetReferences(t *testing.T) {
	h := newHarness(t)
	h.login()
	bob := h.createUser("bob")

	for _, path := range []string{"/admin/", "/admin/setup", "/admin/users/" + itoa(bob.ID)} {
		got := body(t, h.get(path))
		for _, bad := range []string{"//unpkg.com", "//cdn.", "https://cdn", "http://cdn"} {
			if strings.Contains(got, bad) {
				t.Errorf("%s references an external asset (%q)", path, bad)
			}
		}
	}
}

func (h *harness) createUser(username string) *storage.User {
	h.t.Helper()
	hash, err := auth.HashPassword(adminPassword)
	if err != nil {
		h.t.Fatalf("HashPassword() = %v", err)
	}
	u, err := storage.CreateUser(h.t.Context(), h.db, &storage.User{
		Username: username, PasswordHash: hash,
	})
	if err != nil {
		h.t.Fatalf("CreateUser(%q) = %v", username, err)
	}
	return u
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

// Guessing at the sign-in form is limited the same way as at the DAV
// endpoint, or the admin account is the softer of the two targets.
func TestLoginThrottlesRepeatedFailures(t *testing.T) {
	h := newHarness(t)

	var lastStatus int
	for i := range 12 {
		resp := h.post("/admin/login", url.Values{
			"username": {"admin"}, "password": {"wrong"},
		})
		lastStatus = resp.StatusCode
		if lastStatus == http.StatusTooManyRequests {
			if i < 4 {
				t.Fatalf("throttled after only %d attempts", i+1)
			}
			break
		}
		if lastStatus != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, lastStatus)
		}
	}

	if lastStatus != http.StatusTooManyRequests {
		t.Fatal("repeated failures were never throttled")
	}

	resp := h.post("/admin/login", url.Values{"username": {"admin"}, "password": {adminPassword}})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("the correct password during a throttle = %d, want 429", resp.StatusCode)
	}
	if got := body(t, resp); !strings.Contains(got, "Too many attempts") {
		t.Errorf("the page does not explain the wait:\n%s", got)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("no Retry-After header")
	}
}

// A typo before a correct password must not count against the next session.
func TestLoginThrottleClearsOnSuccess(t *testing.T) {
	h := newHarness(t)

	for range 3 {
		if resp := h.post("/admin/login", url.Values{
			"username": {"admin"}, "password": {"wrong"},
		}); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	}

	if resp := h.post("/admin/login", url.Values{
		"username": {"admin"}, "password": {adminPassword},
	}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign in = %d, want 303", resp.StatusCode)
	}

	for i := range 3 {
		if resp := h.post("/admin/login", url.Values{
			"username": {"admin"}, "password": {"wrong"},
		}); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d after a success = %d, want 401", i+1, resp.StatusCode)
		}
	}
}

// The user list is a table like the others, and a long one has to page.
func TestUserListPaginates(t *testing.T) {
	h := newHarness(t)
	h.login()

	for i := range usersPerPage + 15 {
		if _, err := storage.CreateUser(t.Context(), h.db, &storage.User{
			Username:     fmt.Sprintf("user%03d", i),
			PasswordHash: "unused",
		}); err != nil {
			t.Fatalf("CreateUser() = %v", err)
		}
	}

	first := body(t, h.get(basePath))
	if n := strings.Count(first, `<td class="subject">`); n != usersPerPage {
		t.Errorf("first page rendered %d rows, want %d", n, usersPerPage)
	}
	if !strings.Contains(first, "Page 1 of 2") {
		t.Error("no pager on an over-long user list")
	}

	// The admin account is in there too, so the tail is one longer.
	second := body(t, h.get(basePath+"?page=2"))
	if n := strings.Count(second, `<td class="subject">`); n != 16 {
		t.Errorf("second page rendered %d rows, want 16", n)
	}
}

// Searching users must reach past the page in view.
func TestUserSearchIsServerSide(t *testing.T) {
	h := newHarness(t)
	h.login()

	for i := range usersPerPage + 5 {
		if _, err := storage.CreateUser(t.Context(), h.db, &storage.User{
			Username:     fmt.Sprintf("user%03d", i),
			Email:        fmt.Sprintf("user%03d@example.com", i),
			PasswordHash: "unused",
		}); err != nil {
			t.Fatalf("CreateUser() = %v", err)
		}
	}

	// A name that only exists on the last page.
	found := body(t, h.get(basePath+"?q=user104"))
	if !strings.Contains(found, "user104") {
		t.Error("search missed a user beyond the first page")
	}
	if strings.Contains(found, "user003") {
		t.Error("search returned users it should have filtered out")
	}

	// The address is searchable as well as the name.
	byEmail := body(t, h.get(basePath+"?q=user007@example.com"))
	if !strings.Contains(byEmail, "user007") {
		t.Error("search did not match on email")
	}
}
