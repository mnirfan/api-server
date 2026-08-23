package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type APIResponse struct {
	Success bool   `json:"status"`
	Message string `json:"message"`
}

type Application struct {
	redis  *redis.Client
	secret []byte
}

type ctxKey string

const anonIDKey ctxKey = "anonID"

func getUser(w http.ResponseWriter, r *http.Request) {
	user := User{ID: 1, Name: "Nurul"}
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(user); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

func main() {
	err := godotenv.Load()
	if err != nil {
		fmt.Println("[env] WARNING: No .env file is present")
	}

	secretKey := os.Getenv("SECRET_KEY")
	if secretKey == "" || len(secretKey) < 15 {
		panic("secret key not valid")
	}

	opt, err := redis.ParseURL("redis://localhost:6379/0")
	if err != nil {
		panic(err)
	}

	rdb := redis.NewClient(opt)

	app := &Application{
		redis:  rdb,
		secret: []byte(secretKey),
	}

	http.HandleFunc("/user", getUser)
	http.Handle("/health", app.anonIDMiddleware(http.HandlerFunc(app.pingHandler)))
	log.Fatal(http.ListenAndServe(":8080", nil))

}

func (app *Application) pingHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 1000*time.Millisecond)
	defer cancel()

	_, err := app.redis.Ping(ctx).Result()

	if err != nil {
		response := APIResponse{
			Success: false,
			Message: "redis connection error",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(response)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	response := APIResponse{
		Success: true,
		Message: "Ok",
	}

	json.NewEncoder(w).Encode(response)
}

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
