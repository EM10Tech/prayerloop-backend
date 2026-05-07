package controllers

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

// TestEnsureCircleContactsForUser_CreatorPath: CreateGroup case. The user has
// just created the circle; CreateGroup already inserted the group's prayer_subject
// and updated group_profile.prayer_subject_id. The helper should detect the
// anchor, write the group-type psgp row, and find no other members.
func TestEnsureCircleContactsForUser_CreatorPath(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	const (
		userID         = 1
		groupProfileID = 10
		anchorSubject  = 100
	)
	groupName := "Prayer Warriors"

	mock.ExpectBegin()
	// ensureGroupContactForUser: no existing join row
	mock.ExpectQuery("SELECT \"prayer_subject_id\" FROM \"prayer_subject_group_profile\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}))
	// findExistingGroupSubject: anchor present on group_profile
	mock.ExpectQuery("SELECT \"prayer_subject_id\" FROM \"group_profile\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(anchorSubject))
	// findExistingGroupSubject: anchor verifies as group + owned by userID
	mock.ExpectQuery("SELECT \"prayer_subject_id\", \"prayer_subject_type\", \"created_by\" FROM \"prayer_subject\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id", "prayer_subject_type", "created_by"}).
			AddRow(anchorSubject, "group", userID))
	// Insert the join row for the anchor.
	mock.ExpectExec("INSERT INTO \"prayer_subject_group_profile\"").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// Other-members lookup → empty (only the creator).
	mock.ExpectQuery("SELECT \"user_profile_id\" FROM \"user_group\"").
		WillReturnRows(sqlmock.NewRows([]string{"user_profile_id"}))
	mock.ExpectCommit()

	err := EnsureCircleContactsForUser(userID, groupProfileID, groupName)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestEnsureCircleContactsForUser_JoinerWithExistingMember exercises the
// JoinGroup case where the joining user wires up a group-type subject for
// themselves and creates two reciprocal individual prayer_subject rows for
// the existing member. CRITICAL: no individual psgp inserts (schema rejects).
func TestEnsureCircleContactsForUser_JoinerWithExistingMember(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	const (
		joinerID       = 2
		existingMember = 1
		groupProfileID = 10
	)
	groupName := "Prayer Warriors"

	mock.ExpectBegin()
	// ensureGroupContactForUser: no existing join row for joiner
	mock.ExpectQuery("SELECT \"prayer_subject_id\" FROM \"prayer_subject_group_profile\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}))
	// findExistingGroupSubject: anchor exists but belongs to creator, not joiner
	mock.ExpectQuery("SELECT \"prayer_subject_id\" FROM \"group_profile\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(100))
	mock.ExpectQuery("SELECT \"prayer_subject_id\", \"prayer_subject_type\", \"created_by\" FROM \"prayer_subject\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id", "prayer_subject_type", "created_by"}).
			AddRow(100, "group", existingMember)) // owned by creator, not joiner
	// Fallback lookup for an unlinked group-type subject created_by joiner: none.
	mock.ExpectQuery("SELECT \"prayer_subject_id\" FROM \"prayer_subject\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}))
	// Insert a fresh group-type prayer_subject for joiner.
	mock.ExpectQuery("INSERT INTO \"prayer_subject\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(200))
	// Insert join row for that new group-type subject.
	mock.ExpectExec("INSERT INTO \"prayer_subject_group_profile\"").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// Other-members lookup → existingMember (1)
	mock.ExpectQuery("SELECT \"user_profile_id\" FROM \"user_group\"").
		WillReturnRows(sqlmock.NewRows([]string{"user_profile_id"}).AddRow(existingMember))
	// ensureIndividualContact(joiner -> existingMember): not found
	mock.ExpectQuery("SELECT \"prayer_subject_id\" FROM \"prayer_subject\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}))
	// lookupUserDisplayName for existingMember
	mock.ExpectQuery("SELECT \"first_name\", \"last_name\", \"username\" FROM \"user_profile\"").
		WillReturnRows(sqlmock.NewRows([]string{"first_name", "last_name", "username"}).
			AddRow("Alice", "Smith", "alice"))
	// Insert individual prayer_subject (joiner -> existingMember)
	mock.ExpectQuery("INSERT INTO \"prayer_subject\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(301))
	// ensureIndividualContact(existingMember -> joiner): not found
	mock.ExpectQuery("SELECT \"prayer_subject_id\" FROM \"prayer_subject\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}))
	// lookupUserDisplayName for joiner
	mock.ExpectQuery("SELECT \"first_name\", \"last_name\", \"username\" FROM \"user_profile\"").
		WillReturnRows(sqlmock.NewRows([]string{"first_name", "last_name", "username"}).
			AddRow("Bob", "Jones", "bob"))
	// Insert individual prayer_subject (existingMember -> joiner)
	mock.ExpectQuery("INSERT INTO \"prayer_subject\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(302))
	mock.ExpectCommit()

	err := EnsureCircleContactsForUser(joinerID, groupProfileID, groupName)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestEnsureCircleContactsForUser_AlreadyLinked verifies the helper exits
// early when the group-type join row already exists, and still proceeds to
// process other members.
func TestEnsureCircleContactsForUser_AlreadyLinked(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectBegin()
	// existing join row found → ensureGroupContactForUser exits early
	mock.ExpectQuery("SELECT \"prayer_subject_id\" FROM \"prayer_subject_group_profile\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(200))
	// no other current members
	mock.ExpectQuery("SELECT \"user_profile_id\" FROM \"user_group\"").
		WillReturnRows(sqlmock.NewRows([]string{"user_profile_id"}))
	mock.ExpectCommit()

	err := EnsureCircleContactsForUser(1, 10, "name")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestRemoveCircleContactsForLeavingUser verifies the leaving user's group-type
// prayer_subject is deleted (CASCADE drops its psgp row). Individual
// prayer_subject rows are left alone.
func TestRemoveCircleContactsForLeavingUser(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \"prayer_subject\".\"prayer_subject_id\" FROM \"prayer_subject_group_profile\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(555))
	mock.ExpectExec("DELETE FROM \"prayer_subject\"").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := RemoveCircleContactsForLeavingUser(1, 10)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestRemoveCircleContactsForLeavingUser_NoSubject covers the case where the
// leaving user has no group-type subject for this circle (legacy data).
func TestRemoveCircleContactsForLeavingUser_NoSubject(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \"prayer_subject\".\"prayer_subject_id\" FROM \"prayer_subject_group_profile\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}))
	mock.ExpectCommit()

	err := RemoveCircleContactsForLeavingUser(1, 10)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestRemoveAllCircleContacts verifies group-type prayer_subjects across all
// members are deleted; CASCADE clears the join rows.
func TestRemoveAllCircleContacts(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \"prayer_subject\".\"prayer_subject_id\" FROM \"prayer_subject_group_profile\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(101).AddRow(102))
	mock.ExpectExec("DELETE FROM \"prayer_subject\"").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	err := RemoveAllCircleContacts(10)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestRemoveAllCircleContacts_NoGroupSubjects: legacy data with no group-type
// subjects linked. We must NOT issue a DELETE with an empty IN list.
func TestRemoveAllCircleContacts_NoGroupSubjects(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \"prayer_subject\".\"prayer_subject_id\" FROM \"prayer_subject_group_profile\"").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}))
	mock.ExpectCommit()

	err := RemoveAllCircleContacts(10)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
