package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

const RATE_LIMIT_PER_IP = 100
const RATE_LIMIT_WINDOW_PER_IP = 60 * time.Second

const RATE_LIMIT_PER_IP_ID = 2
const RATE_LIMIT_WINDOW_PER_IP_ID = 1 * time.Second

func generateIPRateLimitKey(ip string) string {
	return "ratelimit:" + ip
}

func generateRateLimitKey(ip string, anonID string) string {
	return "ratelimit:" + ip + ":" + anonID
}

func getIPAddress(r *http.Request) (string, error) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	return ip, err
}

func (app *Application) checkRateLimit(ctx context.Context, key string, window time.Duration, max int) (bool, error) {
	script := redis.NewScript(`
		local rateLimitKey = KEYS[1]
		local window = tonumber(ARGV[1] or 0)
		local max = tonumber(ARGV[2] or 0)

		local currentRateLimit = redis.call("GET", rateLimitKey)
		currentRateLimit = tonumber(currentRateLimit or 0)

		if currentRateLimit >= max then
			return 1
		end

		currentRateLimit = currentRateLimit + 1
		redis.call("SET", rateLimitKey, currentRateLimit)
		redis.call("EXPIRE", rateLimitKey, window)
		return 0
	`)

	keys := []string{key}
	values := []interface{}{window.Seconds(), max}
	isLimited, err := script.Run(ctx, app.redis, keys, values...).Int()
	if err != nil {
		return true, errors.New("Rate limit check failed")
	}

	return isLimited == 1, nil
}

func (app *Application) ipRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := APIResponse{
			Success: false,
			Message: "Too many request",
		}

		ip, err := getIPAddress(r)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			response.Message = "requester not identifiable"
			json.NewEncoder(w).Encode(response)
			return
		}

		isLimited, err := app.checkRateLimit(
			r.Context(),
			generateIPRateLimitKey(ip),
			RATE_LIMIT_WINDOW_PER_IP,
			RATE_LIMIT_PER_IP,
		)

		if err != nil {
			response.Message = err.Error()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(response)
			return
		}

		if isLimited {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(response)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (app *Application) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := APIResponse{
			Success: false,
			Message: "Too many request",
		}

		ip, err := getIPAddress(r)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			response.Message = "requester not identifiable"
			json.NewEncoder(w).Encode(response)
			return
		}

		anonID, ok := anonIDFromContext(r)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(response)
			return
		}

		isLimited, err := app.checkRateLimit(
			r.Context(),
			generateRateLimitKey(ip, anonID),
			RATE_LIMIT_WINDOW_PER_IP_ID,
			RATE_LIMIT_PER_IP_ID,
		)

		if err != nil {
			response.Message = err.Error()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(response)
			return
		}

		if isLimited {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(response)
			return
		}

		next.ServeHTTP(w, r)
	})
}
