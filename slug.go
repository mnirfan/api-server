package main

import (
	"encoding/json"
	"net/http"
	"regexp"
)

const MAX_SLUG_LENGTH = 100

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func responseInvalidSlug(w http.ResponseWriter) {
	res := APIResponse{
		Success: false,
		Message: "invalid slug",
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(res)
}

func (app *Application) slugValidationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if len(slug) > MAX_SLUG_LENGTH {
			responseInvalidSlug(w)
			return
		}

		match := slugPattern.MatchString(slug)
		if !match {
			responseInvalidSlug(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}
