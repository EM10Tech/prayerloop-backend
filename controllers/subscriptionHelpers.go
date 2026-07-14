package controllers

import (
	"fmt"

	"github.com/PrayerLoop/initializers"

	"github.com/doug-martin/goqu/v9"
)

// FreeCircleLimit is the maximum number of active circles (owned + joined)
// a free-tier user may belong to at once.
const FreeCircleLimit = 3

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
		return false, 0, fmt.Errorf("count active circles for user %d: %w", userID, err)
	}

	// A missing user_subscription row means free tier -- isPremium's zero
	// value (false) already matches that, so the found bool is unused.
	var isPremium bool
	_, err = initializers.DB.From("user_subscription").
		Select("is_premium").
		Where(goqu.C("user_profile_id").Eq(userID)).
		ScanVal(&isPremium)
	if err != nil {
		return false, 0, fmt.Errorf("lookup premium status for user %d: %w", userID, err)
	}

	if isPremium {
		return true, count, nil
	}

	return count < FreeCircleLimit, count, nil
}
