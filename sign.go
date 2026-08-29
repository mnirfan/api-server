package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

func signAnonID(id string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(id))
	sig := hex.EncodeToString(mac.Sum(nil))
	return id + "." + sig
}

func verifyAnonID(signed string, secret []byte) (id string, ok bool) {
	data := strings.Split(signed, ".")
	if len(data) < 2 {
		return data[0], false
	}

	expectedMac, err := hex.DecodeString(data[1])
	if err != nil {
		return data[0], false
	}

	expectedID := data[0]

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(expectedID))
	computedMac := mac.Sum(nil)

	equal := hmac.Equal(expectedMac, computedMac)
	return data[0], equal
}

func generateAnonIDCookie(secret []byte) (string, *http.Cookie) {
	id := signAnonID(rand.Text(), secret)

	// Set cookie
	anonIDCookie := &http.Cookie{
		Name:     "anonid",
		Value:    id,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   30 * 24 * 60 * 60,
		Path:     "/",
	}

	return id, anonIDCookie
}

func anonIDFromContext(r *http.Request) (string, bool) {
	anonID, ok := r.Context().Value(anonIDKey).(string)
	return anonID, ok
}

func (app *Application) anonIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("anonid")
		if err != nil {
			switch {
			case errors.Is(err, http.ErrNoCookie):
				id, anonIDCookie := generateAnonIDCookie(app.secret)
				http.SetCookie(w, anonIDCookie)

				newR := r.WithContext(context.WithValue(r.Context(), anonIDKey, id))
				next.ServeHTTP(w, newR)
				return
			default:
				response := APIResponse{
					Success: false,
					Message: "Error reading cookie",
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(response)
				return
			}
		}

		anonID, ok := verifyAnonID(cookie.Value, app.secret)
		if !ok {
			id, anonIDCookie := generateAnonIDCookie(app.secret)
			newR := r.WithContext(context.WithValue(r.Context(), anonIDKey, id))
			http.SetCookie(w, anonIDCookie)
			next.ServeHTTP(w, newR)
			return
		}

		newR := r.WithContext(context.WithValue(r.Context(), anonIDKey, anonID))
		next.ServeHTTP(w, newR)
	})
}
