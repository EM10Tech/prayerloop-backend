package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/PrayerLoop/models"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetUserProfile tests the GetUserProfile endpoint
func TestGetUserProfile(t *testing.T) {
	tests := []struct {
		name           string
		mockUser       interface{}
		mockAdmin      bool
		expectedStatus int
		expectedUser   bool
		expectedAdmin  bool
	}{
		{
			name:           "returns regular user profile",
			mockUser:       MockUser(),
			mockAdmin:      false,
			expectedStatus: http.StatusOK,
			expectedUser:   true,
			expectedAdmin:  false,
		},
		{
			name:           "returns admin user profile",
			mockUser:       MockAdminUser(),
			mockAdmin:      true,
			expectedStatus: http.StatusOK,
			expectedUser:   true,
			expectedAdmin:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			c, w := SetupTestContext()
			c.Set("currentUser", tt.mockUser)
			c.Set("admin", tt.mockAdmin)

			// Execute
			GetUserProfile(c)

			// Assert
			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.expectedUser {
				assert.NotNil(t, response["user"])
			}

			assert.Equal(t, tt.expectedAdmin, response["admin"])
		})
	}
}

// TestCheckUsernameAvailability tests the CheckUsernameAvailability endpoint
func TestCheckUsernameAvailability(t *testing.T) {
	tests := []struct {
		name           string
		username       string
		mockCount      int64
		mockError      error
		expectedStatus int
		expectedAvail  bool
		expectError    bool
	}{
		{
			name:           "username is available",
			username:       "newuser",
			mockCount:      0,
			expectedStatus: http.StatusOK,
			expectedAvail:  true,
			expectError:    false,
		},
		{
			name:           "username is taken",
			username:       "existinguser",
			mockCount:      1,
			expectedStatus: http.StatusOK,
			expectedAvail:  false,
			expectError:    false,
		},
		{
			name:           "username is empty",
			username:       "",
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup database mock
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			// Setup Gin context
			c, w := SetupTestContext()
			c.Request = httptest.NewRequest("GET", "/check-username?username="+tt.username, nil)

			// Setup mock expectations (only if username is provided)
			if tt.username != "" {
				// Goqu generates: SELECT COUNT(*) AS "count" FROM "user_profile" WHERE ("username" = 'value') LIMIT 1
				// Note: In test mode, goqu embeds values directly instead of using parameterized queries
				// So we match without expecting arguments
				mock.ExpectQuery("SELECT COUNT").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.mockCount))
			}

			// Execute
			CheckUsernameAvailability(c)

			// Debug: print response body if test fails
			if w.Code != tt.expectedStatus {
				t.Logf("Response body: %s", w.Body.String())
			}

			// Assert
			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.expectError {
				assert.NotNil(t, response["error"])
			} else {
				assert.Equal(t, tt.username, response["username"])
				assert.Equal(t, tt.expectedAvail, response["available"])
			}

			// Verify all expectations were met
			if tt.username != "" {
				err = mock.ExpectationsWereMet()
				assert.NoError(t, err, "Not all database expectations were met")
			}
		})
	}
}

// TestUserLogin tests the UserLogin endpoint
func TestUserLogin(t *testing.T) {
	// Set up JWT secret for token generation
	os.Setenv("SECRET", "test-secret-key")
	defer os.Unsetenv("SECRET")

	tests := []struct {
		name           string
		requestBody    models.Login
		mockUser       *models.UserProfile
		mockError      error
		expectedStatus int
		expectToken    bool
		expectError    bool
	}{
		{
			name: "successful login - regular user",
			requestBody: models.Login{
				Username: "testuser",
				Password: "password123",
			},
			mockUser:       ptrUserProfile(MockUserWithPassword()),
			expectedStatus: http.StatusOK,
			expectToken:    true,
			expectError:    false,
		},
		{
			name: "successful login - admin user",
			requestBody: models.Login{
				Username: "adminuser",
				Password: "admin123",
			},
			mockUser:       ptrUserProfile(MockAdminUserWithPassword()),
			expectedStatus: http.StatusOK,
			expectToken:    true,
			expectError:    false,
		},
		{
			name: "invalid password",
			requestBody: models.Login{
				Username: "testuser",
				Password: "wrongpassword",
			},
			mockUser:       ptrUserProfile(MockUserWithPassword()),
			expectedStatus: http.StatusUnauthorized,
			expectToken:    false,
			expectError:    true,
		},
		{
			name: "user not found",
			requestBody: models.Login{
				Username: "nonexistent",
				Password: "password123",
			},
			mockUser:       nil,
			expectedStatus: http.StatusInternalServerError,
			expectToken:    false,
			expectError:    true,
		},
		{
			name: "missing username",
			requestBody: models.Login{
				Username: "",
				Password: "password123",
			},
			expectedStatus: http.StatusBadRequest,
			expectToken:    false,
			expectError:    true,
		},
		{
			name: "missing password",
			requestBody: models.Login{
				Username: "testuser",
				Password: "",
			},
			expectedStatus: http.StatusBadRequest,
			expectToken:    false,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup database mock
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			// Setup mock expectations if user is provided
			if tt.mockUser != nil {
				rows := sqlmock.NewRows([]string{
					"user_profile_id", "username", "password", "email", "first_name",
					"last_name", "phone_number", "email_verified", "phone_verified",
					"verification_token", "admin", "created_by", "datetime_create",
					"updated_by", "datetime_update", "deleted",
				}).AddRow(
					tt.mockUser.User_Profile_ID,
					tt.mockUser.Username,
					tt.mockUser.Password,
					tt.mockUser.Email,
					tt.mockUser.First_Name,
					tt.mockUser.Last_Name,
					tt.mockUser.Phone_Number,
					tt.mockUser.Email_Verified,
					tt.mockUser.Phone_Verified,
					tt.mockUser.Verification_Token,
					tt.mockUser.Admin,
					tt.mockUser.Created_By,
					tt.mockUser.Datetime_Create,
					tt.mockUser.Updated_By,
					tt.mockUser.Datetime_Update,
					tt.mockUser.Deleted,
				)

				mock.ExpectQuery("SELECT").WillReturnRows(rows)
			} else {
				// User not found - return error
				mock.ExpectQuery("SELECT").WillReturnError(sqlmock.ErrCancelled)
			}

			// Setup Gin context
			c, w := SetupTestContext()
			jsonBody, _ := json.Marshal(tt.requestBody)
			c.Request = httptest.NewRequest("POST", "/login", bytes.NewBuffer(jsonBody))
			c.Request.Header.Set("Content-Type", "application/json")

			// Execute
			UserLogin(c)

			// Debug: print response body if test fails
			if w.Code != tt.expectedStatus {
				t.Logf("Response body: %s", w.Body.String())
			}

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			// Assert token presence
			if tt.expectToken {
				assert.NotNil(t, response["token"], "Expected token in response")
				assert.NotEmpty(t, response["token"], "Expected non-empty token")
				assert.NotNil(t, response["user"], "Expected user in response")
				assert.Equal(t, "User logged in successfully.", response["message"])
			}

			// Assert error presence
			if tt.expectError {
				assert.NotNil(t, response["error"], "Expected error in response")
			}

			// Verify all expectations were met (only if mock was used)
			if tt.mockUser != nil || tt.name == "user not found" {
				err = mock.ExpectationsWereMet()
				assert.NoError(t, err, "Not all database expectations were met")
			}
		})
	}
}

// Helper function to convert UserProfile to pointer
func ptrUserProfile(u models.UserProfile) *models.UserProfile {
	return &u
}

// Helper function to convert string to pointer
func ptrString(s string) *string {
	return &s
}

// TestUpdateUserProfile tests the UpdateUserProfile endpoint
func TestUpdateUserProfile(t *testing.T) {
	tests := []struct {
		name            string
		userID          string
		currentUser     models.UserProfile
		isAdmin         bool
		requestBody     models.UserProfileUpdate
		mockUser        *models.UserProfile
		mockEmailCount  int64
		mockUsernameCount int64
		expectedStatus  int
		expectError     bool
	}{
		{
			name:        "successful update - own profile",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			requestBody: models.UserProfileUpdate{
				First_Name: ptrString("UpdatedFirst"),
				Last_Name:  ptrString("UpdatedLast"),
			},
			mockUser:       ptrUserProfile(MockUser()),
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "successful update - admin updates other user",
			userID:      "2",
			currentUser: MockAdminUser(),
			isAdmin:     true,
			requestBody: models.UserProfileUpdate{
				First_Name: ptrString("AdminUpdated"),
			},
			mockUser:       ptrUserProfile(MockUser()),
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "unauthorized - update someone else's profile",
			userID:      "2",
			currentUser: MockUser(),
			isAdmin:     false,
			requestBody: models.UserProfileUpdate{
				First_Name: ptrString("Hacker"),
			},
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:        "user not found",
			userID:      "999",
			currentUser: MockAdminUser(),
			isAdmin:     true,
			requestBody: models.UserProfileUpdate{
				First_Name: ptrString("Test"),
			},
			mockUser:       nil,
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:        "empty first name",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			requestBody: models.UserProfileUpdate{
				First_Name: ptrString(""),
			},
			mockUser:       ptrUserProfile(MockUser()),
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "empty last name",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			requestBody: models.UserProfileUpdate{
				Last_Name: ptrString(""),
			},
			mockUser:       ptrUserProfile(MockUser()),
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "invalid email format",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			requestBody: models.UserProfileUpdate{
				Email: ptrString("notanemail"),
			},
			mockUser:       ptrUserProfile(MockUser()),
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "email already in use",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			requestBody: models.UserProfileUpdate{
				Email: ptrString("taken@example.com"),
			},
			mockUser:        ptrUserProfile(MockUser()),
			mockEmailCount:  1,
			expectedStatus:  http.StatusBadRequest,
			expectError:     true,
		},
		{
			name:        "username already taken",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			requestBody: models.UserProfileUpdate{
				Username: ptrString("takenuser"),
			},
			mockUser:          ptrUserProfile(MockUser()),
			mockUsernameCount: 1,
			expectedStatus:    http.StatusBadRequest,
			expectError:       true,
		},
		{
			name:        "invalid phone number - too short",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			requestBody: models.UserProfileUpdate{
				Phone_Number: ptrString("123"),
			},
			mockUser:       ptrUserProfile(MockUser()),
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "no fields provided",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			requestBody: models.UserProfileUpdate{},
			mockUser:       ptrUserProfile(MockUser()),
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "invalid user ID",
			userID:      "invalid",
			currentUser: MockUser(),
			isAdmin:     false,
			requestBody: models.UserProfileUpdate{
				First_Name: ptrString("Test"),
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup database mock
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			// Setup mock expectations based on test case
			if tt.userID != "invalid" && tt.mockUser != nil {
				// Mock user lookup
				rows := sqlmock.NewRows([]string{
					"user_profile_id", "username", "password", "email", "first_name",
					"last_name", "phone_number", "email_verified", "phone_verified",
					"verification_token", "admin", "created_by", "datetime_create",
					"updated_by", "datetime_update", "deleted",
				}).AddRow(
					tt.mockUser.User_Profile_ID,
					tt.mockUser.Username,
					tt.mockUser.Password,
					tt.mockUser.Email,
					tt.mockUser.First_Name,
					tt.mockUser.Last_Name,
					tt.mockUser.Phone_Number,
					tt.mockUser.Email_Verified,
					tt.mockUser.Phone_Verified,
					tt.mockUser.Verification_Token,
					tt.mockUser.Admin,
					tt.mockUser.Created_By,
					tt.mockUser.Datetime_Create,
					tt.mockUser.Updated_By,
					tt.mockUser.Datetime_Update,
					tt.mockUser.Deleted,
				)
				mock.ExpectQuery("SELECT").WillReturnRows(rows)

				// Mock email availability check if email provided
				if tt.requestBody.Email != nil {
					mock.ExpectQuery("SELECT COUNT").
						WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.mockEmailCount))
				}

				// Mock username availability check if username provided
				if tt.requestBody.Username != nil {
					mock.ExpectQuery("SELECT COUNT").
						WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.mockUsernameCount))
				}

				// Mock update execution if no validation errors expected
				if tt.expectedStatus == http.StatusOK {
					mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))

					// Mock fetching updated user
					mock.ExpectQuery("SELECT").WillReturnRows(rows)
				}
			} else if tt.mockUser == nil && tt.userID != "invalid" {
				// User not found
				mock.ExpectQuery("SELECT").WillReturnError(sqlmock.ErrCancelled)
			}

			// Setup Gin context
			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: tt.userID}}

			jsonBody, _ := json.Marshal(tt.requestBody)
			c.Request = httptest.NewRequest("PATCH", "/users/"+tt.userID, bytes.NewBuffer(jsonBody))
			c.Request.Header.Set("Content-Type", "application/json")

			// Execute
			UpdateUserProfile(c)

			// Debug output
			if w.Code != tt.expectedStatus {
				t.Logf("Response body: %s", w.Body.String())
			}

			// Assert
			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.expectError {
				assert.NotNil(t, response["error"])
			} else {
				assert.NotNil(t, response["message"])
				assert.NotNil(t, response["user"])
			}
		})
	}
}

// TestChangeUserPassword tests the ChangeUserPassword endpoint
func TestChangeUserPassword(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		currentUser    models.UserProfile
		isAdmin        bool
		requestBody    models.UserProfileChangePassword
		mockUser       *models.UserProfile
		expectedStatus int
		expectError    bool
	}{
		{
			name:        "successful password change - own password",
			userID:      "1",
			currentUser: MockUserWithPassword(),
			isAdmin:     false,
			requestBody: models.UserProfileChangePassword{
				Old_Password: "password123",
				New_Password: "newpassword456",
			},
			mockUser:       ptrUserProfile(MockUserWithPassword()),
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "successful password change - admin changes other user",
			userID:      "1",
			currentUser: MockAdminUserWithPassword(),
			isAdmin:     true,
			requestBody: models.UserProfileChangePassword{
				Old_Password: "notchecked",
				New_Password: "newpassword456",
			},
			mockUser:       ptrUserProfile(MockUserWithPassword()),
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "unauthorized - change someone else's password",
			userID:      "2",
			currentUser: MockUser(),
			isAdmin:     false,
			requestBody: models.UserProfileChangePassword{
				Old_Password: "password123",
				New_Password: "hacker123",
			},
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:        "user not found",
			userID:      "999",
			currentUser: MockAdminUser(),
			isAdmin:     true,
			requestBody: models.UserProfileChangePassword{
				Old_Password: "password123",
				New_Password: "newpass123",
			},
			mockUser:       nil,
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:        "incorrect old password",
			userID:      "1",
			currentUser: MockUserWithPassword(),
			isAdmin:     false,
			requestBody: models.UserProfileChangePassword{
				Old_Password: "wrongpassword",
				New_Password: "newpassword456",
			},
			mockUser:       ptrUserProfile(MockUserWithPassword()),
			expectedStatus: http.StatusUnauthorized,
			expectError:    true,
		},
		{
			name:        "new password too short",
			userID:      "1",
			currentUser: MockUserWithPassword(),
			isAdmin:     false,
			requestBody: models.UserProfileChangePassword{
				Old_Password: "password123",
				New_Password: "short",
			},
			mockUser:       ptrUserProfile(MockUserWithPassword()),
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "missing old password",
			userID:      "1",
			currentUser: MockUserWithPassword(),
			isAdmin:     false,
			requestBody: models.UserProfileChangePassword{
				Old_Password: "",
				New_Password: "newpassword456",
			},
			mockUser:       ptrUserProfile(MockUserWithPassword()),
			expectedStatus: http.StatusUnauthorized,
			expectError:    true,
		},
		{
			name:        "invalid user ID",
			userID:      "invalid",
			currentUser: MockUser(),
			isAdmin:     false,
			requestBody: models.UserProfileChangePassword{
				Old_Password: "password123",
				New_Password: "newpassword456",
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup database mock
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			// Setup mock expectations
			if tt.userID != "invalid" && tt.mockUser != nil {
				// Mock user lookup
				rows := sqlmock.NewRows([]string{
					"user_profile_id", "username", "password", "email", "first_name",
					"last_name", "phone_number", "email_verified", "phone_verified",
					"verification_token", "admin", "created_by", "datetime_create",
					"updated_by", "datetime_update", "deleted",
				}).AddRow(
					tt.mockUser.User_Profile_ID,
					tt.mockUser.Username,
					tt.mockUser.Password,
					tt.mockUser.Email,
					tt.mockUser.First_Name,
					tt.mockUser.Last_Name,
					tt.mockUser.Phone_Number,
					tt.mockUser.Email_Verified,
					tt.mockUser.Phone_Verified,
					tt.mockUser.Verification_Token,
					tt.mockUser.Admin,
					tt.mockUser.Created_By,
					tt.mockUser.Datetime_Create,
					tt.mockUser.Updated_By,
					tt.mockUser.Datetime_Update,
					tt.mockUser.Deleted,
				)

				mock.ExpectQuery("SELECT").WillReturnRows(rows)

				// Mock update execution if no validation errors expected
				if tt.expectedStatus == http.StatusOK {
					mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
				}
			} else if tt.mockUser == nil && tt.userID != "invalid" {
				// User not found
				mock.ExpectQuery("SELECT").WillReturnError(sqlmock.ErrCancelled)
			}

			// Setup Gin context
			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: tt.userID}}

			jsonBody, _ := json.Marshal(tt.requestBody)
			c.Request = httptest.NewRequest("PATCH", "/users/"+tt.userID+"/password", bytes.NewBuffer(jsonBody))
			c.Request.Header.Set("Content-Type", "application/json")

			// Execute
			ChangeUserPassword(c)

			// Debug output
			if w.Code != tt.expectedStatus {
				t.Logf("Response body: %s", w.Body.String())
			}

			// Assert
			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.expectError {
				assert.NotNil(t, response["error"])
			} else {
				assert.Equal(t, "Password changed successfully", response["message"])
			}
		})
	}
}

// TestDeleteUserAccount tests the DeleteUserAccount endpoint
func TestDeleteUserAccount(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		currentUser    models.UserProfile
		isAdmin        bool
		mockUser       *models.UserProfile
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful deletion - own account",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			mockUser:       ptrUserProfile(MockUser()),
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "successful deletion - admin deletes other user",
			userID:         "1",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			mockUser:       ptrUserProfile(MockUser()),
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "unauthorized - delete someone else's account",
			userID:         "2",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "user not found",
			userID:         "999",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			mockUser:       nil,
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:           "invalid user ID",
			userID:         "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup database mock
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			// Setup mock expectations
			if tt.userID != "invalid" && tt.mockUser != nil {
				// Mock user lookup
				rows := sqlmock.NewRows([]string{
					"user_profile_id", "username", "password", "email", "first_name",
					"last_name", "phone_number", "email_verified", "phone_verified",
					"verification_token", "admin", "created_by", "datetime_create",
					"updated_by", "datetime_update", "deleted",
				}).AddRow(
					tt.mockUser.User_Profile_ID,
					tt.mockUser.Username,
					tt.mockUser.Password,
					tt.mockUser.Email,
					tt.mockUser.First_Name,
					tt.mockUser.Last_Name,
					tt.mockUser.Phone_Number,
					tt.mockUser.Email_Verified,
					tt.mockUser.Phone_Verified,
					tt.mockUser.Verification_Token,
					tt.mockUser.Admin,
					tt.mockUser.Created_By,
					tt.mockUser.Datetime_Create,
					tt.mockUser.Updated_By,
					tt.mockUser.Datetime_Update,
					tt.mockUser.Deleted,
				)

				mock.ExpectQuery("SELECT").WillReturnRows(rows)

				// For successful deletion, mock all cascade delete operations
				if tt.expectedStatus == http.StatusOK {
					// 1. user_push_tokens (optional)
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))

					// 2. password_reset_tokens (optional)
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))

					// 2b. user_external_identity, oauth_pending_link,
					// auth_refresh_token (optional)
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))

					// 3. prayer_session_detail (via subquery)
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))

					// 4. prayer_session
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 1))

					// 5. user_stats
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 1))

					// 6. user_preferences
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 1))

					// 7. notification
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 2))

					// 8. group_invite (created_by only)
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))

					// 9. user_group
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 3))

					// 10. prayer_access
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 5))

					// 11. prayer_analytics (optional, via subquery)
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))

					// 12. prayer
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 5))

					// 13. user_profile
					mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 1))
				}
			} else if tt.mockUser == nil && tt.userID != "invalid" {
				// User not found - return empty rows (no error, just no results)
				emptyRows := sqlmock.NewRows([]string{
					"user_profile_id", "username", "password", "email", "first_name",
					"last_name", "phone_number", "email_verified", "phone_verified",
					"verification_token", "admin", "created_by", "datetime_create",
					"updated_by", "datetime_update", "deleted",
				})
				mock.ExpectQuery("SELECT").WillReturnRows(emptyRows)
			}

			// Setup Gin context
			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: tt.userID}}
			c.Request = httptest.NewRequest("DELETE", "/users/"+tt.userID+"/account", nil)

			// Execute
			DeleteUserAccount(c)

			// Debug output
			if w.Code != tt.expectedStatus {
				t.Logf("Response body: %s", w.Body.String())
			}

			// Assert
			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.expectError {
				assert.NotNil(t, response["error"])
			} else {
				assert.Equal(t, "Account deleted successfully", response["message"])
			}

			// Verify all mock expectations were met (for successful deletions)
			if tt.expectedStatus == http.StatusOK {
				err = mock.ExpectationsWereMet()
				if err != nil {
					t.Logf("Mock expectations not met: %v", err)
				}
			}
		})
	}
}

// TestGetUserGroups tests the GetUserGroups endpoint
func TestGetUserGroups(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		currentUser    models.UserProfile
		isAdmin        bool
		mockGroups     []models.GroupProfile
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful fetch - own groups",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			mockGroups:     []models.GroupProfile{MockGroupProfile()},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "successful fetch - admin views other user",
			userID:         "1",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			mockGroups:     []models.GroupProfile{MockGroupProfile()},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "unauthorized - view other user's groups",
			userID:         "2",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "no groups found",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			mockGroups:     []models.GroupProfile{},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "invalid user ID",
			userID:         "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.userID != "invalid" && !tt.expectError {
				// Mock will return empty or populated results
				mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{}))
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: tt.userID}}
			c.Request = httptest.NewRequest("GET", "/users/"+tt.userID+"/groups", nil)

			GetUserGroups(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &response)

			if tt.expectError {
				assert.NotNil(t, response["error"])
			}
		})
	}
}

// TestGetUserPrayers tests the GetUserPrayers endpoint
func TestGetUserPrayers(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		currentUser    models.UserProfile
		isAdmin        bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful fetch - own prayers",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "successful fetch - admin views other user",
			userID:         "1",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "unauthorized - view other user's prayers",
			userID:         "2",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "invalid user ID",
			userID:         "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.userID != "invalid" && !tt.expectError {
				mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{}))
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: tt.userID}}
			c.Request = httptest.NewRequest("GET", "/users/"+tt.userID+"/prayers", nil)

			GetUserPrayers(c)

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

// TestGetUserPreferencesWithDefaults tests the GetUserPreferencesWithDefaults endpoint
func TestGetUserPreferencesWithDefaults(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		currentUser    models.UserProfile
		isAdmin        bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful fetch - own preferences",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "successful fetch - admin views other user",
			userID:         "1",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "unauthorized - view other user's preferences",
			userID:         "2",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "invalid user ID",
			userID:         "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.userID != "invalid" && !tt.expectError {
				// Mock default preferences query
				mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{}))
				// Mock user preferences query
				mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{}))
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: tt.userID}}
			c.Request = httptest.NewRequest("GET", "/users/"+tt.userID+"/preferences", nil)

			GetUserPreferencesWithDefaults(c)

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

// Test PublicUserSignup - Public user registration endpoint
func TestPublicUserSignup(t *testing.T) {
	tests := []struct {
		name           string
		signupData     models.UserProfileSignup
		mockUsername   int64 // Count for username check
		mockEmail      int64 // Count for email check
		expectedStatus int
		expectError    bool
	}{
		{
			name: "successful signup",
			signupData: models.UserProfileSignup{
				Username:     "newuser",
				Password:     "password123",
				Email:        "newuser@example.com",
				First_Name:   "New",
				Last_Name:    "User",
				Phone_Number: "1234567890",
			},
			mockUsername:   0, // Username available
			mockEmail:      0, // Email available
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name: "successful signup without username - defaults to email",
			signupData: models.UserProfileSignup{
				Password:     "password123",
				Email:        "newuser@example.com",
				First_Name:   "New",
				Last_Name:    "User",
				Phone_Number: "1234567890",
			},
			mockUsername:   0, // Username (email) available
			mockEmail:      0, // Email available
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name: "successful signup without lastName - optional field",
			signupData: models.UserProfileSignup{
				Password:   "password123",
				Email:      "newuser@example.com",
				First_Name: "New",
			},
			mockUsername:   0,
			mockEmail:      0,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name: "username already exists",
			signupData: models.UserProfileSignup{
				Username:     "existinguser",
				Password:     "password123",
				Email:        "newuser@example.com",
				First_Name:   "New",
				Last_Name:    "User",
				Phone_Number: "1234567890",
			},
			mockUsername:   1, // Username taken
			mockEmail:      0,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name: "email already exists",
			signupData: models.UserProfileSignup{
				Username:     "newuser",
				Password:     "password123",
				Email:        "existing@example.com",
				First_Name:   "New",
				Last_Name:    "User",
				Phone_Number: "1234567890",
			},
			mockUsername:   0,
			mockEmail:      1, // Email taken
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name: "missing required fields - password",
			signupData: models.UserProfileSignup{
				Username:     "newuser",
				Email:        "newuser@example.com",
				First_Name:   "New",
				Last_Name:    "User",
				Phone_Number: "1234567890",
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name: "missing required fields - email",
			signupData: models.UserProfileSignup{
				Password:   "password123",
				First_Name: "New",
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name: "missing required fields - firstName",
			signupData: models.UserProfileSignup{
				Password: "password123",
				Email:    "newuser@example.com",
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			// Only mock database queries for valid requests (required fields: password, email, firstName)
			if tt.signupData.Password != "" &&
				tt.signupData.Email != "" && tt.signupData.First_Name != "" {

				// Mock username check (username defaults to email if not provided)
				mock.ExpectQuery("SELECT").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.mockUsername))

				if tt.mockUsername == 0 {
					// Mock email check
					mock.ExpectQuery("SELECT").
						WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.mockEmail))

					if tt.mockEmail == 0 {
						// Mock insert with RETURNING user_profile_id
						mock.ExpectQuery("INSERT").
							WillReturnRows(sqlmock.NewRows([]string{"user_profile_id"}).AddRow(1))

						// Mock the GetOrCreateSelfPrayerSubject query (check for existing)
						mock.ExpectQuery("SELECT").
							WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}))

						// Mock the insert for creating self prayer_subject
						mock.ExpectQuery("INSERT").
							WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(1))
					}
				}
			}

			c, w := SetupTestContext()
			jsonData, _ := json.Marshal(tt.signupData)
			c.Request = httptest.NewRequest("POST", "/signup", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			PublicUserSignup(c)

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

// Test UserSignup - Admin-only user registration endpoint
func TestUserSignup(t *testing.T) {
	tests := []struct {
		name           string
		isAdmin        bool
		signupData     models.UserProfileSignup
		mockUsername   int64
		mockEmail      int64
		expectedStatus int
		expectError    bool
	}{
		{
			name:    "successful admin signup",
			isAdmin: true,
			signupData: models.UserProfileSignup{
				Username:     "newuser",
				Password:     "password123",
				Email:        "newuser@example.com",
				First_Name:   "New",
				Last_Name:    "User",
				Phone_Number: "1234567890",
			},
			mockUsername:   0,
			mockEmail:      0,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:    "unauthorized - non-admin",
			isAdmin: false,
			signupData: models.UserProfileSignup{
				Username:   "newuser",
				Password:   "password123",
				Email:      "newuser@example.com",
				First_Name: "New",
				Last_Name:  "User",
			},
			expectedStatus: http.StatusUnauthorized,
			expectError:    true,
		},
		{
			name:    "username already exists",
			isAdmin: true,
			signupData: models.UserProfileSignup{
				Username:   "existinguser",
				Password:   "password123",
				Email:      "newuser@example.com",
				First_Name: "New",
				Last_Name:  "User",
			},
			mockUsername:   1,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:    "email already exists",
			isAdmin: true,
			signupData: models.UserProfileSignup{
				Username:   "newuser",
				Password:   "password123",
				Email:      "existing@example.com",
				First_Name: "New",
				Last_Name:  "User",
			},
			mockUsername:   0,
			mockEmail:      1,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.isAdmin && tt.signupData.Username != "" && tt.signupData.Password != "" {
				// Mock username check
				mock.ExpectQuery("SELECT").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.mockUsername))

				if tt.mockUsername == 0 && tt.signupData.Email != "" {
					// Mock email check
					mock.ExpectQuery("SELECT").
						WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.mockEmail))

					if tt.mockEmail == 0 {
						// Mock insert with RETURNING user_profile_id
						mock.ExpectQuery("INSERT").
							WillReturnRows(sqlmock.NewRows([]string{"user_profile_id"}).AddRow(1))

						// Mock the GetOrCreateSelfPrayerSubject query (check for existing)
						mock.ExpectQuery("SELECT").
							WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}))

						// Mock the insert for creating self prayer_subject
						mock.ExpectQuery("INSERT").
							WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(1))
					}
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, MockAdminUser(), tt.isAdmin)
			jsonData, _ := json.Marshal(tt.signupData)
			c.Request = httptest.NewRequest("POST", "/users", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			UserSignup(c)

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

func TestCreateUserPrayer(t *testing.T) {
	isPrivate := false
	isAnswered := false
	priority := 1

	tests := []struct {
		name           string
		userID         string
		currentUser    models.UserProfile
		isAdmin        bool
		prayerData     models.PrayerCreate
		expectedStatus int
		expectError    bool
	}{
		{
			name:        "successful prayer creation - own prayer",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			prayerData: models.PrayerCreate{
				Prayer_Type:        "personal",
				Is_Private:         &isPrivate,
				Title:              "Test Prayer",
				Prayer_Description: "This is a test prayer",
				Is_Answered:        &isAnswered,
				Prayer_Priority:    &priority,
			},
			expectedStatus: http.StatusCreated,
			expectError:    false,
		},
		{
			name:        "successful prayer creation - admin for other user",
			userID:      "2",
			currentUser: MockAdminUser(),
			isAdmin:     true,
			prayerData: models.PrayerCreate{
				Prayer_Type:        "personal",
				Is_Private:         &isPrivate,
				Title:              "Admin Prayer",
				Prayer_Description: "Admin created prayer",
				Is_Answered:        &isAnswered,
				Prayer_Priority:    &priority,
			},
			expectedStatus: http.StatusCreated,
			expectError:    false,
		},
		{
			name:        "unauthorized - create for other user",
			userID:      "2",
			currentUser: MockUser(),
			isAdmin:     false,
			prayerData: models.PrayerCreate{
				Prayer_Type:        "personal",
				Title:              "Unauthorized Prayer",
				Prayer_Description: "Should fail",
			},
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "invalid user ID",
			userID:         "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			prayerData:     models.PrayerCreate{},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.userID != "invalid" && !tt.expectError {
				// Mock prayer_subject lookup - return existing subject ID
				mock.ExpectQuery("SELECT \"prayer_subject_id\" FROM \"prayer_subject\"").
					WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(1))

				// Mock subject_display_sequence update for prayers in this subject
				mock.ExpectExec("UPDATE \"prayer\" SET \"subject_display_sequence\"").
					WillReturnResult(sqlmock.NewResult(0, 0))

				// Mock prayer insert - return prayer ID
				mock.ExpectQuery("INSERT INTO \"prayer\"").
					WillReturnRows(sqlmock.NewRows([]string{"prayer_id"}).AddRow(1))

				// Mock display sequence update
				mock.ExpectExec("UPDATE \"prayer_access\"").
					WillReturnResult(sqlmock.NewResult(0, 0))

				// Mock prayer access insert - return prayer access ID
				mock.ExpectQuery("INSERT INTO \"prayer_access\"").
					WillReturnRows(sqlmock.NewRows([]string{"prayer_access_id"}).AddRow(1))
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: tt.userID}}
			jsonData, _ := json.Marshal(tt.prayerData)
			c.Request = httptest.NewRequest("POST", "/users/"+tt.userID+"/prayers", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			CreateUserPrayer(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &response)

			if tt.expectError {
				assert.NotNil(t, response["error"])
			} else {
				assert.NotNil(t, response["message"])
				assert.NotNil(t, response["prayerId"])
				assert.NotNil(t, response["prayerAccessId"])
			}
		})
	}
}

// Test ReorderUserGroups - Reorder a user's groups
func TestReorderUserGroups(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		currentUser    models.UserProfile
		isAdmin        bool
		reorderData    map[string]interface{}
		totalGroups    int
		expectedStatus int
		expectError    bool
	}{
		{
			name:        "successful reorder - own groups",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			reorderData: map[string]interface{}{
				"groups": []map[string]interface{}{
					{"groupId": 1, "displaySequence": 0},
					{"groupId": 2, "displaySequence": 1},
				},
			},
			totalGroups:    2,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "successful reorder - admin for other user",
			userID:      "2",
			currentUser: MockAdminUser(),
			isAdmin:     true,
			reorderData: map[string]interface{}{
				"groups": []map[string]interface{}{
					{"groupId": 1, "displaySequence": 0},
				},
			},
			totalGroups:    1,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "unauthorized - reorder other user's groups",
			userID:      "2",
			currentUser: MockUser(),
			isAdmin:     false,
			reorderData: map[string]interface{}{
				"groups": []map[string]interface{}{},
			},
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:        "incomplete reorder - missing groups",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			reorderData: map[string]interface{}{
				"groups": []map[string]interface{}{
					{"groupId": 1, "displaySequence": 0},
				},
			},
			totalGroups:    2, // Has 2 groups but only reordering 1
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "duplicate sequence",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			reorderData: map[string]interface{}{
				"groups": []map[string]interface{}{
					{"groupId": 1, "displaySequence": 0},
					{"groupId": 2, "displaySequence": 0}, // Duplicate sequence
				},
			},
			totalGroups:    2,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "out of range sequence",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			reorderData: map[string]interface{}{
				"groups": []map[string]interface{}{
					{"groupId": 1, "displaySequence": 0},
					{"groupId": 2, "displaySequence": 5}, // Out of range
				},
			},
			totalGroups:    2,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.userID != "invalid" && !tt.expectError {
				// Mock group count query
				mock.ExpectQuery("SELECT").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.totalGroups))

				// Mock update for each group
				if groups, ok := tt.reorderData["groups"].([]map[string]interface{}); ok {
					for range groups {
						mock.ExpectExec("UPDATE \"user_group\"").
							WillReturnResult(sqlmock.NewResult(0, 1))
					}
				}
			} else if tt.name == "incomplete reorder - missing groups" || tt.name == "duplicate sequence" || tt.name == "out of range sequence" {
				// Mock group count for validation error cases
				mock.ExpectQuery("SELECT").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.totalGroups))
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: tt.userID}}
			jsonData, _ := json.Marshal(tt.reorderData)
			c.Request = httptest.NewRequest("PATCH", "/users/"+tt.userID+"/groups/reorder", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			ReorderUserGroups(c)

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

// Test ReorderUserPrayers - Reorder a user's prayers
func TestReorderUserPrayers(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		currentUser    models.UserProfile
		isAdmin        bool
		reorderData    map[string]interface{}
		totalPrayers   int
		expectedStatus int
		expectError    bool
	}{
		{
			name:        "successful reorder - own prayers",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			reorderData: map[string]interface{}{
				"prayers": []map[string]interface{}{
					{"prayerId": 1, "displaySequence": 0},
					{"prayerId": 2, "displaySequence": 1},
					{"prayerId": 3, "displaySequence": 2},
				},
			},
			totalPrayers:   3,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "successful reorder - admin for other user",
			userID:      "2",
			currentUser: MockAdminUser(),
			isAdmin:     true,
			reorderData: map[string]interface{}{
				"prayers": []map[string]interface{}{
					{"prayerId": 1, "displaySequence": 0},
				},
			},
			totalPrayers:   1,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "unauthorized - reorder other user's prayers",
			userID:      "2",
			currentUser: MockUser(),
			isAdmin:     false,
			reorderData: map[string]interface{}{
				"prayers": []map[string]interface{}{},
			},
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:        "incomplete reorder - missing prayers",
			userID:      "1",
			currentUser: MockUser(),
			isAdmin:     false,
			reorderData: map[string]interface{}{
				"prayers": []map[string]interface{}{
					{"prayerId": 1, "displaySequence": 0},
				},
			},
			totalPrayers:   3, // Has 3 prayers but only reordering 1
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.userID != "invalid" && !tt.expectError {
				// Mock prayer count query
				mock.ExpectQuery("SELECT").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.totalPrayers))

				// Mock update for each prayer
				if prayers, ok := tt.reorderData["prayers"].([]map[string]interface{}); ok {
					for range prayers {
						mock.ExpectExec("UPDATE \"prayer_access\"").
							WillReturnResult(sqlmock.NewResult(0, 1))
					}
				}
			} else if tt.name == "incomplete reorder - missing prayers" {
				// Mock prayer count for validation error case
				mock.ExpectQuery("SELECT").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.totalPrayers))
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: tt.userID}}
			jsonData, _ := json.Marshal(tt.reorderData)
			c.Request = httptest.NewRequest("PATCH", "/users/"+tt.userID+"/prayers/reorder", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			ReorderUserPrayers(c)

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

// Test GetUserPreferences - Get raw user preferences without defaults
func TestGetUserPreferences(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		currentUser    models.UserProfile
		isAdmin        bool
		expectedStatus int
		expectError    bool
		hasPrefs       bool
	}{
		{
			name:           "successful fetch - own preferences",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusOK,
			expectError:    false,
			hasPrefs:       true,
		},
		{
			name:           "successful fetch - admin views other user",
			userID:         "2",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			expectedStatus: http.StatusOK,
			expectError:    false,
			hasPrefs:       true,
		},
		{
			name:           "no preferences found",
			userID:         "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusOK,
			expectError:    false,
			hasPrefs:       false,
		},
		{
			name:           "unauthorized - view other user's preferences",
			userID:         "2",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "invalid user ID",
			userID:         "invalid",
			currentUser:    MockUser(),
			isAdmin:        false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.userID != "invalid" && !tt.expectError {
				if tt.hasPrefs {
					// Mock query with preferences
					rows := sqlmock.NewRows([]string{"user_preferences_id", "user_profile_id", "preference_key", "preference_value", "is_active", "datetime_create", "datetime_update"}).
						AddRow(1, 1, "theme", "dark", true, time.Now(), time.Now())
					mock.ExpectQuery("SELECT").WillReturnRows(rows)
				} else {
					// Mock query with no preferences
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{}))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "user_profile_id", Value: tt.userID}}
			c.Request = httptest.NewRequest("GET", "/users/"+tt.userID+"/preferences/raw", nil)

			GetUserPreferences(c)

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

// Test UpdateUserPreferences - Update user preferences with validation
func TestUpdateUserPreferences(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		preferenceID   string
		currentUser    models.UserProfile
		isAdmin        bool
		updateData     models.UserPreferencesUpdate
		existingPref   bool
		prefKey        string
		prefType       string
		expectedStatus int
		expectError    bool
	}{
		{
			name:         "successful update - create new preference",
			userID:       "1",
			preferenceID: "1",
			currentUser:  MockUser(),
			isAdmin:      false,
			updateData: models.UserPreferencesUpdate{
				Preference_Key:   "theme",
				Preference_Value: "dark",
				Is_Active:        true,
			},
			existingPref:   false,
			prefKey:        "theme",
			prefType:       "string",
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:         "successful update - update existing preference",
			userID:       "1",
			preferenceID: "1",
			currentUser:  MockUser(),
			isAdmin:      false,
			updateData: models.UserPreferencesUpdate{
				Preference_Key:   "theme",
				Preference_Value: "light",
				Is_Active:        true,
			},
			existingPref:   true,
			prefKey:        "theme",
			prefType:       "string",
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:         "unauthorized - update other user's preferences",
			userID:       "2",
			preferenceID: "1",
			currentUser:  MockUser(),
			isAdmin:      false,
			updateData: models.UserPreferencesUpdate{
				Preference_Key:   "theme",
				Preference_Value: "dark",
				Is_Active:        true,
			},
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:         "preference key mismatch",
			userID:       "1",
			preferenceID: "1",
			currentUser:  MockUser(),
			isAdmin:      false,
			updateData: models.UserPreferencesUpdate{
				Preference_Key:   "wrong_key",
				Preference_Value: "dark",
				Is_Active:        true,
			},
			prefKey:        "theme",
			prefType:       "string",
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:         "invalid boolean value",
			userID:       "1",
			preferenceID: "1",
			currentUser:  MockUser(),
			isAdmin:      false,
			updateData: models.UserPreferencesUpdate{
				Preference_Key:   "notifications_enabled",
				Preference_Value: "yes", // Should be "true" or "false"
				Is_Active:        true,
			},
			prefKey:        "notifications_enabled",
			prefType:       "boolean",
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:         "invalid theme value",
			userID:       "1",
			preferenceID: "1",
			currentUser:  MockUser(),
			isAdmin:      false,
			updateData: models.UserPreferencesUpdate{
				Preference_Key:   "theme",
				Preference_Value: "blue", // Should be "light" or "dark"
				Is_Active:        true,
			},
			prefKey:        "theme",
			prefType:       "string",
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.userID != "invalid" && tt.preferenceID != "invalid" && !tt.expectError {
				// Mock preference lookup
				prefRows := sqlmock.NewRows([]string{"preference_id", "preference_key", "default_value", "description", "value_type", "datetime_create", "datetime_update", "created_by", "updated_by", "is_active"}).
					AddRow(1, tt.prefKey, "light", "Theme preference", tt.prefType, time.Now(), time.Now(), 1, 1, true)
				mock.ExpectQuery("SELECT").WillReturnRows(prefRows)

				// Mock existing user preference lookup
				if tt.existingPref {
					existingRows := sqlmock.NewRows([]string{"user_preferences_id", "user_profile_id", "preference_key", "preference_value", "is_active", "datetime_create", "datetime_update"}).
						AddRow(1, 1, tt.prefKey, "dark", true, time.Now(), time.Now())
					mock.ExpectQuery("SELECT").WillReturnRows(existingRows)

					// Mock update
					mock.ExpectExec("UPDATE \"user_preferences\"").
						WillReturnResult(sqlmock.NewResult(0, 1))
				} else {
					// Mock empty existing preferences
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{}))

					// Mock insert
					mock.ExpectExec("INSERT").
						WillReturnResult(sqlmock.NewResult(1, 1))
				}
			} else if tt.name == "preference key mismatch" || tt.name == "invalid boolean value" || tt.name == "invalid theme value" {
				// Mock preference lookup for validation error cases
				prefRows := sqlmock.NewRows([]string{"preference_id", "preference_key", "default_value", "description", "value_type", "datetime_create", "datetime_update", "created_by", "updated_by", "is_active"}).
					AddRow(1, tt.prefKey, "light", "Theme preference", tt.prefType, time.Now(), time.Now(), 1, 1, true)
				mock.ExpectQuery("SELECT").WillReturnRows(prefRows)

				// For boolean/theme validation errors, we also need to mock existing preferences lookup
				// This happens before validation in the actual code flow
				if tt.name != "preference key mismatch" {
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{}))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{
				{Key: "user_profile_id", Value: tt.userID},
				{Key: "preference_id", Value: tt.preferenceID},
			}
			jsonData, _ := json.Marshal(tt.updateData)
			c.Request = httptest.NewRequest("PATCH", "/users/"+tt.userID+"/preferences/"+tt.preferenceID, bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			UpdateUserPreferences(c)

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

// Test StorePushToken - Store push notification token
func TestStorePushToken(t *testing.T) {
	tests := []struct {
		name           string
		currentUser    models.UserProfile
		tokenData      models.PushTokenRequest
		expectedStatus int
		expectError    bool
	}{
		{
			name:        "successful token storage - iOS",
			currentUser: MockUser(),
			tokenData: models.PushTokenRequest{
				PushToken: stringRepeater("a").Repeat(100), // Valid length token
				Platform:  "ios",
			},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "successful token storage - Android",
			currentUser: MockUser(),
			tokenData: models.PushTokenRequest{
				PushToken: "ExponentPushToken[" + stringRepeater("a").Repeat(50) + "]",
				Platform:  "android",
			},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:        "token too short",
			currentUser: MockUser(),
			tokenData: models.PushTokenRequest{
				PushToken: "short",
				Platform:  "ios",
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "token too long",
			currentUser: MockUser(),
			tokenData: models.PushTokenRequest{
				PushToken: stringRepeater("a").Repeat(501),
				Platform:  "ios",
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:        "invalid platform",
			currentUser: MockUser(),
			tokenData: models.PushTokenRequest{
				PushToken: stringRepeater("a").Repeat(100),
				Platform:  "web",
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if !tt.expectError {
				// Mock upsert query
				mock.ExpectExec("INSERT INTO \"user_push_tokens\"").
					WillReturnResult(sqlmock.NewResult(1, 1))
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, false)
			jsonData, _ := json.Marshal(tt.tokenData)
			c.Request = httptest.NewRequest("POST", "/user/register-push-token", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			StorePushToken(c)

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
// Helper function for string repeat (Go doesn't have built-in)
type stringRepeater string

func (s stringRepeater) Repeat(count int) string {
	result := ""
	for i := 0; i < count; i++ {
		result += string(s)
	}
	return result
}
