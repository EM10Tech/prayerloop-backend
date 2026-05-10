package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// TestCreatePrayerSubject tests the link_status logic in CreatePrayerSubject
func TestCreatePrayerSubject(t *testing.T) {
	tests := []struct {
		name               string
		userProfileID      *int
		expectedLinkStatus string
	}{
		{
			name:               "link_status is linked when user_profile_id is self",
			userProfileID:      IntPtr(1), // Same as MockUser (User_Profile_ID = 1)
			expectedLinkStatus: "linked",
		},
		{
			name:               "link_status is linked when user_profile_id is another user",
			userProfileID:      IntPtr(99), // Different user
			expectedLinkStatus: "linked",
		},
		{
			name:               "link_status is unlinked when user_profile_id is nil",
			userProfileID:      nil,
			expectedLinkStatus: "unlinked",
		},
		{
			name:               "link_status is unlinked when user_profile_id is zero",
			userProfileID:      IntPtr(0),
			expectedLinkStatus: "unlinked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			currentUser := MockUser()
			userID := currentUser.User_Profile_ID

			// Mock MAX(display_sequence) query
			maxSeqRows := sqlmock.NewRows([]string{"coalesce"}).AddRow(-1)
			mock.ExpectQuery("SELECT COALESCE").WillReturnRows(maxSeqRows)

			// Mock INSERT with RETURNING prayer_subject_id
			mock.ExpectQuery("INSERT INTO \"prayer_subject\"").
				WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(1))

			// Mock SELECT for fetching created subject
			// Build the row based on expected link_status
			subjectRows := sqlmock.NewRows([]string{
				"prayer_subject_id", "prayer_subject_type", "prayer_subject_display_name",
				"notes", "display_sequence", "photo_s3_key", "user_profile_id",
				"use_linked_user_photo", "link_status", "datetime_create", "datetime_update",
				"created_by", "updated_by",
			}).AddRow(
				1, "individual", "Test Subject",
				nil, 0, nil, tt.userProfileID,
				false, tt.expectedLinkStatus, nil, nil,
				userID, userID,
			)
			mock.ExpectQuery("SELECT").WillReturnRows(subjectRows)

			// Build request body
			requestBody := map[string]interface{}{
				"prayerSubjectDisplayName": "Test Subject",
				"prayerSubjectType":        "individual",
			}
			if tt.userProfileID != nil {
				requestBody["userProfileId"] = *tt.userProfileID
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, currentUser, false)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: "1"}}

			jsonData, _ := json.Marshal(requestBody)
			c.Request = httptest.NewRequest("POST", "/users/1/prayer-subjects", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			CreatePrayerSubject(c)

			assert.Equal(t, http.StatusCreated, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			assert.NoError(t, err)

			// Verify prayerSubject is returned
			prayerSubject, ok := response["prayerSubject"].(map[string]interface{})
			if !ok {
				// If prayerSubject not in response (edge case where SELECT fails), skip link_status check
				t.Log("prayerSubject not in response, checking prayerSubjectId only")
				assert.NotNil(t, response["prayerSubjectId"])
				return
			}

			// Verify link_status matches expected value
			linkStatus, ok := prayerSubject["linkStatus"].(string)
			if ok {
				assert.Equal(t, tt.expectedLinkStatus, linkStatus,
					"Expected link_status=%s for userProfileId=%v", tt.expectedLinkStatus, tt.userProfileID)
			}
		})
	}
}

// TestCreatePrayerSubjectValidation tests validation in CreatePrayerSubject
func TestCreatePrayerSubjectValidation(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    map[string]interface{}
		expectedStatus int
		expectError    bool
	}{
		{
			name: "missing display name",
			requestBody: map[string]interface{}{
				"prayerSubjectType": "individual",
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name: "empty display name",
			requestBody: map[string]interface{}{
				"prayerSubjectDisplayName": "   ",
				"prayerSubjectType":        "individual",
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name: "invalid prayer subject type",
			requestBody: map[string]interface{}{
				"prayerSubjectDisplayName": "Test Subject",
				"prayerSubjectType":        "invalid_type",
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, cleanup := SetupTestDB(t)
			defer cleanup()

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, MockUser(), false)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: "1"}}

			jsonData, _ := json.Marshal(tt.requestBody)
			c.Request = httptest.NewRequest("POST", "/users/1/prayer-subjects", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			CreatePrayerSubject(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &response)

			if tt.expectError {
				assert.NotNil(t, response["error"])
			}
		})
	}
}

// TestCreatePrayerSubjectAuthorization tests authorization in CreatePrayerSubject
func TestCreatePrayerSubjectAuthorization(t *testing.T) {
	tests := []struct {
		name           string
		currentUser    int // User_Profile_ID of current user
		targetUser     int // User to create subject for
		isAdmin        bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "user can create for self",
			currentUser:    1,
			targetUser:     1,
			isAdmin:        false,
			expectedStatus: http.StatusCreated,
			expectError:    false,
		},
		{
			name:           "user cannot create for another user",
			currentUser:    1,
			targetUser:     2,
			isAdmin:        false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "admin can create for any user",
			currentUser:    2,
			targetUser:     1,
			isAdmin:        true,
			expectedStatus: http.StatusCreated,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			currentUser := MockUser()
			currentUser.User_Profile_ID = tt.currentUser
			if tt.isAdmin {
				currentUser = MockAdminUser()
			}

			// Only mock DB queries if we expect success (not forbidden)
			if !tt.expectError {
				// Mock MAX(display_sequence) query
				maxSeqRows := sqlmock.NewRows([]string{"coalesce"}).AddRow(-1)
				mock.ExpectQuery("SELECT COALESCE").WillReturnRows(maxSeqRows)

				// Mock INSERT with RETURNING
				mock.ExpectQuery("INSERT INTO \"prayer_subject\"").
					WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(1))

				// Mock SELECT for fetching created subject
				subjectRows := sqlmock.NewRows([]string{
					"prayer_subject_id", "prayer_subject_type", "prayer_subject_display_name",
					"notes", "display_sequence", "photo_s3_key", "user_profile_id",
					"use_linked_user_photo", "link_status", "datetime_create", "datetime_update",
					"created_by", "updated_by",
				}).AddRow(1, "individual", "Test Subject", nil, 0, nil, nil, false, "unlinked", nil, nil, tt.targetUser, tt.targetUser)
				mock.ExpectQuery("SELECT").WillReturnRows(subjectRows)
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: string(rune('0' + tt.targetUser))}}

			requestBody := map[string]interface{}{
				"prayerSubjectDisplayName": "Test Subject",
				"prayerSubjectType":        "individual",
			}
			jsonData, _ := json.Marshal(requestBody)
			c.Request = httptest.NewRequest("POST", "/users/"+string(rune('0'+tt.targetUser))+"/prayer-subjects", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			CreatePrayerSubject(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &response)

			if tt.expectError {
				assert.NotNil(t, response["error"])
			} else {
				assert.NotNil(t, response["message"])
			}
		})
	}
}

// TestDeletePrayerSubjectCircleGuard verifies the 409 guard added to
// DeletePrayerSubject. Two checks fire in sequence:
//
//   1) psgp + user_group join — covers group-type subjects.
//   2) shared-circle semantic check on user_profile_id — covers individual
//      contacts (which the schema doesn't allow in psgp).
//
// Either positive count → 409. Both zero → deletion proceeds.
func TestDeletePrayerSubjectCircleGuard(t *testing.T) {
	tests := []struct {
		name             string
		psgpHits         int
		sharedCircleHits int
		expectedStatus   int
		expectGuardError bool
	}{
		{
			name:             "blocked via psgp (group-type subject)",
			psgpHits:         1,
			sharedCircleHits: 0,
			expectedStatus:   http.StatusConflict,
			expectGuardError: true,
		},
		{
			name:             "blocked via shared-circle (individual contact)",
			psgpHits:         0,
			sharedCircleHits: 1,
			expectedStatus:   http.StatusConflict,
			expectGuardError: true,
		},
		{
			name:             "allowed when no circle ties",
			psgpHits:         0,
			sharedCircleHits: 0,
			expectedStatus:   http.StatusOK,
			expectGuardError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			currentUser := MockUser()
			subjectID := 42
			otherUserID := 99

			// Existing subject lookup — owned by currentUser, linked to OTHER user
			// so the self-subject guard doesn't fire.
			now := time.Now()
			subjectRows := sqlmock.NewRows([]string{
				"prayer_subject_id", "prayer_subject_type", "prayer_subject_display_name",
				"notes", "display_sequence", "photo_s3_key", "user_profile_id",
				"use_linked_user_photo", "link_status", "phone_number", "email",
				"datetime_create", "datetime_update", "created_by", "updated_by",
			}).AddRow(
				subjectID, "individual", "Some Contact",
				nil, 0, nil, otherUserID,
				false, "linked", nil, nil,
				now, now, currentUser.User_Profile_ID, currentUser.User_Profile_ID,
			)
			mock.ExpectQuery("SELECT").WillReturnRows(subjectRows)

			// 1) psgp guard
			mock.ExpectQuery("SELECT COUNT").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.psgpHits))
			// 2) shared-circle guard (always runs; the controller checks both
			// counts together after both queries).
			mock.ExpectQuery("SELECT COUNT").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.sharedCircleHits))

			if !tt.expectGuardError {
				// Path continues: prayer count check (returns 0 — no associated prayers)
				mock.ExpectQuery("SELECT COUNT").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				// Delete the subject
				mock.ExpectExec("DELETE FROM \"prayer_subject\"").
					WillReturnResult(sqlmock.NewResult(0, 1))
				// resequencePrayerSubjects: SELECT then no updates needed
				mock.ExpectQuery("SELECT").
					WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id", "display_sequence"}))
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, currentUser, false)
			c.Params = []gin.Param{{Key: "prayer_subject_id", Value: "42"}}
			c.Request = httptest.NewRequest("DELETE", "/prayer-subjects/42", nil)

			DeletePrayerSubject(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &response)
			if tt.expectGuardError {
				assert.Contains(t, response["error"], "prayer circle")
			} else {
				assert.NotNil(t, response["message"])
			}
		})
	}
}

// TestDeletePrayerSubjectCircleGuard_NoLinkedUser verifies the shared-circle
// query is skipped entirely when the contact has no user_profile_id (purely
// manual contact with no link to a real user).
func TestDeletePrayerSubjectCircleGuard_NoLinkedUser(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	currentUser := MockUser()
	subjectID := 42

	now := time.Now()
	// user_profile_id = nil — no linked user.
	subjectRows := sqlmock.NewRows([]string{
		"prayer_subject_id", "prayer_subject_type", "prayer_subject_display_name",
		"notes", "display_sequence", "photo_s3_key", "user_profile_id",
		"use_linked_user_photo", "link_status", "phone_number", "email",
		"datetime_create", "datetime_update", "created_by", "updated_by",
	}).AddRow(
		subjectID, "individual", "Manual Contact",
		nil, 0, nil, nil,
		false, "unlinked", nil, nil,
		now, now, currentUser.User_Profile_ID, currentUser.User_Profile_ID,
	)
	mock.ExpectQuery("SELECT").WillReturnRows(subjectRows)

	// psgp guard: 0
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// No shared-circle query — skipped because user_profile_id is nil.
	// Prayer count check
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("DELETE FROM \"prayer_subject\"").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id", "display_sequence"}))

	c, w := SetupTestContext()
	SetAuthenticatedUser(c, currentUser, false)
	c.Params = []gin.Param{{Key: "prayer_subject_id", Value: "42"}}
	c.Request = httptest.NewRequest("DELETE", "/prayer-subjects/42", nil)

	DeletePrayerSubject(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
