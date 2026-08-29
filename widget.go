package main

import (
	"context"
	"encoding/json"
	"errors"
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

func (likeWidget *LikeWidget) Validate(slug string) error {
	// allow all for now
	return nil
}

func (likeWidget *LikeWidget) Hit(ctx context.Context, slug string, anonID string, count int) (WidgetState, error) {
	if count < 1 {

		return WidgetState{}, ErrInvalidCount
	}

	redisKey := likeWidget.GenerateSessionKey(slug, anonID)
	val, err := likeWidget.redis.Get(ctx, redisKey).Result()
	if errors.Is(err, redis.Nil) {
		minCount := min(count, LIKE_MAX_PER_ANON_ID)
		newState := WidgetState{
			Count:     minCount,
			Capped:    minCount >= LIKE_MAX_PER_ANON_ID,
			Remaining: LIKE_MAX_PER_ANON_ID - minCount,
		}

		jsonRes, err := json.Marshal(newState)
		if err != nil {
			return newState, errors.New("Can't marshal new state")
		}

		_, setErr := likeWidget.redis.Set(ctx, redisKey, jsonRes, LIKE_TTL_PER_ANON_ID).Result()
		if setErr != nil {
			return newState, errors.New("Can't update count data")
		}
		return newState, nil
	}
	if err != nil {
		// redis is down
		newState := WidgetState{
			Count:     0,
			Capped:    true,
			Remaining: 0,
		}
		return newState, errors.New("Can't get count data")
	}

	var res WidgetState
	if err := json.Unmarshal([]byte(val), &res); err != nil {
		res = WidgetState{}
	}

	res.Count += min(count, res.Remaining)
	res.Capped = res.Count >= LIKE_MAX_PER_ANON_ID
	res.Remaining = LIKE_MAX_PER_ANON_ID - res.Count

	jsonRes, err := json.Marshal(res)
	if err != nil {
		return res, errors.New("Can't marshal new state")
	}

	_, updateError := likeWidget.redis.Set(ctx, redisKey, jsonRes, LIKE_TTL_PER_ANON_ID).Result()

	if updateError != nil {
		return res, updateError
	}

	return res, nil
}

func (likeWidget *LikeWidget) State(ctx context.Context, slug string, anonID string) (WidgetState, error) {
	redisKey := likeWidget.GenerateSessionKey(slug, anonID)
	val, err := likeWidget.redis.Get(ctx, redisKey).Result()
	if errors.Is(err, redis.Nil) {
		newState := WidgetState{
			Count:     0,
			Capped:    false,
			Remaining: LIKE_MAX_PER_ANON_ID,
		}

		return newState, nil
	}
	if err != nil {
		newState := WidgetState{
			Count:     0,
			Capped:    true,
			Remaining: 0,
		}
		return newState, errors.New("Can't get count data")
	}

	var res WidgetState
	if err := json.Unmarshal([]byte(val), &res); err != nil {
		res = WidgetState{}
	}

	return res, nil
}
