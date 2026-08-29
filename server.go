package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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

type APILikeWidgetRequest struct {
	Count int `json:"count"`
}

type APIWidgetResponse struct {
	Success bool         `json:"status"`
	Message string       `json:"message"`
	Data    *WidgetState `json:"data,omitempty"`
}

type Application struct {
	redis          *redis.Client
	secret         []byte
	widgetRegistry WidgetsRegistry
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

	// Widget Registration
	wr := NewWidgetRegistry()

	app := &Application{
		redis:          rdb,
		secret:         []byte(secretKey),
		widgetRegistry: wr,
	}

	// Like widget
	likeWidget := LikeWidget{
		name:  "like-widget",
		redis: app.redis,
	}

	wr.Register("like-widget", &likeWidget)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /user", getUser)
	mux.Handle("GET /health", app.anonIDMiddleware(http.HandlerFunc(app.pingHandler)))
	mux.Handle("GET /widgets/{type}/{slug}", app.anonIDMiddleware(http.HandlerFunc(app.getLikeStateHandler)))
	mux.Handle("POST /widgets/{type}/{slug}/hit", app.anonIDMiddleware(http.HandlerFunc(app.hitLikeHandler)))

	if err := http.ListenAndServe(":8080", mux); err != nil {
		fmt.Println("Error running server:", err)
	}

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

func (app *Application) hitLikeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	response := APIWidgetResponse{
		Success: false,
		Message: "error",
		Data:    nil,
	}

	widgetType := r.PathValue("type")
	widget := app.widgetRegistry.Get(widgetType)
	if widget == nil {
		w.WriteHeader(http.StatusNotFound)
		response.Message = "widget not found"
		json.NewEncoder(w).Encode(response)
		return
	}
	anonID, ok := anonIDFromContext(r)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(response)
		return
	}

	var requestBody APILikeWidgetRequest
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		response.Message = "invalid parameters"
		json.NewEncoder(w).Encode(response)
		return
	}

	state, err := widget.Hit(r.Context(), r.PathValue("slug"), anonID, requestBody.Count)
	if errors.Is(err, ErrInvalidCount) {
		w.WriteHeader(http.StatusBadRequest)
		response.Message = err.Error()
		json.NewEncoder(w).Encode(response)
		return
	}

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		response.Message = err.Error()
		json.NewEncoder(w).Encode(response)
		return
	}

	response.Message = "ok"
	response.Data = &state
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (app *Application) getLikeStateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	response := APIWidgetResponse{
		Success: false,
		Message: "error",
		Data:    nil,
	}

	widgetType := r.PathValue("type")
	widget := app.widgetRegistry.Get(widgetType)
	if widget == nil {
		w.WriteHeader(http.StatusNotFound)
		response.Message = "widget not found"
		json.NewEncoder(w).Encode(response)
		return
	}
	anonID, ok := anonIDFromContext(r)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(response)
		return
	}

	state, err := widget.State(r.Context(), r.PathValue("slug"), anonID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		response.Message = err.Error()
		json.NewEncoder(w).Encode(response)
		return
	}

	response.Message = "ok"
	response.Data = &state
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
