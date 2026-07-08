# Changelog

All notable changes to the Prayerloop backend API will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project uses a date-based versioning scheme: `[year].[month].[sequence]`
(e.g., 2025.11.3 is the third release in November 2025).

## [Unreleased]

### Added

- **Planning Center OAuth login with email-collision interstitial** (Phase 1, planning doc §D/§H/§I)
  - `POST /auth/oauth/:provider/login` - Backend-mediated PKCE code exchange
    (confidential client holds `PC_CLIENT_SECRET`), identity fetch via OIDC
    userinfo, then: returning identity → login; email collision → pending
    link (below); new user → auto-create + link in a transaction (with
    concurrent-duplicate recovery on `UNIQUE(provider, provider_user_id)`)
  - **Email-collision pending-link mechanism** - an OAuth login whose email
    matches an existing `user_profile` is NEVER silently merged (Planning
    Center has no `email_verified` claim; email is not an identity key).
    Instead a single-use, 10-minute `oauth_pending_link` record (verified
    sub + AES-256-GCM-encrypted provider tokens, keyed by sha256 of a
    one-time token) is stored and a structured `409 OAUTH_EMAIL_COLLISION`
    error with `pendingLinkToken` is returned for the mobile interstitial
  - `POST /auth/oauth/:provider/confirm-link` - Completes the interstitial:
    verifies the pending-link token + the account's existing password
    (max 3 attempts), atomically consumes the record, inserts the
    `user_external_identity` link, and issues a prayerloop JWT
  - `services/oauthService.go` - Provider registry + `PlanningCenterProvider`
    (token exchange, userinfo); provider-parameterized so Google/Apple reuse
    the same endpoints
  - `services/crypto.go` - AES-256-GCM encrypt/decrypt for provider tokens at
    rest, keyed by new `OAUTH_TOKEN_ENC_KEY` env var (separate from JWT
    `SECRET`; stdlib only). Missing key degrades to not storing tokens
  - New env vars: `PC_CLIENT_ID`, `PC_CLIENT_SECRET`, `OAUTH_TOKEN_ENC_KEY`

### Changed

- **`UserProfile.Password` is now `*string`** (schema migration 026 makes the
  column nullable). `POST /login` rejects NULL-password (OAuth-only) accounts
  with a "sign in with your provider" 401; a NULL password never matches
- **JWT issuance extracted to `generateAccessToken`** (`controllers/authHelpers.go`)
  - single source shared by password login, OAuth login, and confirm-link;
  token shape (`{id, exp, role}`) unchanged, `CheckAuth` unaffected
- **`PATCH /users/:id/password`** skips the old-password check when the
  account has no password, giving OAuth-only users a set-first-password path
- **`DELETE /users/:id/account`** deletion sequence now includes
  `user_external_identity`, `oauth_pending_link`, and `auth_refresh_token`

### Fixed

- **OAuth auto-create is now fully transactional and idempotent under
  concurrent double-tap** (planning doc §I.1 P0, §D step 5). The self
  prayer_subject is created inside the same transaction as `user_profile` and
  `user_external_identity` (no partial accounts), and a unique-constraint
  violation on the `user_profile` INSERT — where the double-tap race actually
  surfaces, since both requests synthesize the same username from the provider
  sub — now recovers by re-looking-up the winner's `(provider, sub)` identity
  and returning that account instead of a 500. An unrelated username/email
  race (no linked identity) still fails closed rather than returning another
  user's account. The welcome email stays outside the transaction by design

## [2026.2.1] - 2026-02-06

### Added

- **Prayer Comments System** (Phase 7)
  - `POST /prayers/:id/comments` - Create comment with prayer access checks
  - `GET /prayers/:id/comments` - Fetch comments with privacy filtering
  - `PUT /prayers/:id/comments/:commentId` - Update comment text
  - `DELETE /prayers/:id/comments/:commentId` - Hard delete comment
  - `PATCH /prayers/:id/comments/:commentId/hide` - Soft delete for moderation
  - `PATCH /prayers/:id/comments/:commentId/privacy` - Toggle private/public visibility
  - Dual-moderator pattern: prayer creator and linked subject can both moderate
  - Privacy filtering at SQL layer (private comments visible to owner + moderators only)
  - Comment text max 500 characters enforced on backend
- **Comment Notifications** (Phase 7)
  - `PRAYER_COMMENT_ADDED` notification type with 15-minute debounce batching
  - Recipient deduplication (creator + subject + previous commenters, self-excluded)
  - `target_comment_id` field for deep-linking to specific comments
- **Prayer Analytics** (Phase 8)
  - `POST /prayers/:id/analytics` - Record prayer event with 5-minute cooldown
  - `GET /prayers/:id/analytics` - Fetch aggregate analytics (total prayers, unique users)
  - Upsert pattern for analytics records (INSERT or UPDATE atomically)
  - Returns 200 with existing data during cooldown (silent ignore, not error)
  - Zero-value defaults when no analytics record exists

### Changed

- **Group Creation** (Phase 9) - Auto-creates prayer_subject contact card with type='group' on group creation
- **Prayer Sharing** (Phase 9) - Auto-creates user access when sharing to group, fixing share-to-self regression
- **Prayer Queries** (Phase 9) - Added comment_count aggregation via LEFT JOIN on prayer endpoints
- **Contact Cards** (Phase 6) - Group member endpoint returns phone number and email via LEFT JOIN with user_profile
- **Notification Deep Linking** (Phase 7.1) - Group context added to `PRAYER_EDITED_BY_SUBJECT` notifications for navigation
- Both group auto-creation operations are non-fatal (log error but don't block primary operation)

### Database

- `023_add_prayer_comment.sql` - Created `prayer_comment` table with `is_private` and `is_hidden` flags; added `target_comment_id` to notification table
- `022_add_phone_email_to_prayer_subject.sql` - Added `phone_number` and `email` columns to `prayer_subject`
- `024_add_prayer_subject_id_to_group_profile.sql` - Links group to auto-created contact card

## [2026.1.1] - 2026-01-30

### Added

- **Prayer Edit History** (Phase 1)
  - `GET /prayers/:id/history` - Retrieve prayer edit history with actor names
  - Async history logging on prayer mutations (create, edit, delete, share, answer)
  - Action type detection (answered vs edited vs shared)
- **Subject Edit Authorization** (Phase 2)
  - Linked prayer subjects can now edit and delete prayers about them
  - Subject field protection (403 when attempting to change `prayer_subject_id`)
- **Notification System** (Phase 3)
  - `NotifyCircleOfPrayerShared` - Notifications to circle members on prayer share
  - `NotifySubjectOfPrayerCreated` - Notifications to linked subjects on prayer creation
  - `NotifyCreatorOfSubjectEdit` - Notifications when subject edits prayer, with 15-minute debounce
  - Notification muting per group (`mute_notifications` on `user_group`)
  - Notification target fields (`target_prayer_id`, `target_group_id`) for deep linking
  - Database notification records created alongside push notifications

### Fixed

- Stale `link_status` values on `prayer_subject` records (migration 017)
- NULL `mute_notifications` values backfilled on existing `user_group` records
- Notification timestamps converted from TIMESTAMP to TIMESTAMPTZ for timezone correctness

### Database

- `016_create_prayer_edit_history.sql` - Prayer audit log table with action types
- `017_fix_stale_link_status.sql` - Fixed prayer_subject link_status values
- `018_notification_infrastructure.sql` - Added `mute_notifications` to `user_group`, created `notification_debounce` table
- `019_fix_notification_nulls_and_timestamps.sql` - Backfilled NULLs, converted to TIMESTAMPTZ
- `020_add_notification_targets.sql` - Added `target_prayer_id` and `target_group_id` to notification table
- `021_backfill_notification_targets.sql` - Populated `target_group_id` for existing notifications

## [2025.12.1] - 2025-12-18

### Added

- User push notifications on group activity (prayer added, user added/removed)

### Fixed

- Bug preventing user from deleting prayer from group

## [2025.11.3] - 2025-11-19

### Added

- **Delete Account Endpoint** - `DELETE /users/:id/account`
  - Cascade deletes all user data (prayers, groups, memberships)
  - Sends confirmation email before deletion
  - Supports mobile app delete account feature
- **Prayer Reordering Endpoint** - `PATCH /users/:id/prayers/reorder`
  - Accepts array of prayer IDs in desired order
  - Updates `display_sequence` in `prayer_access` table
  - Validates complete prayer list and unique sequences
- **Group Prayer Reordering Endpoint** - `PATCH /groups/:id/prayers/reorder`
  - Reorders prayers within a group
  - Shared order across all group members
  - Validates group membership before allowing reorder
- **User Groups Reordering Endpoint** - `PATCH /users/:id/groups/reorder`
  - Reorders user's group list
  - Updates `group_display_sequence` in `user_group` table
  - Per-user ordering (each user can have their own order)

### Changed

- **Versioning Convention** - Switched from semantic versioning to date-based versioning
  - Format: `[year].[month].[sequence]`
  - Aligns with mobile app versioning

### Fixed

- Rate limiting improvements for delete account endpoint
- Validation improvements for reorder endpoints

## [0.0.1] - 2025-11-16

### Added

- Environment variable configuration for production API URL
- CORS configuration for production domain

- **Core API Endpoints**
  - User authentication (`POST /login`)
  - User signup (`POST /users`, `POST /public/signup`)
  - JWT-based authentication middleware
  - Rate limiting middleware (different limits for different endpoint groups)

- **Prayer Management**
  - `GET /users/:id/prayers` - Get user's prayers
  - `POST /prayers` - Create prayer
  - `PATCH /prayers/:id` - Update prayer
  - `DELETE /prayers/:id` - Delete prayer
  - `PATCH /prayers/:id/answer` - Mark prayer as answered

- **Group Management**
  - `POST /groups` - Create group
  - `GET /users/:id/groups` - Get user's groups
  - `GET /groups/:id` - Get group details
  - `GET /groups/:id/prayers` - Get group prayers
  - `POST /groups/:id/prayers` - Add prayer to group
  - `DELETE /groups/:id` - Delete group (creator only)
  - `DELETE /groups/:id/users/:userId` - Remove user from group (creator only)
  - `POST /groups/:id/leave` - Leave group

- **Group Invitations**
  - `POST /groups/:id/invite` - Invite user to group
  - `POST /groups/:id/join` - Join group via invitation

- **User Profile**
  - `GET /users/:id` - Get user profile
  - `PATCH /users/:id` - Update user profile
  - `PATCH /users/:id/password` - Change password

- **Password Reset**
  - `POST /password-reset/request` - Request reset code
  - `POST /password-reset/verify` - Verify reset code
  - `POST /password-reset/reset` - Reset password with code

- **Push Notifications**
  - `POST /users/:id/register-push-token` - Register FCM token
  - `POST /notifications/send` - Send push notification (admin)
  - Firebase Admin SDK integration
  - APNs configuration for iOS notifications

- **User Preferences**
  - `GET /users/:id/preferences` - Get user preferences
  - `PATCH /users/:id/preferences` - Update preferences

- **Email Notifications** (via Resend)
  - Welcome email on signup
  - Password reset codes
  - Group invitation emails
  - Group management notifications (leave, delete, remove)

### Fixed

- Production deployment configuration
- Environment-specific configuration loading

### Security

- JWT token authentication (24-hour expiration)
- Rate limiting on all endpoints
- Password hashing with bcrypt
- Input validation and sanitization
- CORS configuration

### Database

- PostgreSQL with direct SQL queries (no ORM)
- Connection pooling
- Prepared statements for security

## Version History

- **2026.2.1** - Comments, analytics, group enhancements (v1.1 Community & Interaction)
- **2026.1.1** - Edit history, subject authorization, notifications (v1.0 Prayer Subject Editing)
- **2025.12.1** - Group activity notifications
- **2025.11.3** - Delete account and reordering
- **0.0.1** - Initial MVP release

---

## Migration Notes

### Upgrading to 2026.2.1 from 2026.1.1

- Run migrations 022-024 in order:
  - `022_add_phone_email_to_prayer_subject.sql`
  - `023_add_prayer_comment.sql`
  - `024_add_prayer_subject_id_to_group_profile.sql`

### Upgrading to 2026.1.1 from 2025.12.1

- Run migrations 016-021 in order:
  - `016_create_prayer_edit_history.sql`
  - `017_fix_stale_link_status.sql`
  - `018_notification_infrastructure.sql`
  - `019_fix_notification_nulls_and_timestamps.sql`
  - `020_add_notification_targets.sql`
  - `021_backfill_notification_targets.sql`

### Upgrading to 2025.11.3 from 0.0.1

- Database migration required for `display_sequence` columns
- Run migration: `002_add_display_sequence_to_prayer_access.sql`
- Run migration: `002_add_group_display_sequence_to_user_group.sql`

### API Breaking Changes

None - all new endpoints are additive
