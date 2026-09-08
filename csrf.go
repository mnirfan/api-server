package main

import (
	"encoding/json"
	"net/http"
)

func (app *Application) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", app.allowedOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		next.ServeHTTP(w, r)
	})
}

func (app *Application) csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed := app.isAllowedOrigin(r)
		if !allowed {
			res := APIResponse{
				Success: false,
				Message: "Forbidden",
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(res)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (app *Application) isAllowedOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == app.allowedOrigin
}
