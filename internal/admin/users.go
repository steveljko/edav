package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/steveljko/edav/internal/auth"
	"github.com/steveljko/edav/internal/storage"
)

const minPasswordLen = 8

// usersPerPage is what one page of the user list shows.
const usersPerPage = 100

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	data, err := s.usersPage(r, pageData{})
	if err != nil {
		s.fail(w, r, "list users", err)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		s.renderFragment(w, r, "users.html", "user-results", data)
		return
	}
	s.render(w, r, "users.html", data)
}

func (s *Server) usersPage(r *http.Request, data pageData) (pageData, error) {
	data.Query = strings.TrimSpace(r.URL.Query().Get("q"))
	data.Page = max(atoiOr(r.URL.Query().Get("page"), 1), 1)

	users, total, err := storage.SearchUsers(r.Context(), s.DB, data.Query,
		usersPerPage, (data.Page-1)*usersPerPage)
	if err != nil {
		return data, err
	}

	data.MatchCount = total
	data.Pages = (total + usersPerPage - 1) / usersPerPage
	if data.Page > 1 {
		data.PrevPage = data.Page - 1
	}
	if data.Page < data.Pages {
		data.NextPage = data.Page + 1
	}

	rows := make([]userRow, 0, len(users))
	for _, u := range users {
		collections, err := storage.ListCollections(r.Context(), s.DB, u.ID, "")
		if err != nil {
			return data, err
		}
		row := userRow{User: u}
		for _, c := range collections {
			if c.Type == storage.CollectionCalendar {
				row.Calendars++
			} else {
				row.AddressBooks++
			}
		}
		rows = append(rows, row)
		data.CalendarCount += row.Calendars
		data.AddressBookCount += row.AddressBooks
	}

	data.Users = rows
	return s.page(r, "Users", "users", data), nil
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	form := formValues{
		Username:    strings.TrimSpace(r.PostFormValue("username")),
		DisplayName: strings.TrimSpace(r.PostFormValue("display_name")),
		Email:       strings.TrimSpace(r.PostFormValue("email")),
	}
	password := r.PostFormValue("password")

	reject := func(message string) {
		data, err := s.usersPage(r, pageData{Form: form})
		if err != nil {
			s.fail(w, r, "list users", err)
			return
		}
		data.FormError = message
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "users.html", data)
	}

	if err := validateUsername(form.Username); err != nil {
		reject(err.Error())
		return
	}
	if len(password) < minPasswordLen {
		reject(fmt.Sprintf("The password must be at least %d characters.", minPasswordLen))
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.fail(w, r, "hash password", err)
		return
	}

	created, err := storage.CreateUser(r.Context(), s.DB, &storage.User{
		Username:     form.Username,
		DisplayName:  form.DisplayName,
		Email:        form.Email,
		PasswordHash: hash,
	})
	if errors.Is(err, storage.ErrConflict) {
		reject(fmt.Sprintf("A user named %q already exists.", form.Username))
		return
	}
	if err != nil {
		s.fail(w, r, "create user", err)
		return
	}

	slog.Info("admin created user", "username", created.Username, "id", created.ID)
	s.redirect(w, r, fmt.Sprintf("/admin/users/%d", created.ID))
}

func (s *Server) showUser(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.user(w, r)
	if !ok {
		return
	}
	data, err := s.userPage(r, subject, pageData{})
	if err != nil {
		s.fail(w, r, "show user", err)
		return
	}
	s.render(w, r, "user.html", data)
}

func (s *Server) userPage(r *http.Request, subject *storage.User, data pageData) (pageData, error) {
	collections, err := storage.ListCollections(r.Context(), s.DB, subject.ID, "")
	if err != nil {
		return data, err
	}

	data.Subject = subject
	data.Collections = collections
	if current, ok := auth.UserFrom(r.Context()); ok {
		data.IsSelf = current.ID == subject.ID
	}
	return s.page(r, subject.Username, "users", data), nil
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.user(w, r)
	if !ok {
		return
	}

	reject := func(message string) {
		data, err := s.userPage(r, subject, pageData{})
		if err != nil {
			s.fail(w, r, "show user", err)
			return
		}
		data.Error = message
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "user.html", data)
	}

	username := strings.TrimSpace(r.PostFormValue("username"))
	if err := validateUsername(username); err != nil {
		reject(err.Error())
		return
	}

	updated := *subject
	updated.Username = username
	updated.DisplayName = strings.TrimSpace(r.PostFormValue("display_name"))
	updated.Email = strings.TrimSpace(r.PostFormValue("email"))

	// An administrator demoting themselves locks everyone out of this
	// interface if they are the only one, so the form disables the control and
	// the server does not trust that alone.
	if current, ok := auth.UserFrom(r.Context()); ok && current.ID == subject.ID {
		updated.IsAdmin = subject.IsAdmin
	} else {
		updated.IsAdmin = r.PostFormValue("is_admin") == "1"
	}

	err := storage.UpdateUser(r.Context(), s.DB, &updated)
	if errors.Is(err, storage.ErrConflict) {
		reject(fmt.Sprintf("A user named %q already exists.", username))
		return
	}
	if err != nil {
		s.fail(w, r, "update user", err)
		return
	}

	slog.Info("admin updated user", "username", updated.Username, "id", updated.ID)
	s.redirect(w, r, fmt.Sprintf("/admin/users/%d", subject.ID))
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.user(w, r)
	if !ok {
		return
	}

	password := r.PostFormValue("password")
	if len(password) < minPasswordLen {
		data, err := s.userPage(r, subject, pageData{})
		if err != nil {
			s.fail(w, r, "show user", err)
			return
		}
		data.Error = fmt.Sprintf("The password must be at least %d characters.", minPasswordLen)
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "user.html", data)
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.fail(w, r, "hash password", err)
		return
	}
	if err := storage.SetPassword(r.Context(), s.DB, subject.ID, hash); err != nil {
		s.fail(w, r, "set password", err)
		return
	}
	// A changed password must not leave old admin sessions alive, including
	// the one belonging to whoever just changed it.
	if err := storage.DeleteUserSessions(r.Context(), s.DB, subject.ID); err != nil {
		s.fail(w, r, "revoke sessions", err)
		return
	}

	slog.Info("admin reset password", "username", subject.Username, "id", subject.ID)

	if current, ok := auth.UserFrom(r.Context()); ok && current.ID == subject.ID {
		s.Sessions.Clear(w)
		s.redirect(w, r, loginPath)
		return
	}
	s.redirect(w, r, fmt.Sprintf("/admin/users/%d", subject.ID))
}

func (s *Server) confirmDeleteUser(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.user(w, r)
	if !ok {
		return
	}

	collections, err := storage.ListCollections(r.Context(), s.DB, subject.ID, "")
	if err != nil {
		s.fail(w, r, "list collections", err)
		return
	}

	data := s.page(r, "Delete "+subject.Username, "users", pageData{
		Confirm: confirmation{
			Title: "Delete " + subject.Username + "?",
			Body: fmt.Sprintf("This removes the account and its %d collection%s, with every object in them. It cannot be undone.",
				len(collections), plural(len(collections))),
			Verb:   "Delete " + subject.Username,
			Action: fmt.Sprintf("/admin/users/%d/delete", subject.ID),
			Cancel: fmt.Sprintf("/admin/users/%d", subject.ID),
		},
	})
	s.render(w, r, "confirm.html", data)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.user(w, r)
	if !ok {
		return
	}

	if current, ok := auth.UserFrom(r.Context()); ok && current.ID == subject.ID {
		data, err := s.userPage(r, subject, pageData{})
		if err != nil {
			s.fail(w, r, "show user", err)
			return
		}
		data.Error = "You cannot delete the account you are signed in as."
		s.renderStatus(w, r, http.StatusForbidden, "user.html", data)
		return
	}

	if err := storage.DeleteUser(r.Context(), s.DB, subject.ID); err != nil {
		s.fail(w, r, "delete user", err)
		return
	}

	slog.Info("admin deleted user", "username", subject.Username, "id", subject.ID)
	s.redirect(w, r, basePath)
}

func validateUsername(username string) error {
	if username == "" {
		return errors.New("A username is required.")
	}
	if len(username) > 64 {
		return errors.New("That username is too long.")
	}
	// The username is a path segment in every DAV URL this account uses.
	if strings.ContainsAny(username, "/\\?#% \t") {
		return errors.New("A username cannot contain spaces or any of / \\ ? # %.")
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
