package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

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
	redis *redis.Client
}

func getUser(w http.ResponseWriter, r *http.Request) {
	user := User{ID: 1, Name: "Nurul"}
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(user); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

func main() {

	opt, err := redis.ParseURL("redis://localhost:6379/0")
	if err != nil {
		panic(err)
	}

	rdb := redis.NewClient(opt)

	app := &Application{
		redis: rdb,
	}

	http.HandleFunc("/user", getUser)
	http.HandleFunc("/health", app.pingHandler)
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
