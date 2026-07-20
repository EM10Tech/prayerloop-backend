package controllers

import (
	"github.com/PrayerLoop/initializers"
	"github.com/doug-martin/goqu/v9"
)

// userPrayerAccessQuery builds the access-check query for userHasPrayerAccess.
// Split out so tests can assert the generated SQL shape.
func userPrayerAccessQuery(userID int, prayerID int) *goqu.SelectDataset {
	return initializers.DB.From("prayer_access").
		Select(goqu.COUNT("*")).
		Where(
			goqu.I("prayer_access.prayer_id").Eq(prayerID),
			goqu.Or(
				goqu.And(
					goqu.I("prayer_access.access_type").Eq("user"),
					goqu.I("prayer_access.access_type_id").Eq(userID),
				),
				goqu.And(
					goqu.I("prayer_access.access_type").Eq("group"),
					goqu.I("prayer_access.access_type_id").In(
						initializers.DB.From("user_group").
							Select("group_profile_id").
							Where(goqu.C("user_profile_id").Eq(userID)),
					),
				),
			),
		)
}

// userHasPrayerAccess reports whether a user can access a prayer through the
// prayer_access table: either a direct user grant, or a group grant for a
// circle the user belongs to.
//
// The two branches are deliberately independent. Resolving a direct user
// grant must not involve user_group at all - the old inline checks this
// replaces joined through user_group for both branches, so a user with zero
// circle memberships could never match even their own personal prayers (and
// the mobile app treated the resulting 401 as an expired session and logged
// them out).
//
// Callers should respond 403 (never 401) when this returns false: 401 is
// reserved for authentication failures, and the mobile client force-logs-out
// on repeated 401s.
func userHasPrayerAccess(userID int, prayerID int) (bool, error) {
	var count int64
	_, err := userPrayerAccessQuery(userID, prayerID).ScanVal(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
