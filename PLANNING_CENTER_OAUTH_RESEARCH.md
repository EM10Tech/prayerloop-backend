# Planning Center OAuth + Social Login — Engineering Research & Design

> **Status:** Research / planning only — no code changes have been made.
> **Date:** 2026-06-30
>
> ⚠️ **2026-07-07 addendum (§L): Church Center members CANNOT log in via PCO OAuth.** The congregant-login premise behind Scenario 1's reach is invalid — PCO OAuth authenticates Planning Center *users* (admin-granted product access: staff/volunteers) only; Church Center is a separate, passwordless identity system. Verified empirically and against PCO sources. See §L for the finding, evidence, and the revised (data-level) church integration direction.
> ⚠️ **2026-07-08 addendum (§M): Planning Center deprioritized; Google/Apple are next.** Phase 0 (partial)/Phase 1 shipped (PC login + email-collision interstitial) and stays as-is. The §L.3 org-admin/groups-sync direction is put on hold rather than actively designed further. New work goes into `GoogleProvider`/`AppleProvider` against the same provider-agnostic interface/schema/endpoints already in place. See §M.
> **Scope:** Add Planning Center Online (PCO) OAuth login + account linking to prayerloop, with Google/Apple social login as a follow-on. Spans three repos: `prayerloop-mobile`, `prayerloop-backend`, `prayerloop-psql`.
>
> Confidence tags used throughout: **[OFFICIAL]** = confirmed in PCO/Apple/Expo docs · **[UNVERIFIED]** = could not confirm from official docs, test empirically before relying on it. Claims were produced by a multi-agent research pass and then adversarially verified; verifier corrections are folded in.

---

## A. Bottom line / recommended architecture

- **Backend-mediated OAuth (Backend-for-Frontend).** The Expo app runs PCO's `/oauth/authorize` step with **PKCE (S256)** and hands the resulting authorization `code` to a new prayerloop-backend endpoint. The **backend** (a confidential client holding `PC_CLIENT_SECRET`) performs the token exchange, fetches identity, provisions/links the account, and mints prayerloop's **own** JWT.
  - This is **forced**, not just preferred: PCO's token endpoint requires `client_secret` for both `authorization_code` and `refresh_token` grants (it does not advertise the `none` auth method), and a mobile app is a public client (RFC 8252) that cannot safely embed a secret. On-device token exchange is therefore not an option. **[OFFICIAL]**
- **Reuse the existing prayerloop JWT unchanged.** OAuth login mints the *same* HS256 token (`{id, exp, role}`, 24h, signed with `SECRET`) via a shared helper. `middlewares/checkAuth.go` and the entire authenticated request path need **zero changes**.
- **New identity table keyed on `(provider, sub)`, never on email.** Email is descriptive metadata only.
- **Schema unblock:** make `user_profile.password` nullable, and synthesize a unique `username` on auto-create (both `password` and `username` are `NOT NULL` today; `username` is also `UNIQUE`; `first_name`/`last_name` are `NOT NULL`).
- **Replace the broken "re-login" pattern with server-side refresh tokens.** Today the app stores the **raw password in AsyncStorage** (`util/reLogin.ts`) and replays `POST /login` on 401 — impossible for OAuth users (no password). Introduce prayerloop-issued, rotating, DB-hashed refresh tokens + `POST /auth/refresh`, stored in `expo-secure-store`. This is a **security win for password users too**.
- **Ship Sign in with Apple on iOS** (App Store Guideline 4.8 — see §F.6 for the precise rule), and **never silently auto-merge** a PCO login into an existing password account — PCO provides **no `email_verified` claim**, so its email can never be trusted to merge accounts.

---

## B. Direct answers to the four driving questions

### Q1 — Do we need a schema change? How do we flag PC-linked accounts?

**Yes — two mandatory changes; the "flag" should be derived, not stored.**

1. **Make `password` nullable** (`prayerloop-psql/definitions/user_profile.sql:6` is `password VARCHAR(255) NOT NULL`):
   ```sql
   ALTER TABLE "user_profile" ALTER COLUMN password DROP NOT NULL;
   ```
2. **Add `user_external_identity`** (DDL in §E.2) to record the link + store the PCO `sub` and encrypted tokens.
3. **(Second blocker)** `username VARCHAR(255) NOT NULL UNIQUE` and `first_name`/`last_name NOT NULL` mean the auto-create path must **synthesize a unique username** (e.g. `pc_<sub>`) and populate names from the provider profile. No column change, but the backend must handle it.

**Do NOT add an `is_planning_center_linked` boolean to `user_profile`. Derive it** from `user_external_identity` and expose a computed `linkedProviders: ['planning_center']` array via the API. A boolean drifts (must be re-synced on every link/unlink/cascade-delete); the link table is the single source of truth and the lookup is an indexed `EXISTS` probe that scales to Google/Apple for free. If an admin dashboard ever needs a queryable column, prefer a read-only SQL VIEW over a trigger-maintained boolean.

### Q2 — What if a PC user belongs to several churches/organizations?

**Authenticate first; treat multi-church as future work — but don't bake a 1:1 assumption into the schema.**

- A PCO token is scoped to **exactly one organization**: the OIDC id_token carries a single `organization_id` + `organization_name`. One token does **not** span all orgs a person belongs to. **[OFFICIAL]**
- There is **no documented "list all my organizations" endpoint** from a single token. **[UNVERIFIED — assume none.]** The real-world pattern is **authorize-per-organization**: the user runs OAuth once per church and you receive a distinct token (distinct `organization_id`) each time. PCO guidance is to create **one** OAuth application for the whole product; the *tokens* are per-org, not the app.
- **Cheap insurance now:** include the nullable `organization_id` column on `user_external_identity` and keep v1 uniqueness as `UNIQUE(user_profile_id, provider)`. The single future migration to support multi-church is to relax that to `UNIQUE(user_profile_id, provider, organization_id)`. Pulling PC *groups* for an org is deferred (needs the `groups` scope + the stored refresh token).

### Q3 — What callback URLs in PC? Do we need Expo plugins for the mobile callback?

**Register an exact-match `redirect_uri`** at `https://api.planningcenteronline.com/oauth/applications`. Two viable architectures, both keeping the secret server-side:

- **Variant A — HTTPS backend callback (recommended default).** Register `https://<backend>/auth/oauth/planningcenter/callback` (one per environment). PCO redirects to the backend, which exchanges the code and then deep-links back into the app via `prayerloop://oauth-callback?code=<one-time>` (a short-lived one-time code, **never** the JWT). Works regardless of what PCO allows, and sidesteps the dev/prod scheme-collision below.
- **Variant B — custom scheme (fewer hops).** Register `prayerloop://oauth-callback`; the app captures the code and POSTs it to the backend.

> ⚠️ **Whether PCO accepts custom-scheme redirect URIs (vs HTTPS-only) is NOT documented** — only `https://` examples appear in their docs. **[UNVERIFIED]** ~10-minute test: register a test app and try a `prayerloop://` redirect. **Plan for Variant A** so the design doesn't depend on the unverified capability.

**Expo plugins for the redirect itself: none needed.** The app already declares `scheme: "prayerloop"` (`app.config.ts:12`) and an Android catch-all `scheme:"prayerloop"` intent filter (`app.config.ts:~58-67`), which already routes `prayerloop://oauth-callback`. `expo-auth-session`/`expo-web-browser` capture the redirect *before* expo-router sees it, so there is **no collision** with the existing `prayerloop://join-group` deep link — provided you use a dedicated **unrouted** path (`oauth-callback`) and **do not** create an `app/oauth-callback.tsx` screen.

> ⚠️ **Multi-environment caveat:** dev and prod builds have distinct bundle IDs (`com.prayerloop.app.dev` vs `com.prayerloop.app`) but **share `scheme: "prayerloop"`**, so `prayerloop://oauth-callback` is ambiguous if both apps are installed on one device (Android may prompt / route nondeterministically). Variant A avoids this by registering a per-environment backend URL. Also note `makeRedirectUri` returns an `exp://` proxy under Expo Go vs `prayerloop://` in a dev-client — a single `PC_REDIRECT_URI` env var cannot cover all cases; register every environment's redirect explicitly.

Plugins **are** required for the social-login native modules (Apple, Google, SecureStore) — see §F.7.

### Q4 — Login page design (differentiate PC vs prayerloop)

Keep email/password as the **primary** top block (existing green `#008000` identity), an "or continue with" divider, then visually distinct provider buttons:

```
┌──────────────────────────────────────────┐
│        ╭────────────────────────────╮      │
│        │  Welcome!                  │      │
│        │  Sign in to prayerloop     │      │
│        │  ┌──────────────────────┐  │      │
│        │  │ Email                │  │      │  ← native prayerloop
│        │  └──────────────────────┘  │      │    (PRIMARY, green)
│        │  ┌──────────────────────┐  │      │
│        │  │ Password         (•) │  │      │
│        │  └──────────────────────┘  │      │
│        │             Forgot Password?│     │
│        │  ┌──────────────────────┐  │      │
│        │  │       Sign In        │  │  ◀── green #008000
│        │  └──────────────────────┘  │      │
│        │  ──────  or continue with ──────  │  ◀── divider
│        │  ┌──────────────────────┐  │      │
│        │  │ [PC]  Continue with  │  │  ◀── PC brand blue (verify hex)
│        │  │       Planning Center│  │      │
│        │  └──────────────────────┘  │      │
│        │   Use your church's        │      │  ◀── helper microcopy
│        │   Planning Center login.   │      │
│        │  ┌──────────────────────┐  │      │
│        │  │  []  Sign in         │  │  ◀── black, iOS only
│        │  │      with Apple      │  │      │
│        │  └──────────────────────┘  │      │
│        │  ┌──────────────────────┐  │      │
│        │  │ [G]  Continue with   │  │  ◀── white/outlined
│        │  │      Google          │  │      │
│        │  └──────────────────────┘  │      │
│        │   Don't have an account?   │      │
│        │          Sign Up           │      │
│        ╰────────────────────────────╯      │
└──────────────────────────────────────────┘
```

Differentiators: **brand colors** (never green for OAuth — PC brand blue, Apple black, Google white/outlined), **verb + logo** ("Continue with Planning Center"), and **helper microcopy** ("Use your church's Planning Center login"). The strongest built-in signal is the **system browser sheet showing `planningcenteronline.com`** — users see they're authenticating with their church's system, and never type a PC password into a prayerloop field. Wrap the (now taller) form in a `ScrollView` (matches `SignupView`).

---

## C. End-to-end flows for the four scenarios

`App` = Expo client · `BE` = prayerloop-backend · `PCO` = Planning Center · `sub` = provider stable subject id.

### Scenario 1 — New PC user installs → logs in with PC → auto-create + link
```
App                          PCO                         BE                         DB
 |  tap "Continue with PC"    |                           |                          |
 |  generate PKCE + state     |                           |                          |
 |-- open browser: /oauth/authorize?client_id&redirect_uri&response_type=code        |
 |    &scope=openid&code_challenge&code_challenge_method=S256&state&nonce ----------->|
 |                            |  user logs in & consents  |                          |
 |<- redirect prayerloop://oauth-callback?code&state -----|                          |
 |  verify state == request.state                         |                          |
 |-- POST /auth/oauth/planningcenter/login {code, code_verifier, redirect_uri} ----->|
 |                            |   exchange code --------->| (POST /oauth/token w/ secret + code_verifier)
 |                            |<- {access, refresh, expires_in=7200} ----------------|
 |                            |   fetch identity -------->| (GET /oauth/userinfo or /people/v2/me)
 |                            |<- {sub, name, email, organization_id} ---------------|
 |                            |   lookup (planning_center, sub) -> NONE              |
 |                            |   probe user_profile by email                       |
 |                            |    -> NONE (else collision interstitial, see §H)     |
 |                            |   [TX] INSERT user_profile (password NULL,           |
 |                            |        username=pc_<sub>, first/last, email,         |
 |                            |        email_verified=FALSE, by=1)                   |
 |                            |        GetOrCreateSelfPrayerSubject; SendWelcomeEmail |
 |                            |        INSERT user_external_identity (enc tokens, org)|
 |                            |        issue JWT + refresh token  [/TX]              |
 |<- 200 {token, refreshToken, user} ---------------------|                          |
 |  store JWT (Redux), refreshToken (expo-secure-store); route to app                |
```

### Scenario 2 — Existing logged-in prayerloop user links PC
```
App (has prayerloop JWT)            PCO                BE                          DB
 |  Settings -> Linked Accounts -> "Link" Planning Center
 |  same authorize+PKCE browser flow ------------------>|                           |
 |<- prayerloop://oauth-callback?code&state ------------|                           |
 |-- POST /auth/oauth/planningcenter/link (Bearer <prayerloop JWT>)
 |        {code, code_verifier, redirect_uri} --------->|  currentUser from CheckAuth|
 |                                    | exchange, fetch /me -> sub                   |
 |                                    |   lookup (planning_center, sub):             |
 |                                    |    - bound to DIFFERENT user -> 409          |
 |                                    |    - bound to currentUser    -> idempotent   |
 |                                    |    - none -> INSERT identity for currentUser |
 |                                    |       (uq_uei_user_provider blocks 2nd PC)   |
 |<- 200 {user with linkedProviders} ------------------|                           |
```

### Scenario 3 — Login with EITHER email/password OR PC
- **Email/password:** `POST /login` → load `user_profile`; **if `password IS NULL` → 401** ("this account uses social login; sign in with your provider or set a password"); else bcrypt compare → JWT + refresh token.
- **Planning Center:** lookup `(planning_center, sub)` → load existing user (returning), or fall into Scenario 1 (first time).

### Scenario 4 — Unlink PC
```
App (authenticated)                 BE                              DB
 |  Settings -> Linked Accounts -> Unlink -> confirm
 |-- DELETE /auth/oauth/planningcenter/link (Bearer JWT) -->|
 |                       guard: has_password OR identity_count > 1 ?
 |                         - NO  -> 409 "set a password first"
 |                         - YES -> best-effort revoke token at PCO (/oauth/revoke)
 |                                  DELETE user_external_identity row (destroys tokens)
 |<- 200 {user with linkedProviders} ----------------------|
```

---

## D. Backend changes (`prayerloop-backend`)

**Design decision:** the backend receives the authorization `code` and exchanges it server-side. It holds `PC_CLIENT_SECRET`, calls PCO's token endpoint, fetches identity, provisions/links the user, persists encrypted PCO tokens, and returns a prayerloop JWT.

### New endpoints (provider-parameterized so Google/Apple need no new routes)
```go
// Public — near the existing /auth/* block (after main.go:47):
router.POST("/auth/oauth/:provider/login", middlewares.RateLimitMiddleware(2,2,getKey), controllers.OAuthLogin)   // scenarios 1 & 3
router.GET ("/auth/oauth/:provider/start", middlewares.RateLimitMiddleware(5,5,getKey), controllers.OAuthStart)   // optional (backend-owned authorize)
router.POST("/auth/refresh",               middlewares.RateLimitMiddleware(5,5,getKey), controllers.RefreshAccessToken)
router.POST("/auth/logout",                middlewares.RateLimitMiddleware(5,5,getKey), controllers.RevokeRefreshToken)

// Authenticated — inside the auth group (after main.go:58-61):
auth.POST  ("/auth/oauth/:provider/link",  controllers.OAuthLink)         // scenario 2
auth.DELETE("/auth/oauth/:provider/link",  controllers.OAuthUnlink)       // scenario 4
auth.GET   ("/users/me/identities",        controllers.ListUserIdentities)
```
Add `services.InitOAuthService()` to `init()` (`main.go:14-19`). Gin routing is safe (siblings under `/auth/` are static; the only `:provider` wildcard lives one level deeper with a consistent param name).

### New files
- **`controllers/oauthController.go`** — `OAuthLogin / OAuthStart / OAuthLink / OAuthUnlink / ListUserIdentities / RefreshAccessToken / RevokeRefreshToken`.
- **`controllers/authHelpers.go`** — shared extractions:
  - `generateAccessToken(user) (string, error)` — extract verbatim from `UserLogin` **lines 307–330** (role string, `jwt.NewWithClaims(HS256, {id,exp,role})`, `exp = now+24h`, `SignedString([]byte(os.Getenv("SECRET")))`). Single source of JWT issuance → emitted token is byte-identical → **CheckAuth unaffected**.
  - `createBaseUser(...)` — extract from `PublicUserSignup` **lines 84–137** (bcrypt, build `UserProfile`, insert + `Returning`, `GetOrCreateSelfPrayerSubject` at **1635–1689**, `SendWelcomeEmail`). Note `Email_Verified`/`Admin` carry `goqu:"skipinsert"`, so setting fields like `email_verified` requires a `goqu.Record` or insert-then-update.
  - `issueRefreshToken(userID)` and `validateAndRotateRefreshToken(plaintext)`.
- **`services/oauthService.go`** — provider interface + `PlanningCenterProvider` (authorize-URL build, `ExchangeCode`, `Refresh`, `FetchIdentity` via `/oauth/userinfo` or `/people/v2/me`), env-driven config, PCO wire structs.
- **`services/crypto.go`** — AES-256-GCM `EncryptToken`/`DecryptToken` (Go stdlib `crypto/aes`+`crypto/cipher`, random nonce prepended, base64 out). **No new dependency.**
- **`models/userIdentity.go`** — `UserIdentity` struct + `OAuthCodeRequest{Code, RedirectURI, CodeVerifier}`.
- **`models/authToken.go`** — `AuthRefreshToken` struct + `RefreshTokenRequest`.

### `OAuthLogin` algorithm (scenarios 1 & 3)
1. Resolve provider; reject unknown.
2. `ExchangeCode(code, redirectURI, codeVerifier)` → access/refresh/expiry/scopes.
3. `FetchIdentity(accessToken)` → `sub, email, firstName, lastName`.
4. Look up `user_external_identity` by `(provider, provider_user_id=sub)`:
   - **Found** → re-encrypt+update tokens; `generateAccessToken` + `issueRefreshToken`; return `{token, refreshToken, user}`.
   - **Not found** + email matches an existing `user_profile.email` → **security interstitial, do NOT silently merge** (see §H, and the pending-link mechanism in §I).
   - **Not found** + no email match → `createBaseUser(...)` then insert identity (scenario 1).
5. **Wrap create+link in a transaction** and recover from `UNIQUE(provider, sub)` violations (concurrent double-tap) by re-looking-up and returning the existing user — see §I gaps.

### Ripple edits from making `Password *string`
- `userController.go:301` (`UserLogin`) — **nil-guard**: if `dbUser.Password == nil` return 401, else `bcrypt.CompareHashAndPassword([]byte(*dbUser.Password), …)`. Never let an empty/sentinel password authenticate an OAuth-only account.
- `userController.go:1211` (`ChangeUserPassword`) — nil-guard the old-password compare so an OAuth-only user can *set* a first password.
- Struct inserts at `96–105` / `197–206` → `Password: &hash`.
- Add `refreshToken` to the `UserLogin` response (`326–330`); add `linkedProviders` to the `GetUserProfile`/`/users/me` response (`:333`) and the login response so the profile screen doesn't need a second round-trip.

### Token storage & encryption at rest
PCO `access_token`/`refresh_token`/`token_expires_at`/`scopes` live on the `user_external_identity` row. **Encrypt with AES-256-GCM**, key from env/KMS — **not** the JWT `SECRET`, and **not** pgcrypto (pgcrypto takes the key as a SQL argument → it lands in logs / `pg_stat_statements`). Optional `token_enc_version SMALLINT` for key rotation. Never return raw provider tokens to the device.

> **v1 token lifecycle question (§I gap):** v1 uses the PC access token exactly once (identity fetch at login). The stored refresh token is "for the future groups feature," but nothing refreshes it and PCO refresh tokens expire (~90d), so by the time groups ships the stored tokens are likely dead anyway. **Decide explicitly:** persist tokens in v1 (and accept they may be stale) vs. store only `sub` in v1 and capture tokens when the groups feature is built. Storing only `sub` is simpler and lower-risk.

### The re-login problem → server-side refresh tokens
Current mechanism (`util/reLogin.ts` + `util/apiClient.ts:113-186`) replays the stored raw password against `/login` — impossible for OAuth users. Replace with:
- On **every** successful auth (password + OAuth), also `issueRefreshToken(userID)`: 32 random bytes (`crypto/rand`), store `sha256(token)` in `auth_refresh_token` with `expires_at`, return `{token, refreshToken, user}`.
- `POST /auth/refresh`: validate hash → not-revoked → not-expired → fresh JWT, **rotate** (revoke old, issue new), **detect reuse** (a presented-but-revoked token revokes the whole family — theft resistance).
- `POST /auth/logout`: revoke the presented refresh token.
- **Do not** reuse `passwordResetController.go`'s `createTempToken` (`290–298`) — it's unsigned `base64(userID:timestamp)` and forgeable.

### Env vars to add (`.env` / `.env.example`, via `initializers/loadEnv.go`)
```
PC_CLIENT_ID=...
PC_CLIENT_SECRET=...                  # backend only — NEVER in the app bundle
PC_REDIRECT_URI=...                   # must match PCO registration + what the app sends
PC_OAUTH_SCOPES=openid                # least privilege
OAUTH_TOKEN_ENC_KEY=<base64 32 bytes> # AES-256-GCM key for user_external_identity tokens
REFRESH_TOKEN_TTL_DAYS=90
# later: GOOGLE_OAUTH_CLIENT_ID/SECRET, APPLE_CLIENT_ID/TEAM_ID/KEY_ID/PRIVATE_KEY(.p8)
```
> **Flag:** `firebase-service-account.json` is committed in the backend repo today — do **not** follow that pattern; keep these in env only. `golang.org/x/oauth2 v0.30.0` is already in `go.sum` (indirect); promote to a direct `require` for Google if desired — no new third-party dep is strictly required.

### CheckAuth impact — confirmed unaffected
`middlewares/checkAuth.go` validates HS256 signature + `exp`, loads `user_profile` by `claims["id"]`, sets `currentUser`/`admin`. Since OAuth/refresh mint the same JWT via `generateAccessToken`, **no change**. The `Select("*").ScanStruct(&user)` at `:62` keeps working for NULL-password users once the model is `Password *string`.

---

## E. Database changes (`prayerloop-psql`)

**Repo conventions to match:** VARCHAR+CHECK enums (no native `ENUM`); **zero extensions** (decisive against pgcrypto); PK `<table>_id SERIAL`; FK `... REFERENCES user_profile(user_profile_id) ON DELETE CASCADE`; credential/satellite tables (`user_push_tokens`, `password_reset_tokens`) omit `created_by/updated_by/deleted` and hard-delete; shared trigger fns `set_datetime_create()` / `update_datetime_update()` already exist (`definitions/user_profile.sql:51-69`). **Next migration number is 026.**

> ⚠️ **Pre-existing repo bug to route around:** `scripts/run_migrations.sh:47-49` does its own `INSERT INTO _migrations` with **no `ON CONFLICT`**, but migrations 022–025 already self-record — running 026 through `run_migrations.sh` would abort on the `UNIQUE(filename)` constraint. Apply 026 via `make run-migration` / `psql -f`, **or** fix line 49 to add `ON CONFLICT (filename) DO NOTHING`.

### E.1 Nullable password + the second/third blockers
```sql
ALTER TABLE "user_profile" ALTER COLUMN password DROP NOT NULL;   -- idempotent
```
- `username NOT NULL UNIQUE` → backend synthesizes a unique username (`pc_<sub>` or email-local-part + collision suffix). Note `pc_<sub>` becomes the user's **visible, login-able username with no rename path** unless you add one — decide UX.
- `first_name`/`last_name NOT NULL` → populate from provider (PC returns both; Google `given_name`/`family_name`; Apple **only on first authorization** — capture then).
- `created_by`/`updated_by` (`INT NOT NULL`) → use `1` (seeded admin), matching existing signup.
- **"≥1 auth method" invariant** cannot be a SQL `CHECK` (no subqueries) — enforce in the app (unlink guard) with an optional `BEFORE DELETE` trigger as defense-in-depth (E.5, with the cascade caveat).

### E.2 New table — `user_external_identity`
```sql
CREATE TABLE IF NOT EXISTS "user_external_identity" (
    user_external_identity_id SERIAL PRIMARY KEY,
    user_profile_id   INTEGER NOT NULL REFERENCES user_profile(user_profile_id) ON DELETE CASCADE,
    provider          VARCHAR(50)  NOT NULL,
    provider_user_id  VARCHAR(255) NOT NULL,          -- the OIDC sub (PC person id / Google sub / Apple sub)
    provider_email    VARCHAR(255),                   -- descriptive only, NOT an identity key; may be NULL/relay
    access_token      TEXT,                           -- app-encrypted ciphertext (AES-256-GCM), nullable
    refresh_token     TEXT,                           -- app-encrypted ciphertext, nullable
    token_expires_at  TIMESTAMP,
    scopes            TEXT,                           -- space-delimited granted scopes
    organization_id   VARCHAR(255),                   -- PC org/tenant; reserved for future multi-church
    datetime_create   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    datetime_update   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_uei_provider      CHECK (provider IN ('planning_center','google','apple')),
    CONSTRAINT uq_uei_provider_user  UNIQUE (provider, provider_user_id),   -- one provider identity -> <=1 account
    CONSTRAINT uq_uei_user_provider  UNIQUE (user_profile_id, provider)     -- <=1 account per provider (relax for multi-org)
);
CREATE INDEX IF NOT EXISTS idx_uei_user_profile_id ON "user_external_identity" (user_profile_id);
CREATE INDEX IF NOT EXISTS idx_uei_provider_email  ON "user_external_identity" (provider_email);
CREATE TRIGGER user_external_identity_set_datetime_create BEFORE INSERT ON "user_external_identity"
    FOR EACH ROW EXECUTE PROCEDURE set_datetime_create();
CREATE TRIGGER user_external_identity_set_datetime_update BEFORE UPDATE ON "user_external_identity"
    FOR EACH ROW EXECUTE PROCEDURE update_datetime_update();
```
Deliverables: canonical `definitions/user_external_identity.sql` (leading `DROP TABLE ... CASCADE`, sectioned comments/indexes/triggers) **and** `migrations/026_*.sql` (header block, `BEGIN/COMMIT`, `IF NOT EXISTS`, `DROP TRIGGER IF EXISTS`+`CREATE TRIGGER`, self-record `ON CONFLICT DO NOTHING`, commented Down block) — the two must agree. Wire `\i './definitions/user_external_identity.sql'` into `database_init.sql`; edit `definitions/user_profile.sql:6` to drop `NOT NULL`; update `.tbls.yml` + `docs/_sidebar.md` + regenerate `docs/schema/*` with `tbls doc`; add a `CHANGELOG.md` entry.

### E.3 New table — prayerloop refresh tokens (`auth_refresh_token`)
```sql
auth_refresh_token (
  auth_refresh_token_id SERIAL PK,
  user_profile_id INT NOT NULL REFERENCES user_profile ON DELETE CASCADE,
  token_hash      TEXT UNIQUE,            -- sha256(opaque token); never plaintext
  family_id       VARCHAR/UUID,           -- for rotation/reuse-detection
  expires_at      TIMESTAMP,
  revoked         BOOLEAN DEFAULT FALSE,
  last_used_at    TIMESTAMP,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
)
```

### E.4 Token encryption — application-layer, not pgcrypto
Encrypt `access_token`/`refresh_token` in Go (AES-256-GCM, key from env/KMS), store `base64(nonce‖ciphertext‖tag)` in the `TEXT` columns. Rationale: repo uses zero extensions; pgcrypto leaks the key into logs/`pg_stat_statements`; app-layer gives real key separation (a stolen DB dump exposes nothing).

### E.5 Optional defense-in-depth — last-auth-method trigger (NOT in 026 by default)
```sql
CREATE OR REPLACE FUNCTION enforce_uei_last_auth_method() RETURNS TRIGGER AS $$
DECLARE pw_present BOOLEAN; remaining INT;
BEGIN
  SELECT password IS NOT NULL INTO pw_present FROM user_profile WHERE user_profile_id = OLD.user_profile_id;
  SELECT count(*) INTO remaining FROM user_external_identity
    WHERE user_profile_id = OLD.user_profile_id AND user_external_identity_id <> OLD.user_external_identity_id;
  IF pw_present IS NOT TRUE AND remaining = 0 THEN
    RAISE EXCEPTION 'cannot unlink the only authentication method for user_profile %', OLD.user_profile_id;
  END IF;
  RETURN OLD;
END; $$ LANGUAGE plpgsql;
```
> ⚠️ **This trigger would fire during `ON DELETE CASCADE` from account deletion and break the shipped delete-account feature** (see §I P0). Keep the **app-layer guard primary** (clearer error UX); if you add the trigger, guard it with a session flag / parent-exists check.

### E.6 Scenario → data-op mapping
| Scenario | Operations | Guard |
|---|---|---|
| 1 new PC user | lookup `(planning_center, sub)`→none; probe `user_profile.email`; INSERT `user_profile` (password NULL, synth username, `email_verified=FALSE`, by=1); `GetOrCreateSelfPrayerSubject`; INSERT identity | email-collision → interstitial (don't blind-insert; `UNIQUE(email)` would 500); wrap in TX |
| 2 link logged-in | exchange; lookup `(planning_center, sub)`: same user→idempotent, different user→**409**, none→INSERT for currentUser | `uq_uei_user_provider` blocks 2nd PC acct |
| 3 login either | password: bcrypt (treat NULL password as "not available", never a match); PC: lookup→load or Scenario 1 | — |
| 4 unlink | guard query → DELETE identity (hard delete destroys stored tokens) | block iff `has_password=FALSE AND identity_count<=1` |

---

## F. Mobile changes (`prayerloop-mobile`)

### F.1 Login screen
Native email/password stays primary (green); OAuth buttons below an "or continue with" divider (PC → Apple (iOS only) → Google). ASCII mockup in §B/Q4. New `components/login/OAuthButtons.tsx`.

### F.2 Linking/unlinking UI (scenarios 2 & 4)
Add a **"Linked Accounts" card** to `components/Profile/ProfileContent.tsx` (between `PrayerReminderCard` and logout, `~:49/:51`) — renders in both `app/(tabs)/userProfile.tsx` and `ProfileDrawer.tsx` with zero extra wiring. New `components/Profile/LinkedAccountsCard.tsx`. Each provider row shows linked/not-linked from `user.linkedProviders`; "Link" launches OAuth against the **link** endpoint; "Unlink" confirms via `Alert` and is **blocked when it would remove the last auth method** ("Set a password first").

### F.3 OAuth flow code-shape (`util/oauth.ts` / `util/oauthPlanningCenter.ts`)
```ts
import * as WebBrowser from 'expo-web-browser';
WebBrowser.maybeCompleteAuthSession();          // module top-level — else the popup won't close
import * as AuthSession from 'expo-auth-session';

const discovery = {
  authorizationEndpoint: 'https://api.planningcenteronline.com/oauth/authorize',
  tokenEndpoint:         'https://api.planningcenteronline.com/oauth/token',
};
const redirectUri = AuthSession.makeRedirectUri({ scheme: 'prayerloop', path: 'oauth-callback' }); // dedicated, UNROUTED

const [request, response, promptAsync] = AuthSession.useAuthRequest({
  clientId: PCO_CLIENT_ID,                        // public id only (EXPO_PUBLIC_*) — NEVER the secret
  redirectUri, scopes: ['openid'],
  responseType: AuthSession.ResponseType.Code, usePKCE: true,   // lib makes verifier/challenge (S256)
}, discovery);

// on tap: await promptAsync();  then in an effect on `response`:
if (response?.type === 'success') {
  const { code, state } = response.params;
  if (state !== request?.state) return;          // CSRF check
  const r = await axios.post(`${BASE_API_URL}/auth/oauth/planningcenter/login`, {
    code, code_verifier: request?.codeVerifier, redirect_uri: redirectUri,
  });
  dispatch(loginSuccess({ message: r.data.message, token: r.data.token, user: r.data.user }));
}
// MUST ALSO handle response?.type === 'cancel' | 'dismiss' | 'error' (access_denied), network failures,
// and backend 401/409 — define error UX for the social path.
```

### F.4 OAuth redirect vs the existing `join-group` deep link
`expo-auth-session`/`WebBrowser.openAuthSessionAsync` register a one-shot listener and **resolve `promptAsync` before expo-router sees the URL** — so the redirect doesn't navigate and **doesn't collide** with `app/join-group.tsx` (which only handles `prayerloop://join-group`). Rule: OAuth = `prayerloop://oauth-callback` (dedicated, **unrouted**); invites stay `prayerloop://join-group` (routed). No new intent filter; **do not** add `app/oauth-callback.tsx`.
> ⚠️ **Cold-start gap:** if the OS relaunches the app via the deep link with no active `AuthSession` (app killed mid-flow), the redirect falls through to expo-router → `+not-found`, dropping the `code`. Run a manual warm/backgrounded/killed redirect test; add a fallback handler only if it falls through.

### F.5 Secure storage — replace raw-password re-login
- Today: raw **password** in AsyncStorage (`components/login/LoginView.tsx:59-64`, `store/authSlice.ts:135-136`), replayed on 401 (`util/reLogin.ts:30-37`, `util/apiClient.ts:113-180`). Unusable for OAuth and a standing liability for everyone.
- Add `expo-secure-store`; store the new prayerloop **refresh token** in Keychain/Keystore (`util/secureTokenStore.ts`). Rewrite `util/reLogin.ts` → `refreshAccessToken()` that reads the refresh token and calls `POST /auth/refresh`, reusing the existing `isRefreshing`/`failedQueue` machinery as a **drop-in replacement** behind the same 401 interceptor. **Delete** the `rememberedPassword` writes/reads (keep `rememberedEmail` for prefill — non-secret). On `logout`, `deleteItemAsync` the refresh token and call `POST /auth/logout`.
- **Migration:** existing users have only a stored password. Keep the password-replay path as a **one-time fallback** to mint the first refresh token on the upgrade launch, then purge the stored password. (Note: `/login` still works for un-upgraded clients — plan a coordinated client-upgrade + sunset.)

### F.6 Social login: Apple + Google
- **Apple — App Store Guideline 4.8 (precise rule):** 4.8 ("Login Services") does **not literally mandate Sign in with Apple.** It requires that an app offering a third-party/social login for the **primary** account *also* offer an equivalent login that (i) limits data to name+email, (ii) lets users keep their email private, (iii) doesn't collect interactions for ads without consent. **Sign in with Apple is the standard way to comply** (Hide My Email covers (ii)); there are 5 exceptions (own-account-only, marketplaces, education/enterprise, gov/industry eID, pure third-party-service clients). **prayerloop's plain email/password does NOT satisfy (ii)** (no private relay). Since Scenario 1 makes PC a primary-account login, **ship Sign in with Apple on iOS.** Use `expo-apple-authentication` (`ios.usesAppleSignIn: true`), render iOS-only via `isAvailableAsync()`, use the official `AppleAuthenticationButton`. Apple returns `email`/`fullName` **only on first authorization** — persist them server-side then. (4.8 is iOS-only; irrelevant to Google Play.)
- **Google — recommended native `@react-native-google-signin/google-signin`** over `expo-auth-session/providers/google`: the app already uses `@react-native-firebase` + `expo-build-properties → ios.useFrameworks:"dynamic"`, there are documented SDK 53 regressions in the AuthSession Google flow, and the native lib yields an `idToken` for backend verification (identical server-side shape to Apple/PC).

### F.7 New deps / config / files
- **Deps** (`npx expo install`): `expo-auth-session` (+ `expo-crypto` if not transitive), `expo-secure-store`, `expo-apple-authentication`, `@react-native-google-signin/google-signin`. `expo-web-browser` + `expo-linking` already present. **All contain native code → a new dev-client/EAS build is required** (OTA won't pick them up; they also **cannot run in Expo Go at all** — dev-client from day one).
- **`app.config.ts` plugins:** add `"expo-apple-authentication"`, `["@react-native-google-signin/google-signin", { iosUrlScheme: "<REVERSED_IOS_CLIENT_ID>" }]`, `["expo-secure-store", {}]`. `expo-auth-session` needs **no** plugin. Make the Google `iosUrlScheme` and `GoogleService-Info.{dev,}plist`/`google-services{,.dev}.json` **variant-aware** (`IS_DEV`).
- **State:** extend `AuthState` with `refreshToken` (or keep only in secure-store); extend `User` (`util/shared.types.ts:19-34`) with `linkedProviders`; add `LoginResponse.refreshToken`; new thunks `loginWithOAuth`/`linkProvider`/`unlinkProvider`; replace `attemptAutoLogin`→`attemptRefresh`. Keep the refresh token **out** of redux-persist (`store/store.ts:18`).
- **Public** PCO `client_id` + Google `webClientId` ride in `extra`/`EXPO_PUBLIC_*`; **PCO `client_secret` lives ONLY on the backend.**

---

## G. Planning Center app setup

| Item | Value / Decision |
|---|---|
| **Register at** | `https://api.planningcenteronline.com/oauth/applications` — **Org Admins only**. PCO guidance: create **ONE** dedicated org + OAuth app for a multi-church third-party app, not one per church. **[OFFICIAL/CORROBORATED]** |
| **Fields** | name, website URL, redirect URI(s), scopes. `client_id` + `client_secret` issued at creation; **secret shown once** → store server-side. |
| **Authorize** | `https://api.planningcenteronline.com/oauth/authorize` **[OFFICIAL]** |
| **Token** | `https://api.planningcenteronline.com/oauth/token` **[OFFICIAL]** |
| **UserInfo** | `https://api.planningcenteronline.com/oauth/userinfo` (OIDC, cleaner for name+email); or REST `https://api.planningcenteronline.com/people/v2/me` (email is a *related* resource — needs `?include=emails` or a 2nd call). |
| **Revoke (unlink)** | `https://api.planningcenteronline.com/oauth/revoke` (RFC 7009). **[OFFICIAL — but one verifier didn't see `revocation_endpoint` in the discovery JSON; confirm before building Scenario 4 revoke.]** |
| **JWKS (id_token)** | `https://api.planningcenteronline.com/oauth/discovery/keys` (RS256). |
| **Callback URL(s)** | Exact-match `redirect_uri`. **Custom-scheme vs HTTPS-only is [UNVERIFIED] — test empirically.** Recommended: register an **HTTPS backend callback** (Variant A). Multi-redirect-URI entry format also undocumented [UNVERIFIED]. |
| **PKCE / secret** | PCO **requires PKCE for public apps** (S256). Token endpoint advertises only `client_secret_basic`/`client_secret_post` — **no `none`** → **token exchange MUST run on the backend with the secret.** Use PKCE **and** backend exchange together. **[OFFICIAL]** |
| **Scopes** | Advertised: `api, calendar, check_ins, giving, groups, home, registrations, resources, services, people, publishing, openid`. For login request **`openid`** (yields `sub`, `name`, `email`, `organization_id`, `organization_name`). Use `groups` only for the future PC-groups pull. Least privilege. |
| **`email_verified`** | ⚠️ **NOT in `claims_supported`** — PCO never asserts email verification. Treat every PCO email as unverified. **[OFFICIAL]** |
| **Tokens** | Access **2h**; refresh **up to 90 days**. **[OFFICIAL — some verifiers couldn't independently confirm; design for rotation: always persist the new `refresh_token`, treat `invalid_grant` on refresh as a dead link.]** |
| **Multi-organization** | One token = one `organization_id`. No "list-my-orgs" endpoint [UNVERIFIED]. Authorize-per-org; store `organization_id` now. |

---

## H. Security must-dos

**MUST (do not ship OAuth without these):**
1. **Key identities on `(provider, sub)`, never email.** `UNIQUE(provider, provider_user_id)`. Email is metadata.
2. **Never silently auto-merge an OAuth login into an existing password account.** Not-logged-in email collisions go through an **authenticated "sign in to link" interstitial**. **PCO can never auto-merge** (no `email_verified` claim). This is the single highest-severity issue — it prevents account pre-hijacking / password-bypass takeover. Both directions of the attack apply: (a) attacker controls a PC record claiming the victim's email; (b) attacker pre-registers a prayerloop account with the victim's email (your signup never verifies email), then a silent merge hands the victim into the attacker-controlled account. Google `email_verified=true` may ease *new-account creation* but still requires the interstitial before merging into a pre-existing account.
3. **PKCE (S256) on every authorize; no implicit flow.**
4. **No `client_secret` in the app; PCO token exchange runs on the backend; backend verifies provider id_tokens** (`iss`/`aud`/`exp`/`nonce` via JWKS) before issuing a prayerloop JWT.
5. **Random single-use `state` + `nonce`, validated server-side; exact redirect-URI matching. System browser only — never an embedded WebView.**
6. **Encrypt provider tokens at rest** (AES-256-GCM, key from env/KMS, not the JWT SECRET, not pgcrypto); least-privilege scopes.
7. **Move on-device bearer secrets to `expo-secure-store`; delete plaintext `rememberedPassword`; keep the JWT out of the AsyncStorage redux-persist whitelist.**
8. **Make `password` nullable; enforce "≥1 auth method" before any unlink/password removal; provide a set-password path for OAuth-only users.**
9. **Server-side refresh tokens with rotation + reuse detection.**
10. **Unlink/account-deletion revokes the provider token and deletes stored tokens** (Apple mandates revoke-on-account-delete). Unlink operates on `currentUser` from CheckAuth, never a client-supplied id.

**SHOULD:** prefer claimed-HTTPS redirects (App/Universal Links, `autoVerify:true`) over the catch-all custom scheme; shorten access-JWT TTL (e.g. 1h) now that refresh exists; optionally add a `session_id`/`token_version` claim for immediate revocation; keep HMAC-alg pinning in `checkAuth.go:37-39`; normalize email casing on signup AND link; store `organization_id`/`organization_name`; add audit logging for link/unlink/login events (never log codes/tokens).

---

## I. Open questions, gaps & phased effort

### I.1 Implementation-blocking gaps the review surfaced (address before/while building)

**P0 — will cause incidents or App Store rejection:**
- 🔴 **`DeleteUserAccount` already exists** (`userController.go:1443`, route `main.go:61`, mobile `util/deleteUserAccount.ts`, plus the public `GET /delete-account` static page at `main.go:40`) and performs **manual, ordered table-by-table deletes — it does NOT rely on `ON DELETE CASCADE`.** Therefore: (a) add `user_external_identity` + `auth_refresh_token` to that deletion sequence; (b) insert **best-effort PCO token revocation before** the deletes (Apple requires revoke-on-delete); (c) the E.5 last-auth-method `BEFORE DELETE` trigger would fire during this real delete path and break it — keep the guard app-layer.
- 🔴 **Email-collision interstitial needs a real mechanism.** The single-use auth `code` is already consumed during the backend exchange, so the user can't "replay" it to link. Design a short-lived server-side **pending-link record** (verified `sub` + tokens) keyed by a one-time token returned to the app, an app screen ("An account with this email exists — sign in to link"), and a `POST /auth/oauth/.../confirm-link` endpoint taking the pending-link token + password.
- 🔴 **No transaction/concurrency handling on auto-create+link.** Wrap INSERT user_profile → self-subject → welcome-email → INSERT identity → issue refresh in a TX; a double-tap races two "lookup→none" checks into two INSERTs that collide on `UNIQUE(provider, sub)` → 500. Use `INSERT ... ON CONFLICT` or catch-and-relookup to return the existing user idempotently.
- 🔴 **OAuth error/cancel/dismiss paths unhandled** — define UX for `cancel`/`dismiss`/`access_denied`, network failures on the POST, and backend 401/409.

**P1 — design contradictions / multi-env correctness:**
- 🟠 **nonce vs userinfo:** if the client generates state/nonce/PKCE and the backend fetches identity via userinfo/`/people/v2/me` (no nonce), the backend can't validate a nonce it didn't generate. Resolve: either backend owns the authorize request (`OAuthStart`) or the client forwards the id_token and the backend verifies it against JWKS instead of calling userinfo.
- 🟠 **`email_verified` consistency:** auto-create OAuth users with `email_verified=FALSE` (PCO never asserts verification). Reconcile with the existing `verification_token`/`email_verified` flow.
- 🟠 **Multi-env redirect/scheme collision** (dev/prod share `scheme:"prayerloop"`; `iosUrlScheme` + Google service files differ by `IS_DEV`; `makeRedirectUri` differs Expo Go vs dev-client). Register per-environment redirects; make plugin config variant-aware.
- 🟠 **No "Set Password" path for OAuth-only users** in the app — the unlink guard depends on it; build it.
- 🟠 **Stored PCO tokens are dead weight in v1** with no refresh job — decide whether to persist tokens in v1 or store only `sub` (§D).
- 🟠 **Testing strategy** — PCO sandbox/test org, mock `OAuthProvider` for table-driven backend tests of found/not-found/collision branches, tests for refresh-token rotation + reuse detection and the AES-GCM round-trip, and the fact that native modules can't run in Expo Go (dev-client/EAS from day one).

**P2 — will surface mid-implementation:**
- 🟡 Add `linkedProviders` to `GetUserProfile` (`/users/me`, `:333`) and the `/login` response, not just a new endpoint.
- 🟡 Cold-start/killed-app redirect loses the code (§F.4).
- 🟡 **Pick canonical names now:** provider slug (`planning_center` vs `planningcenter` vs `pco`), identity table (`user_external_identity` vs `user_identity`), refresh table (`auth_refresh_token` vs `user_session`). Pick and use consistently in DDL + backend + mobile.
- 🟡 `SearchUserByEmail` + connection-request endpoints (`main.go:151-156`) key on email; an Apple-relay/NULL-email auto-created user becomes unfindable/unconnectable — decide a policy.
- 🟡 Logout/revocation: `POST /auth/logout` revokes only the presented refresh token; the JWT stays valid until `exp` (≤24h). "Log out all devices" is undesigned.
- 🟡 Returning-user profile sync (name/email changed at PCO; a changed PC email could collide with `UNIQUE(email)`) and the Scenario-2 409 UX are undefined.
- 🟡 Welcome email may go to a null/relay email; synthesized `pc_<sub>` username has no rename path.
- 🟡 API rollout/versioning + sunset for the one-time password→refresh-token bootstrap.

### I.2 Verify empirically before building (~30 min in the PCO developer portal)
1. **Custom-scheme vs HTTPS-only redirect URIs** + multi-redirect entry format (decides Variant A vs B).
2. **`/oauth/revoke` existence** (one verifier didn't find `revocation_endpoint` in discovery).
3. **Token lifetimes & refresh rotation** (access 2h / refresh ≤90d).
4. **Exact `openid`→claims mapping** and that **`email_verified` is absent**.
5. **People `/me` email shape** (related resource vs `?include=emails`).
6. **Deauthorization webhook** existence (defensively treat `invalid_grant`/401 on refresh as an implicit unlink).

### I.3 Phased effort breakdown

- **Phase 0 — Schema + backend foundation + refresh-token swap (gates everything).** Migration 026 (`password` nullable + `user_external_identity` + `auth_refresh_token`); `Password *string` + ripple edits (`:301`, `:1211`, `:96-105`, `:197-206`); `authHelpers.go` extractions; `services/crypto.go`; `/auth/refresh` + `/auth/logout` with rotation/reuse-detection; mobile swap of re-login → refresh + `expo-secure-store` + delete `rememberedPassword`. **Delivers a standalone security win (kills plaintext password storage) even before any OAuth ships.**
- **Phase 1 — Planning Center login (scenarios 1 & 3).** PCO app registration; `services/oauthService.go` (`PlanningCenterProvider`); `OAuthLogin` + auto-create + email-collision interstitial (pending-link); mobile `util/oauth.ts` + "Continue with Planning Center"; `linkedProviders` on user responses. Run the §I.2 empirical tests here.
- **Phase 2 — Linking / unlinking (scenarios 2 & 4). [Provider-agnostic — still needed for whichever provider ships first, see §M]** `OAuthLink`/`OAuthUnlink`/`ListUserIdentities` + last-auth-method guard + set-password path + provider revoke; mobile `LinkedAccountsCard` + account-deletion integration (add new tables to the manual delete sequence).
- **Phase 3 — Google + Apple. [PROMOTED to current priority, 2026-07-08 — see §M]** Reuse the provider-parameterized endpoints (new provider impls + id_token verification per JWKS). `expo-apple-authentication` (iOS, Guideline 4.8) + `@react-native-google-signin/google-signin`. New env: `GOOGLE_*`, `APPLE_*` (Apple client_secret is a `.p8`-signed JWT).
- **Future (don't deep-design now):** multi-church (relax `uq_uei_user_provider` to include `organization_id`; authorize-per-org) and pulling PC groups (request `groups` scope; server-to-server sync via the stored refresh token). **2026-07-07: groups sync has been promoted from afterthought to the primary church-integration direction — see §L.3.**

---

## J. Key repo file references (absolute)

- **Backend:** `/Users/zdelcoco/dmsi-io/prayerloop-backend/controllers/userController.go` (UserLogin `266–331` / JWT `307–330`, PublicUserSignup `23–143`, GetOrCreateSelfPrayerSubject `1635–1689`, password compares `301`/`1211`, DeleteUserAccount `1443`), `middlewares/checkAuth.go:28–80`, `models/userProfile.go:8`, `models/login.go:6`, `main.go:14–19,40,47,52,58–61`, `controllers/passwordResetController.go:290–298`
- **psql:** `/Users/zdelcoco/dmsi-io/prayerloop-psql/definitions/user_profile.sql:5-7,45,51-69`, `definitions/user_push_tokens.sql`, `definitions/password_reset_tokens.sql`, `migrations/025_create_prayer_subject_group_profile.sql` (template), `database_init.sql`, `scripts/run_migrations.sh:47-49`, `.tbls.yml`, `docs/_sidebar.md`, `CHANGELOG.md`; NEW: `migrations/026_add_oauth_account_linking.sql`, `definitions/user_external_identity.sql`, `definitions/auth_refresh_token.sql`
- **Mobile:** `/Users/zdelcoco/dmsi-io/prayerloop-mobile/app.config.ts:12,30-33,46-73,81`, `components/login/LoginView.tsx:44-66,78-85`, `store/authSlice.ts:21-28,85-87,135-136,147-175,198-213`, `store/store.ts:15-19`, `util/reLogin.ts:23-52`, `util/apiClient.ts:58-76,113-186`, `util/login.ts`, `util/login.types.ts`, `util/shared.types.ts:19-34`, `app/join-group.tsx`, `app/_layout.tsx`, `components/Profile/ProfileContent.tsx:49-68`, `app/(tabs)/userProfile.tsx`, `util/deleteUserAccount.ts`; NEW: `util/oauth.ts`, `util/secureTokenStore.ts`, `components/login/OAuthButtons.tsx`, `components/Profile/LinkedAccountsCard.tsx`

---

## K. Primary sources

- **PCO:** `https://api.planningcenteronline.com/docs/overview/authentication` · `https://api.planningcenteronline.com/.well-known/openid-configuration` · OIDC announcement `https://www.planningcenter.com/changelog/integrations-api/new-openid-connect-for-privacy-conscious-authentication` · `https://www.planningcenter.com/developers`
- **Expo:** AuthSession / Apple Authentication / SecureStore / Using Google authentication (`https://docs.expo.dev/...`) · `@react-native-google-signin` Expo setup
- **Apple:** App Review Guidelines 4.8 "Login Services" (`https://developer.apple.com/app-store/review/guidelines/`)
- **Standards:** RFC 8252 (OAuth for Native Apps), RFC 7636 (PKCE), RFC 7009 (Token Revocation)
- **Account-linking security:** nOAuth (`https://www.descope.com/blog/post/noauth`), pre-account-takeover research, handling unverified provider emails

> Generated from a multi-agent research + adversarial-verification pass. Items tagged **[UNVERIFIED]** must be confirmed empirically before implementation.

---

## L. 2026-07-07 addendum — Church Center login limitation & revised church integration

### L.1 The finding

**Church Center members cannot authenticate at PCO's OAuth authorize endpoint.** `api.planningcenteronline.com/oauth/authorize` sends users to the Planning Center login, which only accepts **Planning Center users** — people a church admin has granted product access (staff/volunteers). Church Center accounts are a **separate identity system** (passwordless email/SMS codes on the church's `churchcenter.com` subdomain) that creates a People *profile* but no Planning Center *login*.

**Empirical repro (2026-07-07):** created a Church Center account `zdelcoco+test1@gmail.com` in the em10tech org (signup completed; member portal fully usable). At the OAuth authorize login, both email and phone produce: *"There are no active accounts using the provided email address as their login method. Try another email or phone number. If you need to recover a lost account, contact your church administrators."* Not a propagation delay — the login account genuinely doesn't exist.

**Corroboration:**
- [planningcenter/developers#1460 "Church Center members SSO"](https://github.com/planningcenter/developers/issues/1460) (April 2026, **open, unanswered**) — identical use case (church app SSO for Church Center members), identical verbatim error.
- [planningcenter/developers#308](https://github.com/planningcenter/developers/issues/308) (2017) — PCO staff: *"right now it is only admins and volunteers in Services"* can log in; congregant login was "in the works" post login-rebuild but evidently never shipped for OAuth.
- The [OIDC announcement](https://www.planningcenter.com/changelog/integrations-api/new-openid-connect-for-privacy-conscious-authentication) covers "Planning Center users" — it's the same PC-user OAuth with a privacy-scoped identity claim, not member SSO. **[OFFICIAL]**
- [PCO login help](https://help.planningcenter.com/en/140717-log-in-to-planning-center.html) frames planningcenter.com logins as admin-provisioned.

**Impact on this doc:** Scenario 1 ("new PC user installs → logs in with PC") works exactly as designed but only reaches the staff/volunteer slice of a congregation, not Church Center-only members. The shipped Phase 0/1 code needs **no changes**; Google/Apple (Phase 3) now carry the general signup-friction story, and PC-specific value shifts to §L.3. Worth nudging PCO on #1460 for a roadmap answer before any larger pivot.

**Local testing note:** to give a test person a PC login, an em10tech admin grants them any minimal app permission in People, which emails an invite to create a password; after acceptance the OAuth flow accepts them.

### L.2 Why data-level integration still works

The wall is login-layer only — **PCO's data layer is unified**. A Church Center signup creates a People profile in the org, and Groups memberships hang off that profile. So an **admin-authorized org connection** can see every congregant and every group roster, even though those members can't authenticate themselves. Identity bridging happens by **email match** instead of SSO.

### L.3 Revised design — "church connection" (groups → prayer circles)

The org-level model, replacing member SSO as the church-facing integration:

1. **Church admin links their PCO org to prayerloop.** Admins always have real PC logins, so the existing OAuth flow works unchanged; add a `groups` (+ likely `people`) scope variant and mark the grant as an org connection rather than a personal login. Encrypted token storage already exists (§D); the **token refresh job (§E.3) is promoted from deferred to prerequisite** — this connection must keep working in the background (access tokens ~2h, refresh ~90d, rotating).
2. **Admin maps PCO groups → prayer circles.** `GET /groups/v2/groups` with the admin token; map each group to an existing circle or bulk-create. Mapping is opt-in per group — unmapped groups are never synced (privacy posture).
3. **Membership verification = verified-email match.** For each mapped group, pull memberships and match roster emails against prayerloop accounts with **verified** emails. Two-sided verification: the admin-curated PCO roster vouches for membership; prayerloop verifies email ownership. Matched users are auto-added to the circle.
4. **Unmatched roster members → church-branded invites** ("Grace Church added you to the Men's Ministry prayer circle"); on signup with that verified email they land in their circles automatically. This is the friction-reduction marketing story recovered: *your church's prayer circles are already waiting for you.*
5. **Progressive enhancement for PC users:** staff/volunteers who do PC OAuth login can have their own groups pulled with their own token via `GET /groups/v2/groups?filter=my_groups` (works for non-admin PC users — per [planningcenter/developers#1296](https://github.com/planningcenter/developers/issues/1296)) and self-serve join mapped circles.

**Design caveats:**
- **Household/shared emails** are common in PCO People — an email match can be ambiguous or wrong-person (e.g., spouse matched into the wrong ministry circle). Exact verified-email match only; give circle admins an approval queue for auto-joins (or per-church automatic-vs-approval setting).
- **Connection fragility:** the org connection rides one admin's PCO account — staff turnover, password reset, or 90 days of disuse kills the refresh token. Needs re-connect UX, connection health monitoring, ideally a second admin connection as backup.
- **PII/consent:** syncing congregation names/emails under admin authorization — restrict to mapped groups, church-branded invite emails, data-sharing consent in church onboarding.
- **Ruled out:** building on Church Center's internal app API (undocumented/unofficial; one silent change from breaking a commercial product).

### L.4 Spike before designing further (~30 min, Bruno + em10tech org)

1. Create a test group in em10tech (e.g. "Men's Ministry") with the test1 person as a member.
2. Auth with an admin **Personal Access Token** via HTTP basic auth (PATs are valid for direct API calls — the earlier misconfiguration was using them as *OAuth* creds).
3. `GET /groups/v2/groups` → confirm group visibility/permission requirements for the connecting user. **[UNVERIFIED]**
4. `GET /groups/v2/groups/:id/memberships` → confirm the membership record carries `email_address` directly, or whether emails must be resolved through the People API via the person ID (needs `people` scope). **[UNVERIFIED]**
5. Check whether PCO **webhooks** cover Groups membership created/destroyed events; otherwise scheduled polling (fine at PCO's ~100 req/20s rate limit — church rosters are small). **[UNVERIFIED]**

If the spike confirms the fields, phasing falls out naturally: org connection + refresh job → group-to-circle mapping + sync + email match → invites for unmatched → webhooks/polling cadence.

---

## M. 2026-07-08 addendum — Planning Center deprioritized; Google/Apple are next

### M.1 The decision

Planning Center login is **no longer top priority**. §L already showed PCO OAuth only reaches the staff/volunteer slice of a congregation (not Church Center members), which was the main reach argument for Scenario 1. The §L.3 "church connection" (org-admin-authorized groups → prayer-circle sync) is a real, still-valid idea, but it's a **larger, separate initiative** (admin dashboard, group-mapping UX, a background token-refresh job, ongoing PCO API maintenance) that doesn't need to gate general auth work. It's parked, not abandoned.

**What ships now instead:** Google and Apple sign-in, which cover the actual near-term goal — broader signup/login coverage for everyone, not just churches on Planning Center.

### M.2 Why this doesn't strand the PCO work already built

The Phase 0/Phase 1 code shipped on this branch (`controllers/oauthController.go`, `services/oauthService.go`, `services/crypto.go`, `models/userIdentity.go`, migration for `user_external_identity`/`oauth_pending_link`) was **already designed provider-first, not PC-first**:

- Routes are `/auth/oauth/:provider/login` and `/auth/oauth/:provider/confirm-link` — never PC-specific paths.
- `services.OAuthProvider` is an interface (`Name`, `ExchangeCode`, `FetchIdentity`); `PlanningCenterProvider` is just today's only registered implementation. `GetOAuthProvider(slug)` resolves by provider slug already.
- `user_external_identity.provider` is keyed on `(provider, provider_user_id)` with `CHECK (provider IN ('planning_center','google','apple'))` — Google and Apple rows already fit the schema with no migration.
- The email-collision pending-link mechanism (`createPendingLink`/`OAuthConfirmLink`), the auto-create-in-a-transaction path (`createOAuthUser`), and the "never trust provider email as an identity key" posture are all provider-agnostic — they apply identically to a Google or Apple `sub`.

**Net effect:** dropping PCO priority does not mean reverting or reworking this code. `PlanningCenterProvider` stays registered and working (harmless, tested, cheap to keep) and Google/Apple are added as new implementations of the same interface — not a new architecture.

### M.3 What is and isn't in scope now

**Deprioritized (not scheduled, not deleted):**
- §L.3 org-admin/groups-sync church integration and its §L.4 spike.
- Any further PC-specific mobile UI (a dedicated "Continue with Planning Center" button) beyond what's already backend-tested.
- Chasing [planningcenter/developers#1460](https://github.com/planningcenter/developers/issues/1460) for a congregant-SSO roadmap answer.

**Still needed regardless of provider (do this next, provider-agnostic infra from §D/§I):**
- **Server-side refresh tokens** (`auth_refresh_token` table already exists from the delete-account wiring, but `POST /auth/refresh`/`POST /auth/logout` issuance+rotation were never built). This is a **harder blocker for Google/Apple than it was for PC**: the mobile re-login fallback today replays a stored raw password (`util/reLogin.ts`), which doesn't exist for *any* OAuth-only account. Google/Apple can't ship to mobile without this.
- Linking/unlinking (Scenario 2/4: `OAuthLink`/`OAuthUnlink`/`ListUserIdentities`, last-auth-method guard, set-password path) — still useful so a Google or Apple identity can be linked to/unlinked from an existing account, exactly as designed for PC.

**New work — provider implementations:**
- `GoogleProvider` (`services/oauthService.go`): closest shape to `PlanningCenterProvider` — authorization-code exchange + a userinfo endpoint (`https://openidconnect.googleapis.com/v1/userinfo` or decode the returned `id_token`). `email_verified` **is** a real, trustworthy claim from Google — unlike PCO, a verified Google email *can* ease account creation, though the interstitial-before-merge rule (§H.2) still applies to linking into a pre-existing account.
- `AppleProvider`: **shape wrinkle, not a redesign.** Apple has no separate userinfo REST call — identity arrives as a signed `id_token` in the token-exchange response itself. `AppleProvider.FetchIdentity` verifies/decodes that JWT against Apple's JWKS (`https://appleid.apple.com/auth/keys`) locally rather than doing an HTTP GET like PC/Google do. Apple also only returns `email`/`name` on the **first** authorization ever — must be captured and persisted then, or it's gone. Apple's client "secret" is a `.p8`-signed JWT the backend mints per request (`APPLE_TEAM_ID`/`APPLE_KEY_ID`/`APPLE_PRIVATE_KEY`), not a static value like `PC_CLIENT_SECRET`.
- Mobile: `expo-apple-authentication` (iOS, satisfies Guideline 4.8 per §F.6 — still applies once *any* third-party login is primary-account-eligible) + `@react-native-google-signin/google-signin` (native, per §F.6's rationale over `expo-auth-session`'s Google provider). Both are drop-in against the existing `/auth/oauth/:provider/login` + `/confirm-link` endpoints and `OAuthButtons.tsx` shape from §F.1/F.3 — no new endpoint design needed.

### M.4 Revised phase order

1. Refresh tokens (remaining Phase 0 item) — now a Google/Apple mobile-launch blocker, not just a PC nice-to-have.
2. `GoogleProvider` + mobile Google button (Guideline-4.8-compliant Apple button ships alongside it, since iOS requires the equivalent-login story the moment any social login is primary).
3. `AppleProvider` (id_token/JWKS verification path) if not already done in step 2.
4. Linking/unlinking (Scenario 2/4), now covering all three providers.
5. §L.3 church-connection idea — revisit only if/when church-specific distribution becomes a priority again.

---

## N. 2026-07-08 addendum — project board audit (prayerloop-backend issues #33–39)

Pulled the `prayerloop` org project (EM10Tech, project #1) and reconciled its "Not started" column against what's actually in the codebase. Backend OAuth work is tracked as issues **#33–39** (all `oauth-integration` label); mobile has its own parallel set (not audited here — different repo).

### N.1 Findings at audit time

| # | Title | Board column (before) | Actual code state |
|---|---|---|---|
| #33 | `[P0] Email-collision interstitial + pending-link mechanism` | Testing | Shipped (`createPendingLink`/`OAuthConfirmLink`) — matches. |
| #34 | `[P0] Transaction safety + idempotent OAuth auto-create` | In Process | Satisfied by the uncommitted diff at the time (self-subject creation moved inside the `createOAuthUser` transaction, username/email-race recovery, fail-closed on unrelated races, opt-in real-DB race integration test). Refresh-token issuance — listed in the issue's transaction step list — intentionally **not** included; it doesn't exist yet anywhere in the codebase (tracked under #36). Diff was committed against this issue. |
| #35 | `[P0] Integrate OAuth tables into DeleteUserAccount + token revocation` | Not started | **Half done.** `DeleteUserAccount` (`controllers/userController.go:1509-1518`) already deletes `user_external_identity`/`oauth_pending_link`/`auth_refresh_token` rows. **Token revocation is not implemented** — see N.2. |
| #36 | `[Phase 0] Auth foundation: Password *string refactor, authHelpers.go, refresh/logout endpoints` | Not started | **Half done.** `Password *string` (models/userProfile.go) and `controllers/authHelpers.go` (`generateAccessToken`) both shipped and in use. **`/auth/refresh` and `/auth/logout` do not exist** — no route, no issuance/rotation code, no `models/authToken.go`. See N.3. |
| #37 | `[Phase 1] PCO OAuth service + OAuthLogin endpoint (scenarios 1 & 3)` | Not started | **Fully shipped** (`services/oauthService.go`, `controllers.OAuthLogin`) — stale card. **Moved to Testing** (2026-07-08) for a second dev to verify. |
| #38 | `[Phase 2] OAuthLink, OAuthUnlink, ListUserIdentities + set-password endpoint` | Not started | Correctly not started — none of this exists. |
| #39 | `[Phase 3] Google + Apple OAuth provider implementations` | Not started | Correctly not started — none of this exists. Per §M this is now the **next priority** once #35/#36 close out the remaining Phase 0 gaps. |

Takeaway: the board undercounted progress on #35/#36/#37 — normal drift from committing work without moving cards. #35 and #36 are judged small remaining effort (see below) and are being picked up next; that leaves **#38 and #39** as the actual backend backlog, plus the parallel mobile-side cards in the other repo.

### N.2 #35 remaining scope — token revocation

Not yet in the codebase:
- `OAuthProvider` interface (`services/oauthService.go`) has no `Revoke` method — only `Name`/`ExchangeCode`/`FetchIdentity` exist today.
- Add `Revoke(ctx context.Context, token string) error` to the interface; implement for `PlanningCenterProvider` as a `POST` to `https://api.planningcenteronline.com/oauth/revoke` (RFC 7009 — see §G; unverified whether PCO's discovery doc actually advertises `revocation_endpoint`, confirm before relying on it, but the URL itself is documented).
- In `DeleteUserAccount`, **before** the existing best-effort delete of `user_external_identity` rows (`userController.go:1509-1518`): for each identity row belonging to the user, decrypt its stored `access_token`/`refresh_token` (`services.DecryptToken`, `services/crypto.go`) and call `Revoke` best-effort (log-and-continue on failure — a dead revoke must never block account deletion, matching the existing `safeDeleteOptional` posture). Rows with no stored tokens (encryption was unavailable at link time) are skipped.
- Apple mandates revoke-on-delete (§H.10); doing this generically now means Google/Apple get it for free once #39 lands.

### N.3 #36 remaining scope — refresh/logout endpoints

Not yet in the codebase:
- `models/authToken.go` — `AuthRefreshToken` struct + `RefreshTokenRequest` (referenced in §D's file plan but never created).
- `issueRefreshToken(userID)` / `validateAndRotateRefreshToken(plaintext)` helpers (§D: 32 random bytes, store `sha256(token)`, rotate-on-use, detect reuse of a revoked token as theft signal).
- `POST /auth/refresh`, `POST /auth/logout` routes + handlers (`main.go` currently only has the two `/auth/oauth/:provider/*` routes from #33/#34/#37 — no refresh/logout routes at all).
- Wire `issueRefreshToken` into the existing login paths (`UserLogin`, `OAuthLogin`, `OAuthConfirmLink`) so every successful auth returns `{token, refreshToken, user}` — none of the current responses include a `refreshToken` field yet (confirmed: `respondWithSession` in `oauthController.go` returns only `token`).
- **Dependency to verify first:** confirm the `auth_refresh_token` table migration has actually landed in `prayerloop-psql` — `DeleteUserAccount`'s reference to it (`userController.go:1511`) is defensive (`safeDeleteOptional` silently no-ops on "relation does not exist"), so its presence hasn't been confirmed from this repo alone.
- This is the harder blocker flagged in §M.3: Google/Apple mobile launch needs this (no password to replay for re-login), so closing it now directly unblocks #39.

### N.4 Updated near-term order (backend)

1. #35 (token revocation) and #36 (refresh/logout) — small remaining scope, being picked up next.
2. #39 (Google + Apple provider implementations) — next priority per §M, unblocked once #36 ships.
3. #38 (link/unlink) — provider-agnostic, can land before or after #39.
4. Mobile-side equivalents (separate repo, not audited here).

---

## O. 2026-07-08 addendum — #35 and #36 shipped

Both remaining Phase 0 gaps identified in §N.2/§N.3 are now implemented:

- **#35 (token revocation):** `OAuthProvider` gained a `Revoke(ctx, token) error` method (`services/oauthService.go`); `PlanningCenterProvider.Revoke` POSTs to PCO's `/oauth/revoke` (RFC 7009), treating any non-200 as a failure. `DeleteUserAccount` (`controllers/userController.go`) now calls a new `revokeIdentityTokens` helper (`controllers/oauthController.go`) immediately before the existing `user_external_identity`/`oauth_pending_link`/`auth_refresh_token` delete loop: it loads the user's identity rows, decrypts the stored refresh token (falling back to the access token if no refresh token was stored) via `services.DecryptToken`, and calls `Revoke` best-effort — any failure (missing provider, decrypt error, HTTP failure) is logged and swallowed so deletion is never blocked.
- **#36 (refresh/logout endpoints):** Added `models/authToken.go` (`AuthRefreshToken`, `RefreshTokenRequest`). `controllers/authHelpers.go` gained `issueRefreshToken`, `validateAndRotateRefreshToken` (rotate-on-use; presenting an already-revoked token revokes its entire `family_id` as a theft signal), and `revokeRefreshToken` (idempotent). New routes `POST /auth/refresh` → `RefreshAccessToken` and `POST /auth/logout` → `RevokeRefreshToken` (both in `oauthController.go`, registered as public routes in `main.go` alongside the OAuth endpoints). `UserLogin`, `OAuthLogin`, and `OAuthConfirmLink` (via the shared `respondWithSession`) now all issue a refresh token on successful auth and include it as `refreshToken` in the response — issuance failure is logged and non-fatal, never blocking an otherwise-successful login. `REFRESH_TOKEN_TTL_DAYS` (default 90) added to `.env`/`.env.example`, alongside documenting the previously-undocumented `PC_CLIENT_ID`/`PC_CLIENT_SECRET`/`OAUTH_TOKEN_ENC_KEY` vars in `.env.example`.

Test coverage added: `services/oauthService_test.go` (Revoke success/empty-token/non-200 cases against an `httptest` server), `controllers/authHelpers_test.go` (issue/rotate/reuse-detection/logout unit tests against `sqlmock`), and new cases in `controllers/oauthController_test.go` (`revokeIdentityTokens` decrypt/fallback/skip/failure-survival, `RefreshAccessToken`/`RevokeRefreshToken` handlers). `go build ./...`, `go vet ./...`, and `go test ./... -race` all pass.

Per §N.4, **#39 (Google + Apple provider implementations) is next**, followed by #38 (link/unlink) — both now unblocked.
