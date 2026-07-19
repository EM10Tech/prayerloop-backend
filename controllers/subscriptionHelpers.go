package controllers

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/PrayerLoop/initializers"

	"github.com/doug-martin/goqu/v9"
)

// defaultFreeCircleLimit applies when FREE_CIRCLE_LIMIT is unset or invalid.
const defaultFreeCircleLimit = 3

// FreeCircleLimit is the maximum number of active circles (owned + joined) a
// free-tier user may belong to at once.
//
// Read from the environment at each call rather than cached in a package var:
// package vars initialize before main's init() calls LoadEnvVariables, so a
// cached value would miss anything loaded from .env. Reading at point of use
// also matches how every other env var here is handled (services/oauthService.go).
//
// Raising this is the kill switch for the create/join gate -- it lets the limit
// be relaxed without a redeploy, which matters because the gate is enforced
// server-side against clients that may not yet render a paywall for the 403.
func FreeCircleLimit() int {
	raw := os.Getenv("FREE_CIRCLE_LIMIT")
	if raw == "" {
		return defaultFreeCircleLimit
	}

	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		log.Printf("invalid FREE_CIRCLE_LIMIT %q, using %d", raw, defaultFreeCircleLimit)
		return defaultFreeCircleLimit
	}

	return limit
}

// isUnderCircleLimit reports whether userID may create or join one more
// circle. Admins bypass the limit entirely. Otherwise it's a live COUNT(*)
// of active circle membership (not a debit counter) compared against
// FreeCircleLimit, unless the user is premium -- in which case they're
// always under the limit.
//
// Live counting (rather than a stored/decremented counter) means a premium
// or grandfathered user who lapses back to free keeps every circle they
// already have; they just can't create/join beyond the limit until back at
// or under it. This never retroactively removes anyone from a circle.
func isUnderCircleLimit(userID int, isAdmin bool) (bool, int, error) {
	if isAdmin {
		return true, 0, nil
	}

	count, err := activeCircleCount(userID)
	if err != nil {
		return false, 0, err
	}

	isPremium, err := isPremiumUser(userID)
	if err != nil {
		return false, 0, err
	}

	if isPremium {
		return true, count, nil
	}

	return count < FreeCircleLimit(), count, nil
}

// activeCircleCount is the live COUNT(*) of userID's active circle memberships.
// Archiving a circle flips user_group.is_active to false, which drops it from
// this count automatically -- there is no counter to decrement.
//
// Split out of isUnderCircleLimit because the archive/restore paths need the
// raw count to answer a question isUnderCircleLimit cannot express: "would
// restoring N circles at once put me over?" (count+N <= limit, not count <
// limit). Do NOT reach for isUnderCircleLimit there -- it returns count = 0 for
// admins, which would silently under-count a bulk restore.
func activeCircleCount(userID int) (int, error) {
	var count int
	_, err := initializers.DB.From("user_group").
		Select(goqu.COUNT("*")).
		Join(
			goqu.T("group_profile"),
			goqu.On(goqu.Ex{"group_profile.group_profile_id": goqu.I("user_group.group_profile_id")}),
		).
		Where(
			goqu.Ex{
				"user_group.user_profile_id": userID,
				"user_group.is_active":       true,
				"group_profile.is_active":    true,
			},
		).
		ScanVal(&count)
	if err != nil {
		return 0, fmt.Errorf("count active circles for user %d: %w", userID, err)
	}

	return count, nil
}

// isPremiumUser reports the cached RevenueCat entitlement state for userID.
// A missing user_subscription row means free tier -- isPremium's zero value
// (false) already matches that, so goqu's found bool is unused.
func isPremiumUser(userID int) (bool, error) {
	var isPremium bool
	_, err := initializers.DB.From("user_subscription").
		Select("is_premium").
		Where(goqu.C("user_profile_id").Eq(userID)).
		ScanVal(&isPremium)
	if err != nil {
		return false, fmt.Errorf("lookup premium status for user %d: %w", userID, err)
	}

	return isPremium, nil
}
