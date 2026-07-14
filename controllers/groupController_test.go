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

// Test CreateGroup - Create a new group
func TestCreateGroup(t *testing.T) {
	tests := []struct {
		name           string
		currentUser    models.UserProfile
		isAdmin        bool
		groupData      models.GroupCreate
		circleCount    int
		isPremium      bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:        "successful group creation",
			currentUser: MockUser(),
			groupData: models.GroupCreate{
				Group_Name:        "Prayer Warriors",
				Group_Description: "A group for prayer warriors",
			},
			circleCount:    0,
			expectedStatus: http.StatusCreated,
			expectError:    false,
		},
		{
			name:        "group creation with empty description",
			currentUser: MockUser(),
			groupData: models.GroupCreate{
				Group_Name:        "Test Group",
				Group_Description: "",
			},
			circleCount:    2,
			expectedStatus: http.StatusCreated,
			expectError:    false,
		},
		{
			name:        "premium user bypasses circle limit",
			currentUser: MockUser(),
			groupData: models.GroupCreate{
				Group_Name:        "Fourth Circle",
				Group_Description: "",
			},
			circleCount:    3,
			isPremium:      true,
			expectedStatus: http.StatusCreated,
			expectError:    false,
		},
		{
			name:        "admin bypasses circle limit",
			currentUser: MockAdminUser(),
			isAdmin:     true,
			groupData: models.GroupCreate{
				Group_Name:        "Admin Circle",
				Group_Description: "",
			},
			expectedStatus: http.StatusCreated,
			expectError:    false,
		},
		{
			name:        "free user at circle limit is rejected",
			currentUser: MockUser(),
			groupData: models.GroupCreate{
				Group_Name:        "One Too Many",
				Group_Description: "",
			},
			circleCount:    3,
			isPremium:      false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if !tt.isAdmin {
				// Mock circle limit check: active circle count, then premium lookup
				mock.ExpectQuery("SELECT COUNT").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.circleCount))
				premiumRows := sqlmock.NewRows([]string{"is_premium"})
				if tt.isPremium {
					premiumRows.AddRow(true)
				}
				mock.ExpectQuery("SELECT \"is_premium\" FROM \"user_subscription\"").
					WillReturnRows(premiumRows)
			}

			if !tt.expectError {
				// Mock group insert - return group ID
				mock.ExpectQuery("INSERT INTO \"group_profile\"").
					WillReturnRows(sqlmock.NewRows([]string{"group_profile_id"}).AddRow(1))

				// Mock display sequence update for existing groups
				mock.ExpectExec("UPDATE \"user_group\"").
					WillReturnResult(sqlmock.NewResult(0, 0))

				// Mock user_group insert
				mock.ExpectExec("INSERT INTO \"user_group\"").
					WillReturnResult(sqlmock.NewResult(1, 1))

				// Mock prayer_subject insert (auto-created contact card)
				mock.ExpectQuery("INSERT INTO \"prayer_subject\"").
					WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(100))

				// Mock group_profile update to link prayer_subject_id
				mock.ExpectExec("UPDATE \"group_profile\"").
					WillReturnResult(sqlmock.NewResult(0, 1))
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			jsonData, _ := json.Marshal(tt.groupData)
			c.Request = httptest.NewRequest("POST", "/groups", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			CreateGroup(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &response)

			if tt.expectError {
				assert.NotNil(t, response["error"])
			} else {
				assert.NotNil(t, response["groupId"])
			}
		})
	}
}

// Test GetGroup - Get a specific group
func TestGetGroup(t *testing.T) {
	tests := []struct {
		name           string
		groupID        string
		currentUser    models.UserProfile
		isAdmin        bool
		userInGroup    bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful fetch - user in group",
			groupID:        "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "successful fetch - admin not in group",
			groupID:        "1",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			userInGroup:    false,
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:           "unauthorized - user not in group",
			groupID:        "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			expectedStatus: http.StatusUnauthorized,
			expectError:    true,
		},
		{
			name:           "group not found",
			groupID:        "999",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			expectedStatus: http.StatusUnauthorized,
			expectError:    true,
		},
		{
			name:           "invalid group ID",
			groupID:        "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.groupID != "invalid" {
				if tt.userInGroup {
					// Mock successful group fetch
					now := time.Now()
					prayerSubjectID := 100
					rows := sqlmock.NewRows([]string{"group_profile_id", "group_name", "group_description", "is_active", "created_by", "updated_by", "datetime_create", "datetime_update", "prayer_subject_id"}).
						AddRow(1, "Test Group", "A test group", true, 1, 1, now, now, prayerSubjectID)
					mock.ExpectQuery("SELECT").WillReturnRows(rows)
				} else {
					// Mock empty result (group not found or user not in group)
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"group_profile_id", "group_name", "group_description", "is_active", "created_by", "updated_by", "datetime_create", "datetime_update", "prayer_subject_id"}))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "group_profile_id", Value: tt.groupID}}
			c.Request = httptest.NewRequest("GET", "/groups/"+tt.groupID, nil)

			GetGroup(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &response)

			if tt.expectError {
				assert.NotNil(t, response["error"])
			} else {
				assert.NotNil(t, response["groupId"])
			}
		})
	}
}

// Test GetAllGroups - Admin-only get all groups
func TestGetAllGroups(t *testing.T) {
	tests := []struct {
		name           string
		currentUser    models.UserProfile
		isAdmin        bool
		hasGroups      bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful fetch - admin with groups",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			hasGroups:      true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "successful fetch - admin with no groups",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			hasGroups:      false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "unauthorized - non-admin",
			currentUser:    MockUser(),
			isAdmin:        false,
			hasGroups:      false,
			expectedStatus: http.StatusUnauthorized,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.isAdmin {
				if tt.hasGroups {
					now := time.Now()
					prayerSubjectID := 100
					rows := sqlmock.NewRows([]string{"group_profile_id", "group_name", "group_description", "is_active", "datetime_create", "datetime_update", "created_by", "updated_by", "deleted", "prayer_subject_id"}).
						AddRow(1, "Group 1", "Description 1", true, now, now, 1, 1, false, prayerSubjectID).
						AddRow(2, "Group 2", "Description 2", true, now, now, 1, 1, false, prayerSubjectID)
					mock.ExpectQuery("SELECT").WillReturnRows(rows)
				} else {
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"group_profile_id", "group_name", "group_description", "is_active", "datetime_create", "datetime_update", "created_by", "updated_by", "deleted", "prayer_subject_id"}))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Request = httptest.NewRequest("GET", "/groups", nil)

			GetAllGroups(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if !tt.expectError && tt.hasGroups {
				var response []models.GroupProfile
				_ = json.Unmarshal(w.Body.Bytes(), &response)
				assert.Greater(t, len(response), 0)
			}
		})
	}
}

// Test UpdateGroup - Update group (admin or creator)
func TestUpdateGroup(t *testing.T) {
	tests := []struct {
		name           string
		groupID        string
		currentUser    models.UserProfile
		isAdmin        bool
		isCreator      bool
		groupExists    bool
		updateData     models.GroupUpdate
		rowsAffected   int64
		expectedStatus int
		expectError    bool
	}{
		{
			name:        "successful update - admin",
			groupID:     "1",
			currentUser: MockAdminUser(),
			isAdmin:     true,
			isCreator:   false,
			groupExists: true,
			updateData: models.GroupUpdate{
				Group_Name:        "Updated Group",
				Group_Description: "Updated description",
				Is_Active:         true,
			},
			rowsAffected:   1,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "successful update - creator (non-admin)",
			groupID:     "1",
			currentUser: MockUser(),
			isAdmin:     false,
			isCreator:   true,
			groupExists: true,
			updateData: models.GroupUpdate{
				Group_Name:        "Updated Group",
				Group_Description: "Updated description",
				Is_Active:         true,
			},
			rowsAffected:   1,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "unauthorized - not creator and not admin",
			groupID:     "1",
			currentUser: MockUser(),
			isAdmin:     false,
			isCreator:   false,
			groupExists: true,
			updateData: models.GroupUpdate{
				Group_Name:        "Updated Group",
				Group_Description: "Updated description",
				Is_Active:         true,
			},
			expectedStatus: http.StatusUnauthorized,
			expectError:    true,
		},
		{
			name:        "group not found",
			groupID:     "999",
			currentUser: MockAdminUser(),
			isAdmin:     true,
			isCreator:   false,
			groupExists: false,
			updateData: models.GroupUpdate{
				Group_Name:        "Updated Group",
				Group_Description: "Updated description",
				Is_Active:         true,
			},
			rowsAffected:   0,
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:           "invalid group ID",
			groupID:        "invalid",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			isCreator:      false,
			groupExists:    false,
			updateData:     models.GroupUpdate{},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.groupID != "invalid" {
				if tt.groupExists {
					createdBy := 2 // Default to different user
					if tt.isCreator {
						createdBy = tt.currentUser.User_Profile_ID
					}
					// Mock group lookup to get created_by
					rows := sqlmock.NewRows([]string{"created_by"}).
						AddRow(createdBy)
					mock.ExpectQuery("SELECT").WillReturnRows(rows)

					if tt.isAdmin || tt.isCreator {
						mock.ExpectExec("UPDATE \"group_profile\"").
							WillReturnResult(sqlmock.NewResult(0, tt.rowsAffected))
					}
				} else {
					// Mock empty result (group not found)
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"created_by"}))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "group_profile_id", Value: tt.groupID}}
			jsonData, _ := json.Marshal(tt.updateData)
			c.Request = httptest.NewRequest("PATCH", "/groups/"+tt.groupID, bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			UpdateGroup(c)

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

// Test DeleteGroup - Delete group with authorization checks
func TestDeleteGroup(t *testing.T) {
	tests := []struct {
		name           string
		groupID        string
		currentUser    models.UserProfile
		isAdmin        bool
		isCreator      bool
		groupExists    bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful delete - creator",
			groupID:        "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			isCreator:      true,
			groupExists:    true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "successful delete - admin",
			groupID:        "1",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			isCreator:      false,
			groupExists:    true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "unauthorized - not creator and not admin",
			groupID:        "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			isCreator:      false,
			groupExists:    true,
			expectedStatus: http.StatusUnauthorized,
			expectError:    true,
		},
		{
			name:           "group not found",
			groupID:        "999",
			currentUser:    MockUser(),
			isAdmin:        false,
			isCreator:      false,
			groupExists:    false,
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:           "invalid group ID",
			groupID:        "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			isCreator:      false,
			groupExists:    false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.groupID != "invalid" {
				if tt.groupExists {
					createdBy := 2 // Default to different user
					if tt.isCreator {
						createdBy = tt.currentUser.User_Profile_ID
					}
					// Mock group lookup
					rows := sqlmock.NewRows([]string{"created_by", "group_name"}).
						AddRow(createdBy, "Test Group")
					mock.ExpectQuery("SELECT").WillReturnRows(rows)

					if tt.isAdmin || tt.isCreator {
						// Mock fetch group members for email
						mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{}))

						// Mock cascade deletes
						mock.ExpectExec("DELETE FROM \"user_group\"").
							WillReturnResult(sqlmock.NewResult(0, 1))
						mock.ExpectExec("DELETE FROM \"prayer_access\"").
							WillReturnResult(sqlmock.NewResult(0, 0))
						mock.ExpectExec("DELETE FROM \"group_profile\"").
							WillReturnResult(sqlmock.NewResult(0, 1))
					}
				} else {
					// Mock empty result (group not found)
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{}))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "group_profile_id", Value: tt.groupID}}
			c.Request = httptest.NewRequest("DELETE", "/groups/"+tt.groupID, nil)

			DeleteGroup(c)

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

// Test GetGroupUsers - Fetch group members
func TestGetGroupUsers(t *testing.T) {
	tests := []struct {
		name           string
		groupID        string
		hasUsers       bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful fetch - group with users",
			groupID:        "1",
			hasUsers:       true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "no users found",
			groupID:        "1",
			hasUsers:       false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "invalid group ID",
			groupID:        "invalid",
			hasUsers:       false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.groupID != "invalid" {
				if tt.hasUsers {
					rows := sqlmock.NewRows([]string{"user_profile_id", "username", "email", "first_name", "last_name", "created_by", "updated_by"}).
						AddRow(1, "testuser", "test@example.com", "Test", "User", 1, 1).
						AddRow(2, "testuser2", "test2@example.com", "Test2", "User2", 1, 1)
					mock.ExpectQuery("SELECT").WillReturnRows(rows)
				} else {
					rows := sqlmock.NewRows([]string{"user_profile_id", "username", "email", "first_name", "last_name", "created_by", "updated_by"})
					mock.ExpectQuery("SELECT").WillReturnRows(rows)
				}
			}

			c, w := SetupTestContext()
			c.Params = []gin.Param{{Key: "group_profile_id", Value: tt.groupID}}
			c.Request = httptest.NewRequest("GET", "/groups/"+tt.groupID+"/users", nil)

			GetGroupUsers(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectError {
				var response map[string]interface{}
				_ = json.Unmarshal(w.Body.Bytes(), &response)
				assert.NotNil(t, response["error"])
			} else if tt.hasUsers {
				// Response should be an array of users
				var users []interface{}
				_ = json.Unmarshal(w.Body.Bytes(), &users)
				assert.Greater(t, len(users), 0)
			} else {
				// No users found returns an empty array
				var users []interface{}
				_ = json.Unmarshal(w.Body.Bytes(), &users)
				assert.Equal(t, 0, len(users))
			}
		})
	}
}

// Test AddUserToGroup - Add member to group
func TestAddUserToGroup(t *testing.T) {
	tests := []struct {
		name           string
		groupID        string
		userID         string
		currentUser    models.UserProfile
		isAdmin        bool
		userExists     bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful add - admin adding user",
			groupID:        "1",
			userID:         "2",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			userExists:     false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "successful add - user adding self",
			groupID:        "1",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userExists:     false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "forbidden - non-admin adding other user",
			groupID:        "1",
			userID:         "2",
			currentUser:    MockUser(),
			isAdmin:        false,
			userExists:     false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "conflict - user already in group",
			groupID:        "1",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userExists:     true,
			expectedStatus: http.StatusConflict,
			expectError:    true,
		},
		{
			name:           "invalid group ID",
			groupID:        "invalid",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userExists:     false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:           "invalid user ID",
			groupID:        "1",
			userID:         "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			userExists:     false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.groupID != "invalid" && tt.userID != "invalid" {
				if tt.isAdmin || tt.userID == "1" {
					if tt.userExists {
						// Mock user already exists in group
						rows := sqlmock.NewRows([]string{"user_profile_id", "group_profile_id"}).
							AddRow(1, 1)
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
					} else {
						// Mock user not in group
						mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"user_profile_id", "group_profile_id"}))
						// Mock display sequence update
						mock.ExpectExec("UPDATE \"user_group\"").
							WillReturnResult(sqlmock.NewResult(0, 0))
						// Mock insert
						mock.ExpectExec("INSERT INTO \"user_group\"").
							WillReturnResult(sqlmock.NewResult(1, 1))
					}
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{
				{Key: "group_profile_id", Value: tt.groupID},
				{Key: "user_profile_id", Value: tt.userID},
			}
			c.Request = httptest.NewRequest("POST", "/groups/"+tt.groupID+"/users/"+tt.userID, nil)

			AddUserToGroup(c)

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

// Test RemoveUserFromGroup - Remove member from group
func TestRemoveUserFromGroup(t *testing.T) {
	tests := []struct {
		name           string
		groupID        string
		userID         string
		currentUser    models.UserProfile
		isAdmin        bool
		userInGroup    bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful remove - admin removing user",
			groupID:        "1",
			userID:         "2",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			userInGroup:    true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "successful remove - user leaving group",
			groupID:        "1",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "forbidden - non-admin removing other user",
			groupID:        "1",
			userID:         "2",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    true,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "not found - user not in group",
			groupID:        "1",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:           "invalid group ID",
			groupID:        "invalid",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:           "invalid user ID",
			groupID:        "1",
			userID:         "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.groupID != "invalid" && tt.userID != "invalid" {
				if tt.isAdmin || tt.userID == "1" {
					now := time.Now()
					phone := "1234567890"
					// Mock user fetch for email
					userRows := sqlmock.NewRows([]string{"user_profile_id", "username", "first_name", "last_name", "email", "phone_number", "admin", "created_by", "updated_by", "datetime_create", "datetime_update"}).
						AddRow(1, "testuser", "Test", "User", "test@example.com", phone, false, 1, 1, now, now)
					mock.ExpectQuery("SELECT").WillReturnRows(userRows)

					// Mock group fetch for email
					groupRows := sqlmock.NewRows([]string{"group_name"}).
						AddRow("Test Group")
					mock.ExpectQuery("SELECT").WillReturnRows(groupRows)

					// Mock delete
					if tt.userInGroup {
						mock.ExpectExec("DELETE FROM \"user_group\"").
							WillReturnResult(sqlmock.NewResult(0, 1))

						// Mock GetOtherGroupMemberIDs for push notification (runs in goroutine)
						mock.ExpectQuery("SELECT \"user_profile_id\" FROM \"user_group\"").
							WillReturnRows(sqlmock.NewRows([]string{"user_profile_id"}).AddRow(2).AddRow(3))
					} else {
						mock.ExpectExec("DELETE FROM \"user_group\"").
							WillReturnResult(sqlmock.NewResult(0, 0))
					}
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{
				{Key: "group_profile_id", Value: tt.groupID},
				{Key: "user_profile_id", Value: tt.userID},
			}
			c.Request = httptest.NewRequest("DELETE", "/groups/"+tt.groupID+"/users/"+tt.userID, nil)

			RemoveUserFromGroup(c)

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

// Test GetGroupPrayers - Fetch prayers for a group
func TestGetGroupPrayers(t *testing.T) {
	tests := []struct {
		name           string
		groupID        string
		currentUser    models.UserProfile
		isAdmin        bool
		userInGroup    bool
		groupExists    bool
		hasPrayers     bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful fetch - user in group with prayers",
			groupID:        "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    true,
			groupExists:    true,
			hasPrayers:     true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "successful fetch - admin not in group",
			groupID:        "1",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			userInGroup:    false,
			groupExists:    true,
			hasPrayers:     true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "no prayers found",
			groupID:        "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    true,
			groupExists:    true,
			hasPrayers:     false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "forbidden - user not in group",
			groupID:        "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			groupExists:    true,
			hasPrayers:     false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "group doesn't exist",
			groupID:        "999",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			groupExists:    false,
			hasPrayers:     false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:           "invalid group ID",
			groupID:        "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			groupExists:    false,
			hasPrayers:     false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.groupID != "invalid" {
				if tt.groupExists {
					// Mock group exists check (returns COUNT)
					groupRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
					mock.ExpectQuery("SELECT").WillReturnRows(groupRows)

					// Mock user in group check (returns COUNT) - always called even for admins
					if tt.userInGroup {
						userGroupRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
						mock.ExpectQuery("SELECT").WillReturnRows(userGroupRows)
					} else {
						mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					}

					if tt.userInGroup || tt.isAdmin {
						if tt.hasPrayers {
							now := time.Now()
							isPrivate := false
							isAnswered := false
							priority := 1
							prayerRows := sqlmock.NewRows([]string{
								"prayer_id", "prayer_access_id", "display_sequence", "prayer_type",
								"is_private", "title", "prayer_description", "is_answered",
								"prayer_priority", "datetime_answered", "created_by",
								"datetime_create", "updated_by", "datetime_update", "deleted",
							}).AddRow(1, 1, 0, "general", &isPrivate, "Test Prayer", "Please pray", &isAnswered, &priority, nil, 1, now, 1, now, false)
							mock.ExpectQuery("SELECT").WillReturnRows(prayerRows)
						} else {
							mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
								"prayer_id", "prayer_access_id", "display_sequence", "prayer_type",
								"is_private", "title", "prayer_description", "is_answered",
								"prayer_priority", "datetime_answered", "created_by",
								"datetime_create", "updated_by", "datetime_update", "deleted",
							}))
						}
					}
				} else {
					// Mock group doesn't exist
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "group_profile_id", Value: tt.groupID}}
			c.Request = httptest.NewRequest("GET", "/groups/"+tt.groupID+"/prayers", nil)

			GetGroupPrayers(c)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

// Test CreateGroupPrayer - Create a prayer in a group
func TestCreateGroupPrayer(t *testing.T) {
	tests := []struct {
		name           string
		groupID        string
		currentUser    models.UserProfile
		isAdmin        bool
		userInGroup    bool
		groupExists    bool
		prayerData     map[string]interface{}
		expectedStatus int
		expectError    bool
	}{
		{
			name:        "successful create - user in group",
			groupID:     "1",
			currentUser: MockUser(),
			isAdmin:     false,
			userInGroup: true,
			groupExists: true,
			prayerData: map[string]interface{}{
				"prayerType":        "general",
				"title":             "Test Prayer",
				"prayerDescription": "Please pray for this",
				"isPrivate":         false,
				"isAnswered":        false,
				"prayerPriority":    1,
			},
			expectedStatus: http.StatusCreated,
			expectError:    false,
		},
		{
			name:        "successful create - admin not in group",
			groupID:     "1",
			currentUser: MockAdminUser(),
			isAdmin:     true,
			userInGroup: false,
			groupExists: true,
			prayerData: map[string]interface{}{
				"prayerType":        "general",
				"title":             "Test Prayer",
				"prayerDescription": "Please pray for this",
			},
			expectedStatus: http.StatusCreated,
			expectError:    false,
		},
		{
			name:           "forbidden - user not in group",
			groupID:        "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			groupExists:    true,
			prayerData:     map[string]interface{}{},
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "group doesn't exist",
			groupID:        "999",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			groupExists:    false,
			prayerData:     map[string]interface{}{},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:           "invalid group ID",
			groupID:        "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			groupExists:    false,
			prayerData:     map[string]interface{}{},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:           "invalid JSON",
			groupID:        "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    true,
			groupExists:    true,
			prayerData:     nil,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.groupID != "invalid" {
				if tt.groupExists {
					// Mock group exists check (returns COUNT)
					groupRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
					mock.ExpectQuery("SELECT").WillReturnRows(groupRows)

					// Mock user in group check (returns COUNT) - always called even for admins
					if tt.userInGroup {
						userGroupRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
						mock.ExpectQuery("SELECT").WillReturnRows(userGroupRows)
					} else {
						mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					}

					if (tt.userInGroup || tt.isAdmin) && tt.prayerData != nil && len(tt.prayerData) > 0 {
						// Mock prayer_subject lookup - return existing subject ID
						mock.ExpectQuery("SELECT \"prayer_subject_id\" FROM \"prayer_subject\"").
							WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(1))

						// Mock subject_display_sequence update for prayers in this subject
						mock.ExpectExec("UPDATE \"prayer\" SET \"subject_display_sequence\"").
							WillReturnResult(sqlmock.NewResult(0, 0))

						// Mock prayer insert
						mock.ExpectQuery("INSERT INTO \"prayer\"").
							WillReturnRows(sqlmock.NewRows([]string{"prayer_id"}).AddRow(1))

						// Mock display sequence update
						mock.ExpectExec("UPDATE \"prayer_access\"").
							WillReturnResult(sqlmock.NewResult(0, 0))

						// Mock prayer access insert
						mock.ExpectQuery("INSERT INTO \"prayer_access\"").
							WillReturnRows(sqlmock.NewRows([]string{"prayer_access_id"}).AddRow(1))

						// Mock GetGroupNameByID for push notification (runs in goroutine)
						mock.ExpectQuery("SELECT \"group_name\" FROM \"group_profile\"").
							WillReturnRows(sqlmock.NewRows([]string{"group_name"}).AddRow("Test Group"))

						// Mock GetOtherGroupMemberIDs for push notification (runs in goroutine)
						mock.ExpectQuery("SELECT \"user_profile_id\" FROM \"user_group\"").
							WillReturnRows(sqlmock.NewRows([]string{"user_profile_id"}).AddRow(2).AddRow(3))
					}
				} else {
					// Mock group doesn't exist
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "group_profile_id", Value: tt.groupID}}

			var jsonData []byte
			if tt.prayerData != nil {
				jsonData, _ = json.Marshal(tt.prayerData)
			} else {
				jsonData = []byte("invalid json")
			}
			c.Request = httptest.NewRequest("POST", "/groups/"+tt.groupID+"/prayers", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			CreateGroupPrayer(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &response)

			if tt.expectError {
				assert.NotNil(t, response["error"])
			} else {
				assert.NotNil(t, response["prayerId"])
				assert.NotNil(t, response["prayerAccessId"])
			}
		})
	}
}

// Test ReorderGroupPrayers - Reorder prayers in a group
func TestReorderGroupPrayers(t *testing.T) {
	tests := []struct {
		name           string
		groupID        string
		currentUser    models.UserProfile
		isAdmin        bool
		userInGroup    bool
		groupExists    bool
		reorderData    map[string]interface{}
		totalPrayers   int
		expectedStatus int
		expectError    bool
	}{
		{
			name:        "successful reorder",
			groupID:     "1",
			currentUser: MockUser(),
			isAdmin:     false,
			userInGroup: true,
			groupExists: true,
			reorderData: map[string]interface{}{
				"prayers": []map[string]int{
					{"prayerId": 1, "displaySequence": 0},
					{"prayerId": 2, "displaySequence": 1},
				},
			},
			totalPrayers:   2,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "successful reorder - admin not in group",
			groupID:     "1",
			currentUser: MockAdminUser(),
			isAdmin:     true,
			userInGroup: false,
			groupExists: true,
			reorderData: map[string]interface{}{
				"prayers": []map[string]int{
					{"prayerId": 1, "displaySequence": 0},
				},
			},
			totalPrayers:   1,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "forbidden - user not in group",
			groupID:        "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			groupExists:    true,
			reorderData:    map[string]interface{}{},
			totalPrayers:   0,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "group doesn't exist",
			groupID:        "999",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			groupExists:    false,
			reorderData:    map[string]interface{}{},
			totalPrayers:   0,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "invalid reorder - missing prayers",
			groupID:     "1",
			currentUser: MockUser(),
			isAdmin:     false,
			userInGroup: true,
			groupExists: true,
			reorderData: map[string]interface{}{
				"prayers": []map[string]int{
					{"prayerId": 1, "displaySequence": 0},
				},
			},
			totalPrayers:   2,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "invalid reorder - duplicate sequence",
			groupID:     "1",
			currentUser: MockUser(),
			isAdmin:     false,
			userInGroup: true,
			groupExists: true,
			reorderData: map[string]interface{}{
				"prayers": []map[string]int{
					{"prayerId": 1, "displaySequence": 0},
					{"prayerId": 2, "displaySequence": 0},
				},
			},
			totalPrayers:   2,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "invalid reorder - out of range sequence",
			groupID:     "1",
			currentUser: MockUser(),
			isAdmin:     false,
			userInGroup: true,
			groupExists: true,
			reorderData: map[string]interface{}{
				"prayers": []map[string]int{
					{"prayerId": 1, "displaySequence": 0},
					{"prayerId": 2, "displaySequence": 5},
				},
			},
			totalPrayers:   2,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:           "invalid group ID",
			groupID:        "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    false,
			groupExists:    false,
			reorderData:    map[string]interface{}{},
			totalPrayers:   0,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.groupID != "invalid" {
				if tt.groupExists {
					// Mock group exists check (returns COUNT)
					groupRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
					mock.ExpectQuery("SELECT").WillReturnRows(groupRows)

					// Mock user in group check (returns COUNT) - always called even for admins
					if tt.userInGroup {
						userGroupRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
						mock.ExpectQuery("SELECT").WillReturnRows(userGroupRows)
					} else {
						mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					}

					if tt.userInGroup || tt.isAdmin {
						// Mock prayer count query
						countRows := sqlmock.NewRows([]string{"count"}).AddRow(tt.totalPrayers)
						mock.ExpectQuery("SELECT").WillReturnRows(countRows)

						// If reorder data is valid, mock update queries
						if prayers, ok := tt.reorderData["prayers"].([]map[string]int); ok {
							if len(prayers) == tt.totalPrayers {
								for range prayers {
									mock.ExpectExec("UPDATE \"prayer_access\"").
										WillReturnResult(sqlmock.NewResult(0, 1))
								}
							}
						}
					}
				} else {
					// Mock group doesn't exist
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "group_profile_id", Value: tt.groupID}}

			jsonData, _ := json.Marshal(tt.reorderData)
			c.Request = httptest.NewRequest("PATCH", "/groups/"+tt.groupID+"/prayers/reorder", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			ReorderGroupPrayers(c)

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
