package web

import (
	"net/http"
	"s3gate/db"
)

func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		user, err := db.GetSessionUser(cookie.Value)
		if err != nil || user == nil {
			http.SetCookie(w, &http.Cookie{Name: "session", MaxAge: -1, Path: "/"})
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		r.Header.Set("X-User-ID", user.ID)
		r.Header.Set("X-User-Email", user.Email)
		r.Header.Set("X-User-Status", user.Status)
		next(w, r)
	}
}

func GetCurrentUser(r *http.Request) *db.User {
	cookie, err := r.Cookie("session")
	if err != nil {
		return nil
	}
	user, _ := db.GetSessionUser(cookie.Value)
	return user
}
