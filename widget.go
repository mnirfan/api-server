package main

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const LIKE_MAX_PER_ANON_ID = 20

const LIKE_TTL_PER_ANON_ID = 30 * 24 * time.Hour

type WidgetState struct {
	Count     int  `json:"count"`
	Capped    bool `json:"capped"`
	Remaining int  `json:"remaining"`
}

type Widget interface {
	Validate(slug string) error
	Hit(ctx context.Context, slug string, anonID string, count int) (WidgetState, error)
	State(ctx context.Context, slug string, anonID string) (WidgetState, error)
}

type WidgetsRegistry struct {
	widgets map[string]Widget
}

func NewWidgetRegistry() WidgetsRegistry {
	widgetRegistry := WidgetsRegistry{
		widgets: make(map[string]Widget),
	}

	return widgetRegistry
}

func (widgets *WidgetsRegistry) Register(name string, widget Widget) {
	widgets.widgets[name] = widget
}

func (widgets *WidgetsRegistry) Get(name string) Widget {
	return widgets.widgets[name]
}

// Like Widget

type LikeWidget struct {
	name  string
	redis *redis.Client
}

func (likeWidget *LikeWidget) GenerateSessionKey(slug string, anonID string) string {
	return "widget:" + likeWidget.name + ":" + slug + ":user:" + anonID
}

func (likeWidget *LikeWidget) GenerateGlobalKey(slug string) string {
	return "widget:" + likeWidget.name + ":" + slug + ":global"
}

func (likeWidget *LikeWidget) Validate(slug string) error {
	// allow all for now
	return nil
}

func (likeWidget *LikeWidget) Hit(ctx context.Context, slug string, anonID string, count int) (WidgetState, error) {
	if count < 1 {
		return WidgetState{}, ErrInvalidCount
	}

	redisGlobalKey := likeWidget.GenerateGlobalKey(slug)
	redisSessionKey := likeWidget.GenerateSessionKey(slug, anonID)

	var incrBy = redis.NewScript(`
		local sessionKey = KEYS[1]
		local globalKey = KEYS[2]
		local change = ARGV[1]
		local cap = ARGV[2]
		local ttl = ARGV[3]

		local globalValue = redis.call("GET", globalKey)
		globalValue = tonumber(globalValue or 0)

		local sessionValue = redis.call("GET", sessionKey)
		sessionValue = tonumber(sessionValue or 0)

		local remaining = cap - sessionValue
		change = math.min(change, remaining)

		globalValue = globalValue + change
		sessionValue = sessionValue + change
		redis.call("SET", globalKey, globalValue)
		redis.call("SET", sessionKey, sessionValue)
		redis.call("EXPIRE", sessionKey, ttl)

		return {globalValue, sessionValue}
	`)

	keys := []string{redisSessionKey, redisGlobalKey}
	values := []interface{}{count, LIKE_MAX_PER_ANON_ID, int(LIKE_TTL_PER_ANON_ID.Seconds())}
	countResults, updateError := incrBy.Run(ctx, likeWidget.redis, keys, values...).Result()
	if updateError != nil {
		return WidgetState{}, updateError
	}

	countResultSlice, ok := countResults.([]any)
	if !ok {
		return WidgetState{}, errors.New("Failed to get count update")
	}

	globalCount := int(countResultSlice[0].(int64))
	sessionCount := int(countResultSlice[1].(int64))
	sessionRemaining := LIKE_MAX_PER_ANON_ID - sessionCount
	capped := sessionRemaining < 1

	res := WidgetState{
		Count:     globalCount,
		Remaining: sessionRemaining,
		Capped:    capped,
	}

	return res, nil
}

func (likeWidget *LikeWidget) State(ctx context.Context, slug string, anonID string) (WidgetState, error) {
	redisGlobalKey := likeWidget.GenerateGlobalKey(slug)
	redisSessionKey := likeWidget.GenerateSessionKey(slug, anonID)

	countResults, err := likeWidget.redis.MGet(ctx, redisGlobalKey, redisSessionKey).Result()
	if err != nil {
		newState := WidgetState{
			Count:     0,
			Capped:    true,
			Remaining: 0,
		}
		return newState, errors.New("Can't get count data")
	}

	globalCountStr, globalCountStrOk := countResults[0].(string)
	if !globalCountStrOk {
		globalCountStr = "0"
	}

	sessionCountStr, sessionCountStrOk := countResults[1].(string)
	if !sessionCountStrOk {
		sessionCountStr = "0"
	}

	globalCount, globalCountErr := strconv.Atoi(globalCountStr)
	if globalCountErr != nil {
		return WidgetState{}, errors.New("Failed to parse global count data")
	}

	sessionCount, sessionCountErr := strconv.Atoi(sessionCountStr)
	if sessionCountErr != nil {
		return WidgetState{}, errors.New("Failed to parse session count data")
	}

	remaining := LIKE_MAX_PER_ANON_ID - sessionCount
	capped := remaining < 1

	res := WidgetState{
		Count:     globalCount,
		Capped:    capped,
		Remaining: remaining,
	}

	return res, nil
}
