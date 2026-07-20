package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/PrayerLoop/models"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// Test GetPrayer - Get a single prayer with authorization
func TestGetPrayer(t *testing.T) {
	tests := []struct {
		name           string
		prayerID       string
		currentUser    models.UserProfile
		isAdmin        bool
		prayerExists   bool
		hasAccess      bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful retrieval - user with access",
			prayerID:       "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			prayerExists:   true,
			hasAccess:      true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "successful retrieval - admin without access",
			prayerID:       "1",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			prayerExists:   true,
			hasAccess:      false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "unauthorized - user without access",
			prayerID:       "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			prayerExists:   true,
			hasAccess:      false,
			expectedStatus: http.StatusUnauthorized,
			expectError:    true,
		},
		{
			name:           "prayer not found",
			prayerID:       "999",
			currentUser:    MockUser(),
			isAdmin:        false,
			prayerExists:   false,
			hasAccess:      false,
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:           "invalid prayer ID",
			prayerID:       "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			prayerExists:   false,
			hasAccess:      false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.prayerID != "invalid" {
				now := time.Now()

				if tt.prayerExists {
					// Mock GetPrayer query (returns UserPrayer records from complex JOIN)
					if tt.hasAccess || tt.isAdmin {
						// Return UserPrayer record with user_profile_id matching current user
						prayerRows := sqlmock.NewRows([]string{
							"user_profile_id", "prayer_id", "prayer_type", "is_private", "title",
							"prayer_description", "is_answered", "prayer_priority", "datetime_answered",
							"created_by", "datetime_create", "updated_by", "datetime_update", "deleted",
						}).AddRow(1, 1, "personal", false, "Test Prayer", "Please pray for this", false, 1, nil, 1, now, 1, now, false)
						mock.ExpectQuery("SELECT").WillReturnRows(prayerRows)
					} else {
						// Return UserPrayer record but with different user_profile_id (no access)
						prayerRows := sqlmock.NewRows([]string{
							"user_profile_id", "prayer_id", "prayer_type", "is_private", "title",
							"prayer_description", "is_answered", "prayer_priority", "datetime_answered",
							"created_by", "datetime_create", "updated_by", "datetime_update", "deleted",
						}).AddRow(2, 1, "personal", false, "Test Prayer", "Please pray for this", false, 1, nil, 1, now, 1, now, false)
						mock.ExpectQuery("SELECT").WillReturnRows(prayerRows)
					}
				} else {
					// Mock prayer not found (empty result)
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
						"user_profile_id", "prayer_id", "prayer_type", "is_private", "title",
						"prayer_description", "is_answered", "prayer_priority", "datetime_answered",
						"created_by", "datetime_create", "updated_by", "datetime_update", "deleted",
					}))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "prayer_id", Value: tt.prayerID}}
			c.Request = httptest.NewRequest("GET", "/prayers/"+tt.prayerID, nil)

			GetPrayer(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectError {
				var response map[string]interface{}
				_ = json.Unmarshal(w.Body.Bytes(), &response)
				assert.NotNil(t, response["error"])
			} else {
				var prayer map[string]interface{}
				_ = json.Unmarshal(w.Body.Bytes(), &prayer)
				assert.NotNil(t, prayer["prayerId"]) // JSON uses camelCase
			}
		})
	}
}

// Test GetPrayers - Get all prayers for the authenticated user
func TestGetPrayers(t *testing.T) {
	tests := []struct {
		name           string
		currentUser    models.UserProfile
		hasPrayers     bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful retrieval with prayers",
			currentUser:    MockUser(),
			hasPrayers:     true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "no prayers found",
			currentUser:    MockUser(),
			hasPrayers:     false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			now := time.Now()

			if tt.hasPrayers {
				// Mock prayers fetch with results
				prayerRows := sqlmock.NewRows([]string{
					"user_profile_id", "prayer_id", "prayer_type", "is_private", "title",
					"prayer_description", "is_answered", "prayer_priority", "datetime_answered",
					"created_by", "datetime_create", "updated_by", "datetime_update", "deleted",
				}).AddRow(1, 1, "personal", false, "Test Prayer", "Please pray for this", false, 1, nil, 1, now, 1, now, false)
				mock.ExpectQuery("SELECT").WillReturnRows(prayerRows)
			} else {
				// Mock empty result
				mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
					"user_profile_id", "prayer_id", "prayer_type", "is_private", "title",
					"prayer_description", "is_answered", "prayer_priority", "datetime_answered",
					"created_by", "datetime_create", "updated_by", "datetime_update", "deleted",
				}))
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, false)
			c.Request = httptest.NewRequest("GET", "/prayers", nil)

			GetPrayers(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &response)

			if tt.hasPrayers {
				prayers := response["prayers"].([]interface{})
				assert.Greater(t, len(prayers), 0)
			} else {
				assert.NotNil(t, response["message"])
			}
		})
	}
}

// Test AddPrayerAccess - Share a prayer with a user or group
func TestAddPrayerAccess(t *testing.T) {
	tests := []struct {
		name           string
		prayerID       string
		currentUser    models.UserProfile
		isAdmin        bool
		accessData     models.PrayerAccessCreate
		prayerExists   bool
		accessExists   bool
		hasPermission  bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:        "successful access grant - user with permission",
			prayerID:    "1",
			currentUser: MockUser(),
			isAdmin:     false,
			accessData: models.PrayerAccessCreate{
				Access_Type:    "user",
				Access_Type_ID: 2,
			},
			prayerExists:   true,
			accessExists:   false,
			hasPermission:  true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "successful access grant - admin",
			prayerID:    "1",
			currentUser: MockAdminUser(),
			isAdmin:     true,
			accessData: models.PrayerAccessCreate{
				Access_Type:    "group",
				Access_Type_ID: 1,
			},
			prayerExists:   true,
			accessExists:   false,
			hasPermission:  false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "access already exists",
			prayerID:    "1",
			currentUser: MockUser(),
			isAdmin:     false,
			accessData: models.PrayerAccessCreate{
				Access_Type:    "user",
				Access_Type_ID: 2,
			},
			prayerExists:   true,
			accessExists:   true,
			hasPermission:  true,
			expectedStatus: http.StatusConflict,
			expectError:    true,
		},
		{
			name:        "forbidden - no permission",
			prayerID:    "1",
			currentUser: MockUser(),
			isAdmin:     false,
			accessData: models.PrayerAccessCreate{
				Access_Type:    "user",
				Access_Type_ID: 2,
			},
			prayerExists:   true,
			accessExists:   false,
			hasPermission:  false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:        "prayer not found",
			prayerID:    "999",
			currentUser: MockUser(),
			isAdmin:     false,
			accessData: models.PrayerAccessCreate{
				Access_Type:    "user",
				Access_Type_ID: 2,
			},
			prayerExists:   false,
			accessExists:   false,
			hasPermission:  false,
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:           "invalid prayer ID",
			prayerID:       "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			accessData:     models.PrayerAccessCreate{},
			prayerExists:   false,
			accessExists:   false,
			hasPermission:  false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.prayerID != "invalid" {
				now := time.Now()

				if tt.prayerExists {
					// Mock prayer existence check
					prayerRows := sqlmock.NewRows([]string{
						"prayer_id", "prayer_type", "is_private", "title", "prayer_description",
						"is_answered", "prayer_priority", "datetime_answered", "created_by",
						"datetime_create", "updated_by", "datetime_update", "deleted",
					}).AddRow(1, "personal", false, "Test Prayer", "Please pray for this", false, 1, nil, 1, now, 1, now, false)
					mock.ExpectQuery("SELECT").WillReturnRows(prayerRows)

					// Mock access existence check (for duplicate check)
					if tt.accessExists {
						existingAccessRows := sqlmock.NewRows([]string{
							"prayer_access_id", "prayer_id", "access_type", "access_type_id",
							"datetime_create", "datetime_update", "created_by", "updated_by",
						}).AddRow(1, 1, tt.accessData.Access_Type, tt.accessData.Access_Type_ID, now, now, 1, 1)
						mock.ExpectQuery("SELECT").WillReturnRows(existingAccessRows)
					} else {
						mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
							"prayer_access_id", "prayer_id", "access_type", "access_type_id",
							"datetime_create", "datetime_update", "created_by", "updated_by",
						}))

						// Mock permission check (userHasPrayerAccess COUNT query) -
						// always runs, even for admins
						if tt.hasPermission {
							mock.ExpectQuery("SELECT COUNT").
								WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
						} else {
							// No permission - count 0 (admin check happens after query)
							mock.ExpectQuery("SELECT COUNT").
								WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
						}

						// If authorized (hasPermission or isAdmin), mock the insert
						if tt.hasPermission || tt.isAdmin {
							mock.ExpectQuery("INSERT INTO \"prayer_access\"").
								WillReturnRows(sqlmock.NewRows([]string{"prayer_access_id"}).AddRow(1))
						}
					}
				} else {
					// Mock prayer not found
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
						"prayer_id", "user_profile_id", "prayer_text", "prayer_type",
						"is_answered", "is_private", "is_active", "created_by", "updated_by",
						"datetime_create", "datetime_update", "answered_date", "deleted",
					}))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "prayer_id", Value: tt.prayerID}}

			jsonData, _ := json.Marshal(tt.accessData)
			c.Request = httptest.NewRequest("POST", "/prayers/"+tt.prayerID+"/access", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			AddPrayerAccess(c)

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

// Test RemovePrayerAccess - Remove access to a prayer (with cascade delete for owner)
func TestRemovePrayerAccess(t *testing.T) {
	tests := []struct {
		name           string
		prayerID       string
		accessID       string
		currentUser    models.UserProfile
		prayerExists   bool
		accessExists   bool
		isOwner        bool
		removingOwn    bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful removal of group access",
			prayerID:       "1",
			accessID:       "1",
			currentUser:    MockUser(),
			prayerExists:   true,
			accessExists:   true,
			isOwner:        true,
			removingOwn:    false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "owner removes own access - cascade delete",
			prayerID:       "1",
			accessID:       "1",
			currentUser:    MockUser(),
			prayerExists:   true,
			accessExists:   true,
			isOwner:        true,
			removingOwn:    true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "unauthorized - not prayer owner",
			prayerID:       "1",
			accessID:       "1",
			currentUser:    MockUser(),
			prayerExists:   true,
			accessExists:   true,
			isOwner:        false,
			removingOwn:    false,
			expectedStatus: http.StatusUnauthorized,
			expectError:    true,
		},
		{
			name:           "prayer not found",
			prayerID:       "999",
			accessID:       "1",
			currentUser:    MockUser(),
			prayerExists:   false,
			accessExists:   false,
			isOwner:        false,
			removingOwn:    false,
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:           "access record not found",
			prayerID:       "1",
			accessID:       "999",
			currentUser:    MockUser(),
			prayerExists:   true,
			accessExists:   false,
			isOwner:        true,
			removingOwn:    false,
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:           "invalid prayer ID",
			prayerID:       "invalid",
			accessID:       "1",
			currentUser:    MockUser(),
			prayerExists:   false,
			accessExists:   false,
			isOwner:        false,
			removingOwn:    false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:           "invalid access ID",
			prayerID:       "1",
			accessID:       "invalid",
			currentUser:    MockUser(),
			prayerExists:   true,  // Prayer is fetched before access_id is parsed
			accessExists:   false,
			isOwner:        true,  // Need to mock prayer fetch
			removingOwn:    false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.prayerID != "invalid" {
				now := time.Now()

				if tt.prayerExists {
					// Mock prayer existence check
					ownerID := 1
					if !tt.isOwner {
						ownerID = 2
					}
					prayerRows := sqlmock.NewRows([]string{
						"prayer_id", "prayer_type", "is_private", "title", "prayer_description",
						"is_answered", "prayer_priority", "datetime_answered", "created_by",
						"datetime_create", "updated_by", "datetime_update", "deleted",
					}).AddRow(1, "personal", false, "Test Prayer", "Please pray for this", false, 1, nil, ownerID, now, ownerID, now, false)
					mock.ExpectQuery("SELECT").WillReturnRows(prayerRows)

					// Mock access record fetch (runs for both owner and non-owner cases)
					// Skip if access_id is invalid (controller fails at parameter parsing)
					if tt.accessID != "invalid" && tt.accessExists {
							// Mock access record fetch with correct field names
							accessType := "user"
							accessTypeID := 2
							if tt.removingOwn {
								accessTypeID = 1 // Same as owner
							}

							// For the test case names, determine if this is a group or user access
							if tt.name == "successful removal of group access" {
								accessType = "group"
								accessTypeID = 1
							}

							accessRows := sqlmock.NewRows([]string{
								"prayer_access_id", "prayer_id", "access_type", "access_type_id",
								"datetime_create", "datetime_update", "created_by", "updated_by",
							}).AddRow(1, 1, accessType, accessTypeID, now, now, 1, 1)
							mock.ExpectQuery("SELECT").WillReturnRows(accessRows)

							// If access_type is "group", mock group fetch and isUserInGroup check
							if accessType == "group" {
								groupRows := sqlmock.NewRows([]string{
									"group_profile_id", "group_name", "group_description", "is_active",
									"datetime_create", "datetime_update", "created_by", "updated_by", "deleted",
								}).AddRow(1, "Test Group", "A test group", true, now, now, 1, 1, false)
								mock.ExpectQuery("SELECT").WillReturnRows(groupRows)

								// Mock isUserInGroup check (returns COUNT)
								userGroupRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
								mock.ExpectQuery("SELECT").WillReturnRows(userGroupRows)
							}

							// Only mock DELETE/UPDATE for authorized users
							if tt.isOwner {
								if tt.removingOwn {
									// Owner removing own access - cascade delete all access records
									mock.ExpectExec("DELETE FROM \"prayer_access\"").
										WillReturnResult(sqlmock.NewResult(0, 2))

									// Soft delete the prayer
									mock.ExpectExec("UPDATE \"prayer\"").
										WillReturnResult(sqlmock.NewResult(0, 1))
								} else {
									// Normal access removal
									mock.ExpectExec("DELETE FROM \"prayer_access\"").
										WillReturnResult(sqlmock.NewResult(0, 1))
								}
							}
					} else if tt.accessID != "invalid" {
						// Mock access record not found (only if access_id is valid)
						mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
							"prayer_access_id", "prayer_id", "access_type", "access_type_id",
							"datetime_create", "datetime_update", "created_by", "updated_by",
						}))
					}
				} else {
					// Mock prayer not found
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
						"prayer_id", "prayer_type", "is_private", "title", "prayer_description",
						"is_answered", "prayer_priority", "datetime_answered", "created_by",
						"datetime_create", "updated_by", "datetime_update", "deleted",
					}))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, false)
			c.Params = []gin.Param{
				{Key: "prayer_id", Value: tt.prayerID},
				{Key: "prayer_access_id", Value: tt.accessID},
			}
			c.Request = httptest.NewRequest("DELETE", "/prayers/"+tt.prayerID+"/access/"+tt.accessID, nil)

			RemovePrayerAccess(c)

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

// Test UpdatePrayer - Update a prayer with partial field updates
func TestUpdatePrayer(t *testing.T) {
	tests := []struct {
		name             string
		prayerID         string
		currentUser      models.UserProfile
		updateData       models.PrayerCreate
		prayerExists     bool
		isCreator        bool
		prayerSubjectID  *int  // Subject ID on the prayer (nil = no subject)
		subjectUserID    *int  // User linked to subject (nil = not linked to current user)
		subjectLinkStatus string // "linked", "pending", "unlinked", or ""
		expectedStatus   int
		expectError      bool
	}{
		{
			name:        "successful update",
			prayerID:    "1",
			currentUser: MockUser(),
			updateData: models.PrayerCreate{
				Title:              "Updated title",
				Prayer_Description: "Updated prayer description",
				Is_Answered:        BoolPtr(true),
			},
			prayerExists:   true,
			isCreator:      true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "unauthorized - not prayer creator",
			prayerID:    "1",
			currentUser: MockUser(),
			updateData: models.PrayerCreate{
				Prayer_Description: "Updated prayer description",
			},
			prayerExists:   true,
			isCreator:      false,
			expectedStatus: http.StatusUnauthorized,
			expectError:    true,
		},
		{
			name:           "prayer not found",
			prayerID:       "999",
			currentUser:    MockUser(),
			updateData:     models.PrayerCreate{},
			prayerExists:   false,
			isCreator:      false,
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:           "invalid prayer ID",
			prayerID:       "invalid",
			currentUser:    MockUser(),
			updateData:     models.PrayerCreate{},
			prayerExists:   false,
			isCreator:      false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		// Subject authorization test cases
		{
			name:        "successful update - linked subject can edit",
			prayerID:    "1",
			currentUser: MockUser(), // User_Profile_ID = 1
			updateData: models.PrayerCreate{
				Prayer_Description: "Subject updated description",
			},
			prayerExists:      true,
			isCreator:         false, // creatorID = 2 (not current user)
			prayerSubjectID:   IntPtr(1),
			subjectUserID:     IntPtr(1), // Linked to current user
			subjectLinkStatus: "linked",
			expectedStatus:    http.StatusOK,
			expectError:       false,
		},
		{
			name:        "unauthorized - pending link cannot edit",
			prayerID:    "1",
			currentUser: MockUser(),
			updateData: models.PrayerCreate{
				Prayer_Description: "Subject updated description",
			},
			prayerExists:      true,
			isCreator:         false,
			prayerSubjectID:   IntPtr(1),
			subjectUserID:     IntPtr(1),
			subjectLinkStatus: "pending",
			expectedStatus:    http.StatusUnauthorized,
			expectError:       true,
		},
		{
			name:        "unauthorized - unlinked subject cannot edit",
			prayerID:    "1",
			currentUser: MockUser(),
			updateData: models.PrayerCreate{
				Prayer_Description: "Subject updated description",
			},
			prayerExists:      true,
			isCreator:         false,
			prayerSubjectID:   IntPtr(1),
			subjectUserID:     IntPtr(1),
			subjectLinkStatus: "unlinked",
			expectedStatus:    http.StatusUnauthorized,
			expectError:       true,
		},
		{
			name:        "forbidden - subject cannot change prayer_subject_id",
			prayerID:    "1",
			currentUser: MockUser(),
			updateData: models.PrayerCreate{
				Prayer_Description: "Subject updated description",
				Prayer_Subject_ID:  IntPtr(99), // Attempting to change subject
			},
			prayerExists:      true,
			isCreator:         false,
			prayerSubjectID:   IntPtr(1),
			subjectUserID:     IntPtr(1),
			subjectLinkStatus: "linked",
			expectedStatus:    http.StatusForbidden,
			expectError:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.prayerID != "invalid" {
				now := time.Now()

				if tt.prayerExists {
					// Mock prayer existence check
					creatorID := 1
					if !tt.isCreator {
						creatorID = 2
					}
					prayerRows := sqlmock.NewRows([]string{
						"prayer_id", "prayer_type", "is_private", "title", "prayer_description",
						"is_answered", "prayer_priority", "datetime_answered", "created_by",
						"datetime_create", "updated_by", "datetime_update", "deleted",
						"prayer_subject_id", "subject_display_sequence",
					}).AddRow(1, "personal", false, "Original prayer title", "Original prayer description", false, 1, nil, creatorID, now, creatorID, now, false, tt.prayerSubjectID, 0)
					mock.ExpectQuery("SELECT").WillReturnRows(prayerRows)

					// If not creator and has subject, mock subject lookup
					if !tt.isCreator && tt.prayerSubjectID != nil {
						subjectRows := sqlmock.NewRows([]string{
							"prayer_subject_id", "user_profile_id", "link_status",
						}).AddRow(*tt.prayerSubjectID, tt.subjectUserID, tt.subjectLinkStatus)
						mock.ExpectQuery("SELECT .* FROM \"prayer_subject\"").WillReturnRows(subjectRows)
					}

					// Mock update if authorized (creator OR linked subject without forbidden action)
					isLinkedSubject := tt.subjectLinkStatus == "linked" && tt.subjectUserID != nil && *tt.subjectUserID == 1
					isForbidden := tt.expectedStatus == http.StatusForbidden
					if tt.isCreator || (isLinkedSubject && !isForbidden) {
						mock.ExpectExec("UPDATE \"prayer\"").
							WillReturnResult(sqlmock.NewResult(0, 1))
					}
				} else {
					// Mock prayer not found
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
						"prayer_id", "prayer_type", "is_private", "title", "prayer_description",
						"is_answered", "prayer_priority", "datetime_answered", "created_by",
						"datetime_create", "updated_by", "datetime_update", "deleted",
						"prayer_subject_id", "subject_display_sequence",
					}))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, false)
			c.Params = []gin.Param{{Key: "prayer_id", Value: tt.prayerID}}

			jsonData, _ := json.Marshal(tt.updateData)
			c.Request = httptest.NewRequest("PATCH", "/prayers/"+tt.prayerID, bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			UpdatePrayer(c)

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

// Test DeletePrayer - Soft delete a prayer
func TestDeletePrayer(t *testing.T) {
	tests := []struct {
		name              string
		prayerID          string
		currentUser       models.UserProfile
		prayerExists      bool
		isCreator         bool
		hasAccessRecords  bool
		prayerSubjectID   *int   // Subject ID on the prayer (nil = no subject)
		subjectUserID     *int   // User linked to subject (nil = not linked to current user)
		subjectLinkStatus string // "linked", "pending", "unlinked", or ""
		expectedStatus    int
		expectError       bool
	}{
		{
			name:             "successful deletion",
			prayerID:         "1",
			currentUser:      MockUser(),
			prayerExists:     true,
			isCreator:        true,
			hasAccessRecords: false,
			expectedStatus:   http.StatusOK,
			expectError:      false,
		},
		{
			name:             "conflict - access records exist",
			prayerID:         "1",
			currentUser:      MockUser(),
			prayerExists:     true,
			isCreator:        true,
			hasAccessRecords: true,
			expectedStatus:   http.StatusConflict,
			expectError:      true,
		},
		{
			name:             "unauthorized - not prayer creator",
			prayerID:         "1",
			currentUser:      MockUser(),
			prayerExists:     true,
			isCreator:        false,
			hasAccessRecords: false,
			expectedStatus:   http.StatusUnauthorized,
			expectError:      true,
		},
		{
			name:             "prayer not found",
			prayerID:         "999",
			currentUser:      MockUser(),
			prayerExists:     false,
			isCreator:        false,
			hasAccessRecords: false,
			expectedStatus:   http.StatusNotFound,
			expectError:      true,
		},
		{
			name:             "invalid prayer ID",
			prayerID:         "invalid",
			currentUser:      MockUser(),
			prayerExists:     false,
			isCreator:        false,
			hasAccessRecords: false,
			expectedStatus:   http.StatusBadRequest,
			expectError:      true,
		},
		// Subject authorization test cases
		{
			name:              "successful deletion - linked subject can delete",
			prayerID:          "1",
			currentUser:       MockUser(), // User_Profile_ID = 1
			prayerExists:      true,
			isCreator:         false, // creatorID = 2 (not current user)
			hasAccessRecords:  false,
			prayerSubjectID:   IntPtr(1),
			subjectUserID:     IntPtr(1), // Linked to current user
			subjectLinkStatus: "linked",
			expectedStatus:    http.StatusOK,
			expectError:       false,
		},
		{
			name:              "unauthorized - pending link cannot delete",
			prayerID:          "1",
			currentUser:       MockUser(),
			prayerExists:      true,
			isCreator:         false,
			hasAccessRecords:  false,
			prayerSubjectID:   IntPtr(1),
			subjectUserID:     IntPtr(1),
			subjectLinkStatus: "pending",
			expectedStatus:    http.StatusUnauthorized,
			expectError:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.prayerID != "invalid" {
				now := time.Now()

				if tt.prayerExists {
					// Mock prayer existence check
					creatorID := 1
					if !tt.isCreator {
						creatorID = 2
					}
					prayerRows := sqlmock.NewRows([]string{
						"prayer_id", "prayer_type", "is_private", "title", "prayer_description",
						"is_answered", "prayer_priority", "datetime_answered", "created_by",
						"datetime_create", "updated_by", "datetime_update", "deleted",
						"prayer_subject_id", "subject_display_sequence",
					}).AddRow(1, "personal", false, "Test Prayer", "Please pray for this", false, 1, nil, creatorID, now, creatorID, now, false, tt.prayerSubjectID, 0)
					mock.ExpectQuery("SELECT").WillReturnRows(prayerRows)

					// If not creator and has subject, mock subject lookup
					if !tt.isCreator && tt.prayerSubjectID != nil {
						subjectRows := sqlmock.NewRows([]string{
							"prayer_subject_id", "user_profile_id", "link_status",
						}).AddRow(*tt.prayerSubjectID, tt.subjectUserID, tt.subjectLinkStatus)
						mock.ExpectQuery("SELECT .* FROM \"prayer_subject\"").WillReturnRows(subjectRows)
					}

					// Determine if user is authorized (creator OR linked subject)
					isLinkedSubject := tt.subjectLinkStatus == "linked" && tt.subjectUserID != nil && *tt.subjectUserID == 1
					isAuthorized := tt.isCreator || isLinkedSubject

					if isAuthorized {
						// Mock access records check
						if tt.hasAccessRecords {
							accessRows := sqlmock.NewRows([]string{"count"}).AddRow(2)
							mock.ExpectQuery("SELECT").WillReturnRows(accessRows)
						} else {
							mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

							// Mock soft delete
							mock.ExpectExec("UPDATE \"prayer\"").
								WillReturnResult(sqlmock.NewResult(0, 1))
						}
					}
				} else {
					// Mock prayer not found
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
						"prayer_id", "prayer_type", "is_private", "title", "prayer_description",
						"is_answered", "prayer_priority", "datetime_answered", "created_by",
						"datetime_create", "updated_by", "datetime_update", "deleted",
						"prayer_subject_id", "subject_display_sequence",
					}))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, false)
			c.Params = []gin.Param{{Key: "prayer_id", Value: tt.prayerID}}
			c.Request = httptest.NewRequest("DELETE", "/prayers/"+tt.prayerID, nil)

			DeletePrayer(c)

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

// Helper functions for pointer types
func StrPtr(s string) *string {
	return &s
}

func BoolPtr(b bool) *bool {
	return &b
}

func IntPtr(i int) *int {
	return &i
}
