package controllers

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

// Test FreeCircleLimit - env-var driven so ops can relax the gate without a
// redeploy. t.Setenv restores the prior value automatically, which matters
// because FreeCircleLimit reads the environment at every call.
func TestFreeCircleLimit(t *testing.T) {
	t.Run("unset falls back to the default", func(t *testing.T) {
		t.Setenv("FREE_CIRCLE_LIMIT", "")

		assert.Equal(t, defaultFreeCircleLimit, FreeCircleLimit())
	})

	t.Run("valid value is honored", func(t *testing.T) {
		t.Setenv("FREE_CIRCLE_LIMIT", "5")

		assert.Equal(t, 5, FreeCircleLimit())
	})

	t.Run("a large value acts as the gate kill switch", func(t *testing.T) {
		t.Setenv("FREE_CIRCLE_LIMIT", "9999")

		assert.Equal(t, 9999, FreeCircleLimit())
	})

	t.Run("zero is allowed - no free circles at all", func(t *testing.T) {
		t.Setenv("FREE_CIRCLE_LIMIT", "0")

		assert.Equal(t, 0, FreeCircleLimit())
	})

	t.Run("unparseable value falls back rather than gating everyone at zero", func(t *testing.T) {
		t.Setenv("FREE_CIRCLE_LIMIT", "three")

		assert.Equal(t, defaultFreeCircleLimit, FreeCircleLimit())
	})

	t.Run("negative value falls back", func(t *testing.T) {
		t.Setenv("FREE_CIRCLE_LIMIT", "-1")

		assert.Equal(t, defaultFreeCircleLimit, FreeCircleLimit())
	})
}

// Test isUnderCircleLimit - the sole enforcer of the free-tier circle cap
func TestIsUnderCircleLimit(t *testing.T) {
	const userID = 1

	t.Run("admin bypasses the limit without querying the database", func(t *testing.T) {
		_, mock, cleanup := SetupTestDB(t)
		defer cleanup()

		underLimit, count, err := isUnderCircleLimit(userID, true)

		assert.NoError(t, err)
		assert.True(t, underLimit)
		assert.Equal(t, 0, count)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("free user under the limit is allowed", func(t *testing.T) {
		_, mock, cleanup := SetupTestDB(t)
		defer cleanup()

		mock.ExpectQuery("SELECT COUNT").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectQuery("SELECT \"is_premium\" FROM \"user_subscription\"").
			WillReturnRows(sqlmock.NewRows([]string{"is_premium"}))

		underLimit, count, err := isUnderCircleLimit(userID, false)

		assert.NoError(t, err)
		assert.True(t, underLimit)
		assert.Equal(t, 2, count)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("free user at the limit is rejected", func(t *testing.T) {
		_, mock, cleanup := SetupTestDB(t)
		defer cleanup()

		mock.ExpectQuery("SELECT COUNT").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(FreeCircleLimit()))
		mock.ExpectQuery("SELECT \"is_premium\" FROM \"user_subscription\"").
			WillReturnRows(sqlmock.NewRows([]string{"is_premium"}))

		underLimit, count, err := isUnderCircleLimit(userID, false)

		assert.NoError(t, err)
		assert.False(t, underLimit)
		assert.Equal(t, FreeCircleLimit(), count)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("missing user_subscription row is treated as free tier", func(t *testing.T) {
		_, mock, cleanup := SetupTestDB(t)
		defer cleanup()

		mock.ExpectQuery("SELECT COUNT").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(FreeCircleLimit()))
		mock.ExpectQuery("SELECT \"is_premium\" FROM \"user_subscription\"").
			WillReturnRows(sqlmock.NewRows([]string{"is_premium"})) // no row

		underLimit, _, err := isUnderCircleLimit(userID, false)

		assert.NoError(t, err)
		assert.False(t, underLimit)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("premium user is always under the limit, even past the cap", func(t *testing.T) {
		_, mock, cleanup := SetupTestDB(t)
		defer cleanup()

		mock.ExpectQuery("SELECT COUNT").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))
		mock.ExpectQuery("SELECT \"is_premium\" FROM \"user_subscription\"").
			WillReturnRows(sqlmock.NewRows([]string{"is_premium"}).AddRow(true))

		underLimit, count, err := isUnderCircleLimit(userID, false)

		assert.NoError(t, err)
		assert.True(t, underLimit)
		assert.Equal(t, 10, count)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates the circle count query error", func(t *testing.T) {
		_, mock, cleanup := SetupTestDB(t)
		defer cleanup()

		mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("db unreachable"))

		underLimit, _, err := isUnderCircleLimit(userID, false)

		assert.Error(t, err)
		assert.False(t, underLimit)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates the premium lookup query error", func(t *testing.T) {
		_, mock, cleanup := SetupTestDB(t)
		defer cleanup()

		mock.ExpectQuery("SELECT COUNT").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery("SELECT \"is_premium\" FROM \"user_subscription\"").
			WillReturnError(errors.New("db unreachable"))

		underLimit, _, err := isUnderCircleLimit(userID, false)

		assert.Error(t, err)
		assert.False(t, underLimit)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
