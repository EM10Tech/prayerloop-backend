# prayerloop-backend — Architecture

**Role:** The single REST API in front of PostgreSQL. Owns authentication, authorization, business rules, push-notification fanout, and transactional email. Stateless — no session storage; horizontally scalable.

## Stack

- Go 1.18+, [Gin](https://github.com/gin-gonic/gin) HTTP router
- [`goqu`](https://github.com/doug-martin/goqu) SQL builder over `database/sql` + `lib/pq`
- `github.com/golang-jwt/jwt/v4` for HS256 JWTs
- Firebase Admin SDK for FCM push (`firebase-service-account.json` next to the binary)

## Layered Request Flow

```
HTTP request
   │
   ▼
main.go                         Route registration, middleware composition
   │
   ▼
middlewares/                    RateLimitMiddleware → CheckAuth (JWT, loads user_profile) → [CheckAdmin]
   │
   ▼
controllers/*.go                Request validation, goqu query construction, response shaping
   │
   ▼
models/*.go                     Structs with `db:"snake_case"` and `json:"camelCase"` tags
   │
   ▼
initializers/database.go        Singleton `*goqu.Database` (DB_URL env var)
   │
   ▼
PostgreSQL (prayerloop-psql)

services/                       Side-effect helpers reused across controllers:
  • emailService.go               SMTP (password resets, verification)
  • pushNotificationService.go    FCM fanout via stored device tokens
  • notificationTriggerService.go Persists notification rows + atomic debounce window
```

## Routing Surface (`main.go`)

- **Public:** `/login`, `/signup`, `/check-username`, `/ping`, `/auth/forgot-password`, `/auth/verify-reset-code`, `/auth/reset-password`, `/privacy`, `/static/*`.
- **Authenticated** (`auth` group, `CheckAuth` + 10 rps rate limit): users, groups, prayers, prayer subjects, categories, comments, notifications, connection requests, prayer analytics, push-token registration.
- **Admin** (`admin` group, `CheckAdmin` + 5 rps): cross-tenant prayer reads, internal user creation, broadcast push.

## Directory Map

```
controllers/        One file per resource (prayerController, groupController, …) plus tests
middlewares/        checkAuth, checkAdmin, rateLimit
models/             DTO/DB structs — also the JSON wire format
initializers/       loadEnv (dotenv), database (goqu)
services/           email, push notifications, notification trigger/debounce
static/             privacy.html and static assets
```

## Key Conventions

- All authenticated controllers expect `currentUser` (and `admin`) on the Gin context — set by `CheckAuth`.
- Database access goes exclusively through `initializers.DB` (a `*goqu.Database`); the raw `*sql.DB` is not exposed.
- Models double as the JSON wire format. New columns require a migration in `prayerloop-psql` **and** the matching struct field here.
- Tests sit alongside the code (`*_test.go`, `fixtures.go`, `test_helpers.go`); `go test ./...` runs them.
- Rate-limit keys are by `c.FullPath()` in debug mode and `c.ClientIP()` in production.

## Configuration

Env vars (loaded by `initializers/loadEnv.go`):
- `DB_URL` — full PostgreSQL DSN
- `SECRET` — HS256 JWT signing key
- SMTP credentials consumed by `emailService.go`
- `firebase-service-account.json` — service account file mounted next to the binary (FCM)

## Deploy

`Dockerfile` builds the binary; merging to `main` triggers a GitHub Actions workflow that builds the image and deploys to EC2 (currently `dev.prayerloop.io`).
