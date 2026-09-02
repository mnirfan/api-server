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
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type APILikeWidgetRequest struct {
	Count int `json:"count"`
}

type APIWidgetResponse struct {
	Success bool         `json:"success"`
	Message string       `json:"message"`
	Data    *WidgetState `json:"data,omitempty"`
}

type Application struct {
	redis          *redis.Client
	secret         []byte
	widgetRegistry WidgetsRegistry
	allowedOrigin  string
}

type ctxKey string

type Middleware func(http.Handler) http.Handler

const anonIDKey ctxKey = "anonID"

func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

func main() {
	err := godotenv.Load()
	if err != nil {
		fmt.Println("[env] WARNING: No .env file is present")
	}

	secretKey := os.Getenv("SECRET_KEY")
	if secretKey == "" || len(secretKey) < 15 {
		panic("[env] secret key is not valid")
	}

	allowedOrigin := os.Getenv("ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		panic("[env] allowed origin is not valid")
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
		allowedOrigin:  allowedOrigin,
	}

	// Like widget
	likeWidget := LikeWidget{
		name:  "like-widget",
		redis: app.redis,
	}

	wr.Register("like-widget", &likeWidget)

	mux := http.NewServeMux()

	mux.Handle("OPTIONS /", http.HandlerFunc(app.optionsHandler))

	mux.Handle("GET /health", Chain(http.HandlerFunc(app.pingHandler), app.corsMiddleware, app.ipRateLimitMiddleware))
	mux.Handle("GET /widgets/{type}/{slug}", Chain(http.HandlerFunc(app.getLikeStateHandler), app.corsMiddleware, app.csrfMiddleware, app.ipRateLimitMiddleware, app.anonIDMiddleware, app.rateLimitMiddleware))
	mux.Handle("POST /widgets/{type}/{slug}/hit", Chain(http.HandlerFunc(app.hitLikeHandler), app.corsMiddleware, app.csrfMiddleware, app.ipRateLimitMiddleware, app.anonIDMiddleware, app.rateLimitMiddleware))

	if err := http.ListenAndServe(":8080", mux); err != nil {
		fmt.Println("Error running server:", err)
	}

}

func (app *Application) optionsHandler(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin != app.allowedOrigin {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", app.allowedOrigin)
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.WriteHeader(http.StatusNoContent)
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

	response.Success = true
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

	response.Success = true
	response.Message = "ok"
	response.Data = &state
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
