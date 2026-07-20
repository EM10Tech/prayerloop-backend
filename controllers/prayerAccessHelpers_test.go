package controllers

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

// The generated SQL must resolve a direct user grant WITHOUT touching
// user_group. The old inline checks joined through user_group for both
// branches, so a user with zero circle memberships could never match even
// their own personal prayers (their activity view then 401'd and the mobile
// app force-logged them out).
func TestUserPrayerAccessQuerySQLShape(t *testing.T) {
	_, _, cleanup := SetupTestDB(t)
	defer cleanup()

	sql, _, err := userPrayerAccessQuery(108, 419).ToSQL()
	assert.NoError(t, err)

	// Direct user branch: plain equality on prayer_access columns only.
	assert.Contains(t, sql, `("prayer_access"."access_type" = 'user') AND ("prayer_access"."access_type_id" = 108)`)

	// Group branch: membership resolved via subquery, not a join.
	assert.Contains(t, sql, `IN ((SELECT "group_profile_id" FROM "user_group" WHERE ("user_profile_id" = 108)))`)
	assert.NotContains(t, sql, "JOIN")
}

func TestUserHasPrayerAccess(t *testing.T) {
	t.Run("grant found", func(t *testing.T) {
		_, mock, cleanup := SetupTestDB(t)
		defer cleanup()

		mock.ExpectQuery("SELECT COUNT").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		hasAccess, err := userHasPrayerAccess(108, 419)
		assert.NoError(t, err)
		assert.True(t, hasAccess)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no grant denies access", func(t *testing.T) {
		_, mock, cleanup := SetupTestDB(t)
		defer cleanup()

		mock.ExpectQuery("SELECT COUNT").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		hasAccess, err := userHasPrayerAccess(108, 419)
		assert.NoError(t, err)
		assert.False(t, hasAccess)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error propagates", func(t *testing.T) {
		_, mock, cleanup := SetupTestDB(t)
		defer cleanup()

		mock.ExpectQuery("SELECT COUNT").
			WillReturnError(errors.New("connection refused"))

		hasAccess, err := userHasPrayerAccess(108, 419)
		assert.Error(t, err)
		assert.False(t, hasAccess)
	})
}
