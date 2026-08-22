# External REST API

All management endpoints require `X-API-Key: <ESP_API_KEY>` and use JSON. The liveness endpoint `GET /api/v1/health` is public.

## Accounts

- `GET /api/v1/accounts`
- `POST /api/v1/accounts` — requires `Idempotency-Key`; `{ "username", "password", "expires_at": "RFC3339", "note" }`
- `PATCH /api/v1/accounts/{id}` — `{ "expires_at": "RFC3339", "note" }`
- `POST /api/v1/accounts/restrict-media` — disable transcoding, remuxing, downloads, subtitle downloads/management, media conversion, and camera uploads for every managed account.
- `POST /api/v1/accounts/{id}/enable`
- `POST /api/v1/accounts/{id}/disable`
- `DELETE /api/v1/accounts/{id}`

## Invite codes

- `GET /api/v1/invites`
- `POST /api/v1/invites` — `{ "duration_minutes": 60, "max_uses": 1, "note": "optional" }`

`duration_minutes` accepts 1 minute through 3,650 days and supports minute, hour, and day cards. `duration_days` remains accepted for backward compatibility; when both are supplied, `duration_minutes` takes precedence.
- `PATCH /api/v1/invites/{id}` — `{ "enabled": true }`
- `DELETE /api/v1/invites/{id}`
- `POST /api/v1/register` — requires `Idempotency-Key`; `{ "code", "username", "password" }`
- `POST /api/v1/renew` — `{ "code", "username", "password" }`; the existing managed-account password is required to prevent applying a code to the wrong user.

The raw invite code is returned only by `POST /api/v1/invites`. List responses expose only a prefix. `POST /api/v1/accounts` and `POST /api/v1/register` require a non-empty `Idempotency-Key` header. Repeating the same request with that key returns the original business account; reusing it with different input returns `409 Conflict`.

```bash
curl -X POST http://localhost:8080/api/v1/invites \
  -H 'X-API-Key: replace-with-ESP_API_KEY' \
  -H 'Content-Type: application/json' \
  -d '{"duration_minutes":60,"max_uses":1}'
```

A renewal always uses `original_expiry + duration_minutes`, even if that resulting date is still in the past.

## Media requests (求剧)

Request records aggregate per TMDB title: multiple users asking for the same movie/series share one record, and the lifecycle status (pending/fulfilled/rejected) applies to the whole record. Each record carries the `requesters` list and a `requester_count`.

- `GET /api/v1/requests` — query params: `status` (`pending`\|`fulfilled`\|`rejected`, empty = all), `tmdb_id` (exact match, useful for workflow dedupe), `q` (title / original title / requester username / tmdb id substring), `page` (1-based), `page_size` (default 20, max 100)

  Response: `{ "requests": [{ "id", "requesters": [{ "account_id", "account_username", "created_at" }], "requester_count", "tmdb_id", "media_type", "title", "original_title", "overview", "poster_path", "release_date", "kind", "episodes", "status", "created_at", "updated_at" }], "total", "pending", "fulfilled", "page", "page_size", "total_pages" }`
- `POST /api/v1/requests` — `{ "media_type": "movie"\|"tv", "tmdb_id": 157336, "account_id": 0 }`; the server re-fetches catalog details from TMDB and checks the Emby library (movie already in library → `409`, TMDB entry missing → `404`, TMDB not configured → `503`). `account_id` is optional attribution; when provided it must reference an existing business account (otherwise `400`) and the account joins the record as a requester. Re-submitting an existing title joins the requester list and resets the status to pending.
- `POST /api/v1/requests/{id}/fulfill` — mark 已入库 (closes the workflow loop after the media is added)
- `POST /api/v1/requests/{id}/reject` — mark 已驳回; unknown id → `404`
