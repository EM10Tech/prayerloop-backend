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

// Test CreateGroupInviteCode - Generate invite codes for groups
func TestCreateGroupInviteCode(t *testing.T) {
	tests := []struct {
		name           string
		groupID        string
		currentUser    models.UserProfile
		isAdmin        bool
		userInGroup    bool
		groupExists    bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful creation - user in group",
			groupID:        "1",
			currentUser:    MockUser(),
			isAdmin:        false,
			userInGroup:    true,
			groupExists:    true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "successful creation - admin not in group",
			groupID:        "1",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			userInGroup:    false,
			groupExists:    true,
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
					// Mock isGroupExists check (returns COUNT)
					groupRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
					mock.ExpectQuery("SELECT").WillReturnRows(groupRows)

					// Mock isUserInGroup check (returns COUNT) - always called even for admins
					if tt.userInGroup {
						userGroupRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
						mock.ExpectQuery("SELECT").WillReturnRows(userGroupRows)
					} else {
						mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					}

					if tt.userInGroup || tt.isAdmin {
						// Mock invite code insert
						mock.ExpectQuery("INSERT INTO \"group_invite\"").
							WillReturnRows(sqlmock.NewRows([]string{"invite_code"}).AddRow("0001-A4F2"))
					}
				} else {
					// Mock group doesn't exist (returns COUNT of 0)
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "group_profile_id", Value: tt.groupID}}
			c.Request = httptest.NewRequest("POST", "/groups/"+tt.groupID+"/invite", nil)

			CreateGroupInviteCode(c)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &response)

			if tt.expectError {
				assert.NotNil(t, response["error"])
			} else {
				assert.NotNil(t, response["inviteCode"])
				assert.NotNil(t, response["expiresAt"])
			}
		})
	}
}

// Test JoinGroup - Join a group using an invite code
func TestJoinGroup(t *testing.T) {
	tests := []struct {
		name           string
		groupID        string
		currentUser    models.UserProfile
		isAdmin        bool
		inviteCode     string
		inviteValid    bool
		inviteExpired  bool
		inviteInactive bool
		wrongGroup     bool
		userInGroup    bool
		groupExists    bool
		invalidJSON    bool
		circleCount    int
		isPremium      bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful join with valid invite",
			groupID:        "1",
			currentUser:    MockUser(),
			inviteCode:     "0001-A4F2",
			inviteValid:    true,
			inviteExpired:  false,
			inviteInactive: false,
			wrongGroup:     false,
			userInGroup:    false,
			groupExists:    true,
			invalidJSON:    false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "invalid invite code",
			groupID:        "1",
			currentUser:    MockUser(),
			inviteCode:     "INVALID",
			inviteValid:    false,
			inviteExpired:  false,
			inviteInactive: false,
			wrongGroup:     false,
			userInGroup:    false,
			groupExists:    true,
			invalidJSON:    false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "expired invite code",
			groupID:        "1",
			currentUser:    MockUser(),
			inviteCode:     "0001-A4F2",
			inviteValid:    true,
			inviteExpired:  true,
			inviteInactive: false,
			wrongGroup:     false,
			userInGroup:    false,
			groupExists:    true,
			invalidJSON:    false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "inactive invite code",
			groupID:        "1",
			currentUser:    MockUser(),
			inviteCode:     "0001-A4F2",
			inviteValid:    true,
			inviteExpired:  false,
			inviteInactive: true,
			wrongGroup:     false,
			userInGroup:    false,
			groupExists:    true,
			invalidJSON:    false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "invite for different group",
			groupID:        "1",
			currentUser:    MockUser(),
			inviteCode:     "0002-B5E3",
			inviteValid:    true,
			inviteExpired:  false,
			inviteInactive: false,
			wrongGroup:     true,
			userInGroup:    false,
			groupExists:    true,
			invalidJSON:    false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "user already in group",
			groupID:        "1",
			currentUser:    MockUser(),
			inviteCode:     "0001-A4F2",
			inviteValid:    true,
			inviteExpired:  false,
			inviteInactive: false,
			wrongGroup:     false,
			userInGroup:    true,
			groupExists:    true,
			invalidJSON:    false,
			expectedStatus: http.StatusConflict,
			expectError:    true,
		},
		{
			name:           "group doesn't exist",
			groupID:        "999",
			currentUser:    MockUser(),
			inviteCode:     "0999-C6D4",
			inviteValid:    false,
			inviteExpired:  false,
			inviteInactive: false,
			wrongGroup:     false,
			userInGroup:    false,
			groupExists:    false,
			invalidJSON:    false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:           "invalid group ID",
			groupID:        "invalid",
			currentUser:    MockUser(),
			inviteCode:     "0001-A4F2",
			inviteValid:    false,
			inviteExpired:  false,
			inviteInactive: false,
			wrongGroup:     false,
			userInGroup:    false,
			groupExists:    false,
			invalidJSON:    false,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:           "invalid JSON",
			groupID:        "1",
			currentUser:    MockUser(),
			inviteCode:     "",
			inviteValid:    false,
			inviteExpired:  false,
			inviteInactive: false,
			wrongGroup:     false,
			userInGroup:    false,
			groupExists:    true,
			invalidJSON:    true,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:           "free user at circle limit is rejected",
			groupID:        "1",
			currentUser:    MockUser(),
			inviteCode:     "0001-A4F2",
			inviteValid:    true,
			inviteExpired:  false,
			inviteInactive: false,
			wrongGroup:     false,
			userInGroup:    false,
			groupExists:    true,
			invalidJSON:    false,
			circleCount:    3,
			isPremium:      false,
			expectedStatus: http.StatusForbidden,
			expectError:    true,
		},
		{
			name:           "premium user bypasses circle limit",
			groupID:        "1",
			currentUser:    MockUser(),
			inviteCode:     "0001-A4F2",
			inviteValid:    true,
			inviteExpired:  false,
			inviteInactive: false,
			wrongGroup:     false,
			userInGroup:    false,
			groupExists:    true,
			invalidJSON:    false,
			circleCount:    3,
			isPremium:      true,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "admin bypasses circle limit",
			groupID:        "1",
			currentUser:    MockAdminUser(),
			isAdmin:        true,
			inviteCode:     "0001-A4F2",
			inviteValid:    true,
			inviteExpired:  false,
			inviteInactive: false,
			wrongGroup:     false,
			userInGroup:    false,
			groupExists:    true,
			invalidJSON:    false,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, cleanup := SetupTestDB(t)
			defer cleanup()

			if tt.groupID != "invalid" {
				if tt.groupExists {
					// Mock isGroupExists check (returns COUNT)
					groupRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
					mock.ExpectQuery("SELECT").WillReturnRows(groupRows)

					// Don't mock invite lookup for invalid JSON - it fails at binding
					if !tt.invalidJSON && tt.inviteValid {
						// Mock invite code lookup
						now := time.Now()
						var expiresAt time.Time
						if tt.inviteExpired {
							expiresAt = now.AddDate(0, 0, -1) // Expired yesterday
						} else {
							expiresAt = now.AddDate(0, 0, 7) // Expires in 7 days
						}

						groupIDForInvite := 1
						if tt.wrongGroup {
							groupIDForInvite = 2 // Different group
						}

						inviteRows := sqlmock.NewRows([]string{
							"group_invite_id", "group_profile_id", "invite_code",
							"datetime_create", "datetime_update", "created_by", "updated_by",
							"datetime_expires", "is_active",
						}).AddRow(1, groupIDForInvite, tt.inviteCode, now, now, 1, 1, expiresAt, !tt.inviteInactive)
						mock.ExpectQuery("SELECT").WillReturnRows(inviteRows)

						// If invite is valid, not expired, not inactive, and correct group
						if !tt.inviteExpired && !tt.inviteInactive && !tt.wrongGroup {
							// Mock isUserInGroup check (returns COUNT)
							if tt.userInGroup {
								userGroupRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
								mock.ExpectQuery("SELECT").WillReturnRows(userGroupRows)
							} else {
								mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

								// Mock circle limit check: active circle count, then premium lookup
								atCircleLimit := false
								if !tt.isAdmin {
									mock.ExpectQuery("SELECT COUNT").
										WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.circleCount))
									premiumRows := sqlmock.NewRows([]string{"is_premium"})
									if tt.isPremium {
										premiumRows.AddRow(true)
									}
									mock.ExpectQuery("SELECT \"is_premium\" FROM \"user_subscription\"").
										WillReturnRows(premiumRows)
									atCircleLimit = tt.circleCount >= FreeCircleLimit && !tt.isPremium
								}

								if !atCircleLimit {
									// Mock display sequence update
									mock.ExpectExec("UPDATE \"user_group\"").
										WillReturnResult(sqlmock.NewResult(0, 0))

									// Mock user_group insert
									mock.ExpectExec("INSERT INTO \"user_group\"").
										WillReturnResult(sqlmock.NewResult(1, 1))

									// Mock invite deactivation
									mock.ExpectExec("UPDATE \"group_invite\"").
										WillReturnResult(sqlmock.NewResult(0, 1))

									// Mock GetGroupNameByID for push notification (runs in goroutine)
									mock.ExpectQuery("SELECT \"group_name\" FROM \"group_profile\"").
										WillReturnRows(sqlmock.NewRows([]string{"group_name"}).AddRow("Test Group"))

									// Mock GetOtherGroupMemberIDs for push notification (runs in goroutine)
									mock.ExpectQuery("SELECT \"user_profile_id\" FROM \"user_group\"").
										WillReturnRows(sqlmock.NewRows([]string{"user_profile_id"}).AddRow(2).AddRow(3))
								}
							}
						}
					} else if !tt.invalidJSON {
						// Mock invite not found (only if not invalid JSON)
						mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
							"group_invite_id", "group_profile_id", "invite_code",
							"datetime_create", "datetime_update", "created_by", "updated_by",
							"datetime_expires", "is_active",
						}))
					}
				} else {
					// Mock group doesn't exist (returns COUNT of 0)
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				}
			}

			c, w := SetupTestContext()
			SetAuthenticatedUser(c, tt.currentUser, tt.isAdmin)
			c.Params = []gin.Param{{Key: "group_profile_id", Value: tt.groupID}}

			var jsonData []byte
			if tt.invalidJSON {
				jsonData = []byte("{invalid json}")
			} else {
				joinRequest := models.JoinRequest{Invite_Code: tt.inviteCode}
				jsonData, _ = json.Marshal(joinRequest)
			}
			c.Request = httptest.NewRequest("POST", "/groups/"+tt.groupID+"/join", bytes.NewBuffer(jsonData))
			c.Request.Header.Set("Content-Type", "application/json")

			JoinGroup(c)

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
