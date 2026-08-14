# Playful Widgets API — Design Spec

**Date:** 2026-08-14
**Status:** Approved for planning
**Purpose:** Learning-focused backend project. REST + WebSocket + Redis API serving small interactive "widgets" embedded in a static blog. First widget: a hit/like button with a per-visitor lifetime cap.

## Goals

- Generic widget framework: pluggable widget types behind a common registry, not hard-coded to one widget.
- First concrete widget: `like_button` — anonymous visitors can hit it multiple times, up to a configured cap (e.g. 20), then further hits are no-ops.
- Live count broadcast via WebSocket to all visitors viewing the same post.
- Guarded against common web attacks (CSRF, cross-origin abuse, request flooding, key-space spam) appropriate to a learning project's scope.
- Single server instance, Redis as the only datastore (counts + rate-limit + persistence via Redis AOF).

## Non-Goals (v1)

- Multi-instance / horizontally scaled deployment (no Redis pub/sub fan-out for WS).
- Authenticated (non-anonymous) users.
- Durable secondary datastore (Postgres) — Redis persistence is sufficient for this scope.
- Bot detection beyond rate-limiting and cap enforcement.
- Admin API for pre-registering widget instances — instances are implicit, created on first hit.

## Architecture

Go REST+WS server, single process, backed by Redis.

**Layers:**
- **Widget registry** — in-process map of widget type name → handler implementing a common interface (validate config, handle hit, get state). v1 registers one type: `like_button`.
- **REST API** — hit endpoint and state-fetch endpoint.
- **WS hub** — in-memory (no cross-instance fan-out needed at this scale). Tracks subscribers per widget instance (`type:slug`), broadcasts count updates to all of them on every hit.
- **Middleware chain** (applied in order): CORS allowlist → CSRF origin check → anon-ID resolution (cookie) → input validation (type/slug) → rate-limit (Redis) → handler.
- **Redis** — source of truth for global counts, per-user hit tallies, and rate-limit buckets.

## Widget Registry Interface

Each widget type implements:
- `Validate(slug string) error` — format/allowlist checks specific to the type (v1: shared slug regex).
- `Hit(ctx, slug string, anonID string, count int) (WidgetState, error)` — apply a batched hit, return new state.
- `State(ctx, slug string, anonID string) (WidgetState, error)` — read current state for a given viewer.

`WidgetState` = `{count int, capped bool, remaining int}`.

This keeps `like_button` as one implementation among potentially many (poll, reaction, counter, etc.) without those needing design work now.

## REST API

### `GET /widgets/{type}/{slug}`
Returns current state for the calling anon ID.
Response: `200 {count, capped, remaining}`

### `POST /widgets/{type}/{slug}/hit`
Body: `{count: N}` — the number of hits to apply in this request (client debounces rapid clicks and batches them; `1 ≤ N ≤ cap` for the widget type, else `400`).

Server applies the hit atomically (see Redis Key Schema), clamping to whatever remains of the visitor's cap, and returns:
`200 {count, capped, remaining}`

- `count` — new global count for this widget instance (after this request's contribution).
- `capped` — true if this visitor has now reached (or already reached) their cap.
- `remaining` — hits this visitor has left before their cap.

Reaching the cap is **not** an error — it's an expected end state, returned as `200` with `capped: true` and no further count change on subsequent calls.

### `GET /widgets/{type}/{slug}/ws`
WebSocket upgrade. Same CORS-origin check as REST. Joins the broadcast room for `{type}:{slug}`. Receives `{count}` push messages whenever any visitor hits this widget instance. Connection cap per IP (e.g. 5 concurrent) to bound socket exhaustion.

### Validation
- `{type}` — must exist in the widget registry, else `400`.
- `{slug}` — must match `^[a-z0-9-]{1,100}$`, else `400`. This closes the arbitrary-key-spam risk that comes from letting the client name widget instances freely (no pre-registration in v1).

## Data Model — Redis Key Schema

Per widget instance (`{type}`, `{slug}`):

| Key | Purpose | TTL |
|---|---|---|
| `widget:{type}:{slug}:count` | Global hit count | none (permanent) |
| `widget:{type}:{slug}:user:{anonID}` | This visitor's hit count for this instance | 30 days — matches anon-ID cookie expiry |
| `ratelimit:{anonID}:{ip}` | Technical request-rate throttle bucket | short (window-sized, e.g. 1s) |

**Why the user-hit-count key has a TTL and the global count doesn't:** the cap is meant to last as long as the visitor's anon ID is valid, not forever. Once the anon-ID cookie expires (30 days), the browser drops it and a returning visitor gets a fresh ID with a fresh cap anyway — so the old per-user key has no further purpose and Redis reaps it automatically. The global count is the permanent public metric for the post and must never expire.

## Atomic Hit Application (Lua script)

A batched hit (`count: N`) must clamp against the visitor's remaining cap and update both the global and per-user keys atomically — a plain read-then-write from application code races under concurrent requests from the same visitor (e.g. two tabs open).

Use a Redis Lua script (`EVAL`) that, in one atomic step:
1. Reads current per-user hit count (default 0 if unset).
2. Computes `allowed = max(0, cap - current)`, `actual = min(N, allowed)`.
3. If `actual > 0`: `INCRBY` both the global count key and the per-user key by `actual`; set/refresh the per-user key's TTL to 30 days.
4. Returns `{newGlobalCount, actual, current + actual >= cap}`.

This is also a deliberate learning target for atomic Redis scripting.

## Anonymous Identity

- Signed cookie (HMAC with a server secret), issued on first request if absent.
- Attributes: `Secure; HttpOnly; SameSite=None` (required since the API is cross-origin from the static blog) with a 30-day expiry.
- Rate-limit and hit-cap both key off this ID, with client IP as a secondary signal for the rate-limit bucket.

## Security

- **CORS** — allowlist the blog's origin only; `Access-Control-Allow-Credentials: true` (cookie requires this).
- **CSRF** — reject `POST` requests whose `Origin` header is missing or not in the allowlist.
- **Rate-limit** — token bucket per `(anonID, IP)`, 1 request/second, Redis-backed. `429` on breach.
- **Input validation** — widget-type allowlist and slug regex (above), plus `1 ≤ count ≤ cap` on hit requests.
- **WS abuse guard** — same origin check on upgrade; per-IP concurrent-connection cap.
- **No internal leakage** — `500` responses return a generic message; details logged server-side only.

### Error Responses

| Status | Cause |
|---|---|
| `400` | Unknown widget type, invalid slug, invalid `count` |
| `403` | CSRF origin check failed |
| `429` | Rate-limit exceeded |
| `200` (`capped: true`) | Cap already reached — expected state, not an error |
| `500` | Redis/internal error (generic message, logged detail) |

## Configuration

- `LIKE_BUTTON_MAX_HITS` (default 20) — per-widget-type cap, server-side env config. Not client-overridable in v1.
- Blog origin(s) for CORS/CSRF allowlist — server config.
- Anon-ID cookie signing secret — server config.

## Testing Plan

- **Unit** — registry lookup, slug/type validators, Lua script behavior (via `miniredis` or real Redis in CI), cookie sign/verify.
- **Middleware** — CORS allow/reject, CSRF origin check, rate-limit enforcement (1 req/s).
- **Integration** — fresh anon ID → hits accumulate → cap reached → further hits return `capped:true` with unchanged count → per-user key TTL behavior (short TTL in test env).
- **WS** — connect and receive broadcast triggered by another client's hit; per-IP connection-cap enforcement.

## Open Items Deferred Past v1

- Multi-instance deployment (Redis pub/sub fan-out for WS).
- Additional widget types beyond `like_button`.
- Admin/pre-registration flow for widget instances (would remove the need for slug-regex-only validation).
