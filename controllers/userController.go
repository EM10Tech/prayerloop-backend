package controllers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"time"

	"github.com/gin-gonic/gin"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"
	"github.com/PrayerLoop/services"
	"github.com/doug-martin/goqu/v9"
	"golang.org/x/crypto/bcrypt"
)

func PublicUserSignup(c *gin.Context) {
	var user models.UserProfileSignup

	if err := c.ShouldBindJSON(&user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate required fields (username is optional - defaults to email)
	var missingFields []string
	if user.Password == "" {
		missingFields = append(missingFields, "password")
	}
	if user.Email == "" {
		missingFields = append(missingFields, "email")
	}
	if user.First_Name == "" {
		missingFields = append(missingFields, "firstName")
	}
	// lastName is now optional

	if len(missingFields) > 0 {
		var errorMsg string
		if len(missingFields) == 1 {
			errorMsg = fmt.Sprintf("The following field is required: %s", missingFields[0])
		} else {
			errorMsg = fmt.Sprintf("The following fields are required: %s", strings.Join(missingFields, ", "))
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": errorMsg})
		return
	}

	// If username is not provided, default to email
	if user.Username == "" {
		user.Username = user.Email
	}

	// Check if username already exists
	userCount, err := initializers.DB.From("user_profile").Select("username").Where(goqu.C("username").Eq(user.Username)).Count()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if userCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username already exists."})
		return
	}

	// Check if email already exists
	emailCount, err := initializers.DB.From("user_profile").Select("email").Where(goqu.C("email").Eq(user.Email)).Count()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if emailCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email already exists."})
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Handle phone number - convert to pointer for nullable field
	var phoneNumber *string
	if user.Phone_Number != "" {
		phoneNumber = &user.Phone_Number
	}

	passwordHashStr := string(passwordHash)

	newUser := models.UserProfile{
		Username:     user.Username,
		Password:     &passwordHashStr,
		Email:        user.Email,
		First_Name:   user.First_Name,
		Last_Name:    user.Last_Name,
		Phone_Number: phoneNumber,
		Created_By:   1,
		Updated_By:   1,
	}

	insert := initializers.DB.Insert("user_profile").Rows(newUser).Returning("user_profile_id")
	var insertedUserID int
	_, err = insert.Executor().ScanVal(&insertedUserID)
	if err != nil {
		log.Default().Println(insert)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Create self prayer_subject for the new user
	createdUser := models.UserProfile{
		User_Profile_ID: insertedUserID,
		First_Name:      user.First_Name,
		Last_Name:       user.Last_Name,
		Username:        user.Username,
	}
	_, err = GetOrCreateSelfPrayerSubject(createdUser)
	if err != nil {
		log.Printf("Failed to create self prayer_subject for user %d: %v", insertedUserID, err)
		// Don't fail the signup if prayer_subject creation fails - just log it
	}

	// Send welcome email to new user
	emailService := services.GetEmailService()
	if emailService != nil {
		err := emailService.SendWelcomeEmail(user.Email, user.First_Name)
		if err != nil {
			log.Printf("Failed to send welcome email to %s: %v", user.Email, err)
			// Don't fail the signup if email fails - just log it
		}
	}

	c.JSON(200, gin.H{
		"message": "User created successfully.",
		"user":    user,
	})
}

func UserSignup(c *gin.Context) {
	admin := c.MustGet("admin")

	if admin != true {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "must be logged in as an admin to create a user."})
		return
	}
	var user models.UserProfileSignup

	if err := c.ShouldBindJSON(&user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if username already exists
	userCount, err := initializers.DB.From("user_profile").Select("username").Where(goqu.C("username").Eq(user.Username)).Count()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if userCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username already exists."})
		return
	}

	// Check if email already exists (if provided)
	if user.Email != "" {
		emailCount, err := initializers.DB.From("user_profile").Select("email").Where(goqu.C("email").Eq(user.Email)).Count()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if emailCount > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "email already exists."})
			return
		}
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Handle phone number - convert to pointer for nullable field
	var phoneNumber *string
	if user.Phone_Number != "" {
		phoneNumber = &user.Phone_Number
	}

	passwordHashStr := string(passwordHash)

	newUser := models.UserProfile{
		Username:     user.Username,
		Password:     &passwordHashStr,
		Email:        user.Email,
		First_Name:   user.First_Name,
		Last_Name:    user.Last_Name,
		Phone_Number: phoneNumber,
		Created_By:   1,
		Updated_By:   1,
	}

	insert := initializers.DB.Insert("user_profile").Rows(newUser).Returning("user_profile_id")
	var insertedUserID int
	_, err = insert.Executor().ScanVal(&insertedUserID)
	if err != nil {
		log.Default().Println(insert)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Create self prayer_subject for the new user
	createdUser := models.UserProfile{
		User_Profile_ID: insertedUserID,
		First_Name:      user.First_Name,
		Last_Name:       user.Last_Name,
		Username:        user.Username,
	}
	_, err = GetOrCreateSelfPrayerSubject(createdUser)
	if err != nil {
		log.Printf("Failed to create self prayer_subject for user %d: %v", insertedUserID, err)
		// Don't fail the signup if prayer_subject creation fails - just log it
	}

	// Send welcome email to new user
	emailService := services.GetEmailService()
	if emailService != nil {
		err := emailService.SendWelcomeEmail(user.Email, user.First_Name)
		if err != nil {
			log.Printf("Failed to send welcome email to %s: %v", user.Email, err)
			// Don't fail the signup if email fails - just log it
		}
	}

	c.JSON(200, gin.H{
		"message": "User created successfully.",
		"user":    user,
	})
}

func CheckUsernameAvailability(c *gin.Context) {
	username := c.Query("username")

	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username parameter is required"})
		return
	}

	userCount, err := initializers.DB.From("user_profile").Select("username").Where(goqu.C("username").Eq(username)).Count()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"username":  username,
		"available": userCount == 0,
	})
}

func UserLogin(c *gin.Context) {
	var user models.Login

	if err := c.ShouldBindJSON(&user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate that either email or username is provided
	if user.Email == "" && user.Username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Email or username is required"})
		return
	}

	var dbUser models.UserProfile
	var found bool
	var err error

	// Email takes precedence if provided
	if user.Email != "" {
		found, err = initializers.DB.From("user_profile").Select("*").Where(goqu.C("email").Eq(user.Email)).ScanStruct(&dbUser)
	} else {
		found, err = initializers.DB.From("user_profile").Select("*").Where(goqu.C("username").Eq(user.Username)).ScanStruct(&dbUser)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	// OAuth-only accounts have a NULL password: password login is unavailable,
	// and a NULL hash must never authenticate.
	if dbUser.Password == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "This account uses social login. Sign in with your provider or set a password via 'Forgot Password'."})
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(*dbUser.Password), []byte(user.Password))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	token, err := generateAccessToken(dbUser)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to generate token", "details": err.Error()})
	}

	refreshToken, err := issueRefreshToken(dbUser.User_Profile_ID, "")
	if err != nil {
		log.Printf("Failed to issue refresh token for user %d: %v", dbUser.User_Profile_ID, err)
	}

	c.JSON(200, gin.H{
		"message":      "User logged in successfully.",
		"token":        token,
		"refreshToken": refreshToken,
		"user":         dbUser,
	})
}

func GetUserProfile(c *gin.Context) {

	user, _ := c.Get("currentUser")

	c.JSON(200, gin.H{
		"user":  user,
		"admin": c.MustGet("admin"),
	})
}

func GetUserGroups(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if userID != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to view this user's groups"})
		return
	}

	// prayer_subject_id is resolved per-viewer (see GetGroup for rationale): the
	// viewer's psgp anchor takes precedence over the static creator-only column.
	query := initializers.DB.From("user_group").
		Select(
			"group_profile.group_profile_id",
			"group_profile.group_name",
			"group_profile.group_description",
			"group_profile.is_active",
			"group_profile.datetime_create",
			"group_profile.datetime_update",
			"group_profile.created_by",
			"group_profile.updated_by",
			"group_profile.deleted",
			goqu.L(
				"COALESCE(prayer_subject_group_profile.prayer_subject_id, group_profile.prayer_subject_id)",
			).As("prayer_subject_id"),
			"user_group.group_display_sequence",
		).
		InnerJoin(
			goqu.T("group_profile"),
			goqu.On(goqu.Ex{"user_group.group_profile_id": goqu.I("group_profile.group_profile_id")}),
		).
		LeftJoin(
			goqu.T("prayer_subject_group_profile"),
			goqu.On(goqu.And(
				goqu.Ex{"prayer_subject_group_profile.group_profile_id": goqu.I("group_profile.group_profile_id")},
				goqu.Ex{"prayer_subject_group_profile.created_by": userID},
			)),
		).
		Where(
			goqu.And(
				goqu.C("user_profile_id").Table("user_group").Eq(userID),
				goqu.C("is_active").Table("user_group").IsTrue(),
				goqu.C("is_active").Table("group_profile").IsTrue(),
			),
		).
		Order(goqu.I("user_group.group_display_sequence").Asc())

	sql, args, err := query.ToSQL()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to construct query", "details": err.Error()})
		return
	}

	var groups []models.GroupProfile
	err = initializers.DB.ScanStructs(&groups, sql, args...)
	if err != nil {
		log.Printf("ERROR scanning groups for user %d: %v", userID, err)
		log.Printf("SQL was: %s", sql)
		log.Printf("Args were: %v", args)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user groups", "details": err.Error()})
		return
	}

	// Always return an array, even if empty (for consistent client-side handling)
	if groups == nil {
		groups = []models.GroupProfile{}
	}

	c.JSON(http.StatusOK, groups)
}

func ReorderUserGroups(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if currentUser.User_Profile_ID != userID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to reorder this user's groups"})
		return
	}

	var reorderData struct {
		Groups []struct {
			GroupID         int `json:"groupId"`
			DisplaySequence int `json:"displaySequence"`
		} `json:"groups"`
	}

	if err := c.BindJSON(&reorderData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get total count of groups for this user
	var totalGroups int
	_, err = initializers.DB.From("user_group").
		Select(goqu.COUNT("user_group_id")).
		Where(
			goqu.C("user_profile_id").Eq(userID),
			goqu.C("is_active").IsTrue(),
		).
		ScanVal(&totalGroups)
	if err != nil {
		log.Println("Failed to count user groups:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count groups", "details": err.Error()})
		return
	}

	// Validate that all groups are included in the request
	if len(reorderData.Groups) != totalGroups {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Invalid reorder request: expected %d groups, got %d. All groups must be included in reorder request.", totalGroups, len(reorderData.Groups)),
		})
		return
	}

	// Validate that all displaySequence values are unique and contiguous
	sequenceMap := make(map[int]bool)
	for _, group := range reorderData.Groups {
		if group.DisplaySequence < 0 || group.DisplaySequence >= totalGroups {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Invalid displaySequence %d: must be between 0 and %d", group.DisplaySequence, totalGroups-1),
			})
			return
		}
		if sequenceMap[group.DisplaySequence] {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Duplicate displaySequence %d: each group must have a unique sequence", group.DisplaySequence),
			})
			return
		}
		sequenceMap[group.DisplaySequence] = true
	}

	// Update each group's display_sequence in user_group table
	for _, group := range reorderData.Groups {
		updateQuery := initializers.DB.Update("user_group").
			Set(goqu.Record{"group_display_sequence": group.DisplaySequence}).
			Where(
				goqu.C("group_profile_id").Eq(group.GroupID),
				goqu.C("user_profile_id").Eq(userID),
			)

		_, err := updateQuery.Executor().Exec()
		if err != nil {
			log.Println("Failed to update group display sequence:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder groups", "details": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Groups reordered successfully"})
}

func GetUserPrayers(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if userID != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to view this user's prayers"})
		return
	}

	var userPrayers []models.UserPrayer

	dbErr := initializers.DB.From("prayer_access").
		Select(
			goqu.L("?", currentUser.User_Profile_ID).As("user_profile_id"),
			goqu.I("prayer.prayer_id"),
			goqu.I("prayer_access.prayer_access_id"),
			goqu.I("prayer_access.display_sequence"),
			goqu.I("prayer.prayer_type"),
			goqu.I("prayer.is_private"),
			goqu.I("prayer.title"),
			goqu.I("prayer.prayer_description"),
			goqu.I("prayer.is_answered"),
			goqu.I("prayer.prayer_priority"),
			goqu.I("prayer.datetime_answered"),
			goqu.I("prayer.created_by"),
			goqu.I("prayer.datetime_create"),
			goqu.I("prayer.updated_by"),
			goqu.I("prayer.datetime_update"),
			goqu.I("prayer.deleted"),
			goqu.I("prayer_category.prayer_category_id"),
			goqu.I("prayer_category.category_name"),
			goqu.I("prayer_category.category_color"),
			goqu.I("prayer_category.display_sequence").As("category_display_sequence"),
		).
		Join(
			goqu.T("prayer"),
			goqu.On(goqu.Ex{"prayer_access.prayer_id": goqu.I("prayer.prayer_id")}),
		).
		LeftJoin(
			goqu.T("prayer_category_item"),
			goqu.On(goqu.Ex{"prayer_access.prayer_access_id": goqu.I("prayer_category_item.prayer_access_id")}),
		).
		LeftJoin(
			goqu.T("prayer_category"),
			goqu.On(goqu.Ex{"prayer_category_item.prayer_category_id": goqu.I("prayer_category.prayer_category_id")}),
		).
		Where(
			goqu.And(
				goqu.Ex{"prayer_access.access_type": "user"},
				goqu.Ex{"prayer_access.access_type_id": currentUser.User_Profile_ID},
				goqu.Ex{"prayer.deleted": false},
			),
		).
		Order(goqu.I("prayer_access.display_sequence").Asc()).
		ScanStructsContext(c, &userPrayers)

	if dbErr != nil {
		c.JSON(500, gin.H{"error": dbErr.Error()})
		return
	}

	if len(userPrayers) == 0 {
		c.JSON(200, gin.H{"message": "No prayer records found."})
		return
	}

	c.JSON(200, gin.H{
		"message": "Prayer records retrieved successfully.",
		"prayers": userPrayers,
	})
}

func CreateUserPrayer(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if currentUser.User_Profile_ID != userID &&
		!isAdmin {
		c.JSON(http.StatusForbidden,
			gin.H{"error": fmt.Sprintf("You don't have permission to create a prayer on behalf of user %d",
				currentUser.User_Profile_ID)})
		return
	}

	var newPrayer models.PrayerCreate
	if err := c.BindJSON(&newPrayer); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Determine prayer_subject_id - use provided value or fall back to self subject
	var prayerSubjectID int
	if newPrayer.Prayer_Subject_ID != nil {
		// Verify the prayer subject exists and belongs to this user
		var subjectExists bool
		subjectExists, err = initializers.DB.From("prayer_subject").
			Select(goqu.L("1")).
			Where(
				goqu.C("prayer_subject_id").Eq(*newPrayer.Prayer_Subject_ID),
				goqu.C("created_by").Eq(userID),
			).
			ScanVal(new(int))

		if err != nil {
			log.Println("Failed to verify prayer_subject:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify prayer subject", "details": err.Error()})
			return
		}

		if !subjectExists {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Prayer subject not found or does not belong to you"})
			return
		}

		prayerSubjectID = *newPrayer.Prayer_Subject_ID
	} else {
		// Fall back to self subject for backwards compatibility
		prayerSubjectID, err = GetOrCreateSelfPrayerSubject(currentUser)
		if err != nil {
			log.Println("Failed to get/create self prayer_subject:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create prayer subject", "details": err.Error()})
			return
		}
	}

	// Shift all existing prayers in this subject down by incrementing their subject_display_sequence
	// This makes room for the new prayer at position 0 (top of subject list)
	updateSubjectSeqQuery := initializers.DB.Update("prayer").
		Set(goqu.Record{"subject_display_sequence": goqu.L("subject_display_sequence + 1")}).
		Where(
			goqu.C("prayer_subject_id").Eq(prayerSubjectID),
			goqu.C("deleted").Eq(false),
		)

	_, err = updateSubjectSeqQuery.Executor().Exec()
	if err != nil {
		log.Println("Failed to update prayer subject display sequence:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder prayers in subject", "details": err.Error()})
		return
	}

	newPrayerEntry := models.Prayer{
		Prayer_Type:              newPrayer.Prayer_Type,
		Is_Private:               newPrayer.Is_Private,
		Title:                    newPrayer.Title,
		Prayer_Description:       newPrayer.Prayer_Description,
		Is_Answered:              newPrayer.Is_Answered,
		Datetime_Answered:        newPrayer.Datetime_Answered,
		Prayer_Priority:          newPrayer.Prayer_Priority,
		Prayer_Subject_ID:        &prayerSubjectID,
		Subject_Display_Sequence: 0, // New prayers appear at the top of their subject
		Created_By:               currentUser.User_Profile_ID,
		Updated_By:               currentUser.User_Profile_ID,
		Datetime_Create:          time.Now(),
		Datetime_Update:          time.Now(),
	}

	prayerInsert := initializers.DB.Insert("prayer").Rows(newPrayerEntry).Returning("prayer_id")

	var insertedPrayerID int
	_, err = prayerInsert.Executor().ScanVal(&insertedPrayerID)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create prayer record", "details": err.Error()})
		return
	}

	// Shift all existing prayers down by incrementing their display_sequence
	// This makes room for the new prayer at position 0 (top of list)
	updateQuery := initializers.DB.Update("prayer_access").
		Set(goqu.Record{"display_sequence": goqu.L("display_sequence + 1")}).
		Where(
			goqu.C("access_type").Eq("user"),
			goqu.C("access_type_id").Eq(userID),
		)

	_, err = updateQuery.Executor().Exec()
	if err != nil {
		log.Println("Failed to update prayer display sequence:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder prayers", "details": err.Error()})
		return
	}

	// Insert new prayer at position 0 (top of list)
	newPrayerAccessEntry := models.PrayerAccess{
		Prayer_ID:        insertedPrayerID,
		Access_Type:      "user",
		Access_Type_ID:   userID,
		Display_Sequence: 0,
		Created_By:       currentUser.User_Profile_ID,
		Updated_By:       currentUser.User_Profile_ID,
		Datetime_Create:  time.Now(),
		Datetime_Update:  time.Now(),
	}

	prayerAccessInsert := initializers.DB.Insert("prayer_access").Rows(newPrayerAccessEntry).Returning("prayer_access_id")

	var insertedPrayerAccessID int
	_, err = prayerAccessInsert.Executor().ScanVal(&insertedPrayerAccessID)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create prayer access record", "details": err.Error()})
		return
	}

	// Log prayer creation to history (async, non-blocking)
	go func(prayerID int, userID int) {
		historyEntry := models.PrayerEditHistory{
			Prayer_ID:       prayerID,
			User_Profile_ID: userID,
			Action_Type:     models.HistoryActionCreated,
		}
		insert := initializers.DB.Insert("prayer_edit_history").Rows(historyEntry)
		_, err := insert.Executor().Exec()
		if err != nil {
			log.Printf("Failed to log prayer creation to history: %v", err)
		}
	}(insertedPrayerID, currentUser.User_Profile_ID)

	c.JSON(http.StatusCreated, gin.H{"message": "Prayer created sucessfully!",
		"prayerId":       insertedPrayerID,
		"prayerAccessId": insertedPrayerAccessID})
}

func ReorderUserPrayers(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if currentUser.User_Profile_ID != userID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to reorder this user's prayers"})
		return
	}

	var reorderData struct {
		Prayers []struct {
			PrayerID        int `json:"prayerId"`
			DisplaySequence int `json:"displaySequence"`
		} `json:"prayers"`
	}

	if err := c.BindJSON(&reorderData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get total count of prayers for this user
	var totalPrayers int
	_, err = initializers.DB.From("prayer_access").
		Select(goqu.COUNT("prayer_access_id")).
		Where(
			goqu.C("access_type").Eq("user"),
			goqu.C("access_type_id").Eq(userID),
		).
		ScanVal(&totalPrayers)
	if err != nil {
		log.Println("Failed to count user prayers:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count prayers", "details": err.Error()})
		return
	}

	// Validate that all prayers are included in the request
	if len(reorderData.Prayers) != totalPrayers {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Invalid reorder request: expected %d prayers, got %d. All prayers must be included in reorder request.", totalPrayers, len(reorderData.Prayers)),
		})
		return
	}

	// Validate that all displaySequence values are unique and contiguous
	sequenceMap := make(map[int]bool)
	for _, prayer := range reorderData.Prayers {
		if prayer.DisplaySequence < 0 || prayer.DisplaySequence >= totalPrayers {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Invalid displaySequence %d: must be between 0 and %d", prayer.DisplaySequence, totalPrayers-1),
			})
			return
		}
		if sequenceMap[prayer.DisplaySequence] {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Duplicate displaySequence %d: each prayer must have a unique sequence", prayer.DisplaySequence),
			})
			return
		}
		sequenceMap[prayer.DisplaySequence] = true
	}

	// Update each prayer's display_sequence in prayer_access table
	for _, prayer := range reorderData.Prayers {
		updateQuery := initializers.DB.Update("prayer_access").
			Set(goqu.Record{"display_sequence": prayer.DisplaySequence}).
			Where(
				goqu.C("prayer_id").Eq(prayer.PrayerID),
				goqu.C("access_type").Eq("user"),
				goqu.C("access_type_id").Eq(userID),
			)

		_, err := updateQuery.Executor().Exec()
		if err != nil {
			log.Println("Failed to update prayer display sequence:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder prayers", "details": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Prayers reordered successfully"})
}

func GetUserPreferencesWithDefaults(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if userID != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to view this user's preferences"})
		return
	}

	// First, get all default preferences
	var defaultPrefs []models.Preference
	dbErr := initializers.DB.From("preference").
		Select("preference_id", "preference_key", "default_value", "description", "value_type").
		Where(goqu.C("is_active").IsTrue()).
		Order(goqu.C("preference_key").Asc()).
		ScanStructsContext(c, &defaultPrefs)

	if dbErr != nil {
		c.JSON(500, gin.H{"error": "Failed to load default preferences", "details": dbErr.Error()})
		return
	}

	// Then, get user's custom preferences
	var userPrefs []models.UserPreferences
	dbErr = initializers.DB.From("user_preferences").
		Select("preference_key", "preference_value").
		Where(goqu.C("user_profile_id").Eq(userID)).
		ScanStructsContext(c, &userPrefs)

	if dbErr != nil {
		c.JSON(500, gin.H{"error": "Failed to load user preferences", "details": dbErr.Error()})
		return
	}

	// Create a map of user preferences for quick lookup
	userPrefMap := make(map[string]string)
	for _, pref := range userPrefs {
		userPrefMap[pref.Preference_Key] = pref.Preference_Value
	}

	// Build the response with defaults overridden by user preferences
	var responsePrefs []map[string]interface{}
	for _, defaultPref := range defaultPrefs {
		prefValue := defaultPref.Default_Value
		isDefault := true

		if userValue, exists := userPrefMap[defaultPref.Preference_Key]; exists {
			prefValue = userValue
			isDefault = false
		}

		responsePrefs = append(responsePrefs, map[string]interface{}{
			"preferenceId": defaultPref.Preference_ID,
			"key":          defaultPref.Preference_Key,
			"value":        prefValue,
			"description":  defaultPref.Description,
			"valueType":    defaultPref.Value_Type,
			"isDefault":    isDefault,
		})
	}

	c.JSON(200, gin.H{
		"message":     "User preferences retrieved successfully.",
		"preferences": responsePrefs,
	})
}

func GetUserPreferences(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if userID != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to view this user's preferences"})
		return
	}

	var preferences []models.UserPreferences

	dbErr := initializers.DB.From("user_preferences").
		Select("*").
		Where(goqu.C("user_profile_id").Eq(userID)).
		Order(goqu.C("preference_key").Asc()).
		ScanStructsContext(c, &preferences)

	if dbErr != nil {
		c.JSON(500, gin.H{"error": dbErr.Error()})
		return
	}

	if len(preferences) == 0 {
		c.JSON(200, gin.H{"message": "No preferences found for this user"})
		return
	}

	c.JSON(200, gin.H{
		"message":     "User preferences retrieved successfully.",
		"preferences": preferences,
	})
}

func UpdateUserPreferences(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if userID != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to update this user's preferences"})
		return
	}

	preferenceID, err := strconv.Atoi(c.Param("preference_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid preference ID", "details": err.Error()})
		return
	}

	var updatedPreference models.UserPreferencesUpdate
	if err := c.BindJSON(&updatedPreference); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// First, verify the preference_id exists in the preference table
	var preference []models.Preference
	dbErr := initializers.DB.From("preference").
		Select("*").
		Where(goqu.C("preference_id").Eq(preferenceID)).
		ScanStructsContext(c, &preference)

	if dbErr != nil {
		c.JSON(500, gin.H{"error": "Failed to verify preference", "details": dbErr.Error()})
		return
	}

	if len(preference) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Preference not found"})
		return
	}

	basePreference := preference[0]

	// Validate that the preference key matches
	if basePreference.Preference_Key != updatedPreference.Preference_Key {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Preference key mismatch: expected '%s', but received '%s'",
				basePreference.Preference_Key,
				updatedPreference.Preference_Key),
		})
		return
	}

	// Check if user already has a custom preference for this preference_id
	var existingUserPrefs []models.UserPreferences
	dbErr = initializers.DB.From("user_preferences").
		Select("*").
		Where(goqu.And(
			goqu.C("user_profile_id").Eq(userID),
			goqu.C("preference_key").Eq(basePreference.Preference_Key),
		)).
		ScanStructsContext(c, &existingUserPrefs)

	if dbErr != nil {
		c.JSON(500, gin.H{"error": "Failed to check existing user preferences", "details": dbErr.Error()})
		return
	}

	// Perform validation based on preference type
	if basePreference.Value_Type == "boolean" &&
		updatedPreference.Preference_Value != "true" &&
		updatedPreference.Preference_Value != "false" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Invalid value for boolean preference '%s'. Allowed values are 'true' or 'false', but received '%s'",
				basePreference.Preference_Key,
				updatedPreference.Preference_Value),
		})
		return
	}

	if updatedPreference.Preference_Key == "theme" &&
		updatedPreference.Preference_Value != "light" &&
		updatedPreference.Preference_Value != "dark" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Invalid value for preference key 'theme'. Allowed values are 'light' or 'dark', but received '%s'",
				updatedPreference.Preference_Value),
		})
		return
	}

	// Check if this would be a no-op (same value)
	if len(existingUserPrefs) > 0 {
		existing := existingUserPrefs[0]
		if existing.Preference_Value == updatedPreference.Preference_Value &&
			existing.Is_Active == updatedPreference.Is_Active {
			c.JSON(http.StatusOK, gin.H{
				"message": "No changes detected in the user preferences. No update performed.",
				"preference": map[string]interface{}{
					"preferenceId": basePreference.Preference_ID,
					"key":          basePreference.Preference_Key,
					"value":        existing.Preference_Value,
					"description":  basePreference.Description,
					"valueType":    basePreference.Value_Type,
					"isDefault":    false,
				},
			})
			return
		}

		// Update existing user preference
		update := initializers.DB.Update("user_preferences").
			Set(goqu.Record{
				"preference_value": updatedPreference.Preference_Value,
				"is_active":        updatedPreference.Is_Active,
				"datetime_update":  time.Now(),
			}).
			Where(goqu.C("user_preferences_id").Eq(existing.User_Preferences_ID))

		_, err := update.Executor().Exec()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user preferences", "details": err.Error()})
			return
		}
	} else {
		// Insert new user preference
		newUserPref := models.UserPreferences{
			User_Profile_ID:  userID,
			Preference_Key:   basePreference.Preference_Key,
			Preference_Value: updatedPreference.Preference_Value,
			Is_Active:        updatedPreference.Is_Active,
			Datetime_Create:  time.Now(),
			Datetime_Update:  time.Now(),
		}

		insert := initializers.DB.Insert("user_preferences").Rows(newUserPref).Executor()
		if _, err := insert.Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user preference", "details": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "User preferences updated successfully.",
		"preference": map[string]interface{}{
			"preferenceId": basePreference.Preference_ID,
			"key":          basePreference.Preference_Key,
			"value":        updatedPreference.Preference_Value,
			"description":  basePreference.Description,
			"valueType":    basePreference.Value_Type,
			"isDefault":    false,
		},
	})
}

func StorePushToken(c *gin.Context) {
	var request models.PushTokenRequest

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate push token
	if len(request.PushToken) < 20 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid push token: token too short"})
		return
	}

	if len(request.PushToken) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid push token: token too long"})
		return
	}

	// Validate platform
	if request.Platform != "ios" && request.Platform != "android" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid platform: must be 'ios' or 'android'"})
		return
	}

	// Get user ID from JWT token
	userClaim, exists := c.Get("currentUser")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	user := userClaim.(models.UserProfile)
	userID := user.User_Profile_ID

	// Use UPSERT (INSERT ... ON CONFLICT) to handle duplicate tokens atomically
	// If the (user_profile_id, push_token) combination already exists, update the platform and timestamp
	// Otherwise, insert a new record
	log.Printf("Upserting push token for user %d", userID)

	// Use goqu.Record to avoid including the auto-generated ID field
	newTokenRecord := goqu.Record{
		"user_profile_id": userID,
		"push_token":      request.PushToken,
		"platform":        request.Platform,
		"created_at":      time.Now(),
		"updated_at":      time.Now(),
	}

	// Build the INSERT query with ON CONFLICT clause
	insert := initializers.DB.Insert("user_push_tokens").
		Rows(newTokenRecord).
		OnConflict(goqu.DoUpdate(
			"user_profile_id, push_token", // The columns with the unique constraint
			goqu.Record{
				"platform":   request.Platform,
				"updated_at": time.Now(),
			},
		))

	sql, params, _ := insert.ToSQL()
	log.Printf("Executing upsert query: %s, params: %v", sql, params)

	_, err := insert.Executor().Exec()
	if err != nil {
		log.Printf("Failed to upsert push token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store push token", "details": err.Error()})
		return
	}

	log.Printf("Push token upserted successfully for user %d", userID)
	c.JSON(http.StatusOK, gin.H{"message": "Push token stored successfully"})
}

func ChangeUserPassword(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	// Authorization: user can only change their own password unless they're an admin
	if userID != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to change this user's password"})
		return
	}

	var passwordChange models.UserProfileChangePassword
	if err := c.ShouldBindJSON(&passwordChange); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify the user exists and get their current password
	var existingUser models.UserProfile
	_, err = initializers.DB.From("user_profile").
		Select("*").
		Where(goqu.C("user_profile_id").Eq(userID)).
		ScanStruct(&existingUser)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found", "details": err.Error()})
		return
	}

	// Verify old password (unless admin is changing another user's password).
	// OAuth-only accounts (NULL password) skip the check so they can set a
	// first password — the JWT already proves account ownership.
	if userID == currentUser.User_Profile_ID && existingUser.Password != nil {
		err = bcrypt.CompareHashAndPassword([]byte(*existingUser.Password), []byte(passwordChange.Old_Password))
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Current password is incorrect"})
			return
		}
	}

	// Validate new password
	if len(passwordChange.New_Password) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "New password must be at least 6 characters long"})
		return
	}

	// Hash the new password
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(passwordChange.New_Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash new password", "details": err.Error()})
		return
	}

	// Update the password
	updateRecord := goqu.Record{
		"password":        string(passwordHash),
		"updated_by":      currentUser.User_Profile_ID,
		"datetime_update": time.Now(),
	}

	update := initializers.DB.Update("user_profile").
		Set(updateRecord).
		Where(goqu.C("user_profile_id").Eq(userID))

	_, err = update.Executor().Exec()
	if err != nil {
		log.Println("Password update error:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Password changed successfully",
	})
}

func UpdateUserProfile(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if userID != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to update this user's profile"})
		return
	}

	var updateData models.UserProfileUpdate
	if err := c.ShouldBindJSON(&updateData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify the user exists
	var existingUser models.UserProfile
	_, err = initializers.DB.From("user_profile").
		Select("*").
		Where(goqu.C("user_profile_id").Eq(userID)).
		ScanStruct(&existingUser)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found", "details": err.Error()})
		return
	}

	// Build the update record with only provided fields
	updateRecord := goqu.Record{
		"updated_by":      currentUser.User_Profile_ID,
		"datetime_update": time.Now(),
	}

	// Validate and add each field if provided
	if updateData.First_Name != nil {
		firstName := strings.TrimSpace(*updateData.First_Name)
		if firstName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "First name cannot be empty"})
			return
		}
		updateRecord["first_name"] = firstName
	}

	if updateData.Last_Name != nil {
		lastName := strings.TrimSpace(*updateData.Last_Name)
		if lastName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Last name cannot be empty"})
			return
		}
		updateRecord["last_name"] = lastName
	}

	if updateData.Email != nil {
		email := strings.TrimSpace(*updateData.Email)
		if email == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Email cannot be empty"})
			return
		}

		// Basic email validation
		if !strings.Contains(email, "@") || !strings.Contains(email, ".") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid email format"})
			return
		}

		// Check if email is already in use by another user
		if email != existingUser.Email {
			emailCount, err := initializers.DB.From("user_profile").
				Select("email").
				Where(goqu.And(
					goqu.C("email").Eq(email),
					goqu.C("user_profile_id").Neq(userID),
				)).
				Count()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check email availability", "details": err.Error()})
				return
			}

			if emailCount > 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Email is already in use by another account"})
				return
			}

			updateRecord["email"] = email
			// Reset email verification if email changed
			updateRecord["email_verified"] = false
		}
	}

	if updateData.Username != nil {
		username := strings.TrimSpace(*updateData.Username)
		if username == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Username cannot be empty"})
			return
		}

		// Check if username is already in use by another user
		if username != existingUser.Username {
			usernameCount, err := initializers.DB.From("user_profile").
				Select("username").
				Where(goqu.And(
					goqu.C("username").Eq(username),
					goqu.C("user_profile_id").Neq(userID),
				)).
				Count()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check username availability", "details": err.Error()})
				return
			}

			if usernameCount > 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Username is already taken"})
				return
			}

			updateRecord["username"] = username
		}
	}

	// Phone number can be set to empty/null
	if updateData.Phone_Number != nil {
		phoneNumber := strings.TrimSpace(*updateData.Phone_Number)
		if phoneNumber == "" {
			updateRecord["phone_number"] = nil
			// Reset phone verification if phone number is removed
			updateRecord["phone_verified"] = false
		} else {
			// Basic phone number validation (remove all non-digits and check length)
			digitsOnly := strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' {
					return r
				}
				return -1
			}, phoneNumber)

			if len(digitsOnly) < 10 || len(digitsOnly) > 15 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid phone number format"})
				return
			}

			updateRecord["phone_number"] = phoneNumber
			// Reset phone verification if phone number changed
			if existingUser.Phone_Number == nil || *existingUser.Phone_Number != phoneNumber {
				updateRecord["phone_verified"] = false
			}
		}
	}

	// Check if there are any fields to update (beyond updated_by and datetime_update)
	if len(updateRecord) <= 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No valid fields provided for update"})
		return
	}

	// Perform the update
	update := initializers.DB.Update("user_profile").
		Set(updateRecord).
		Where(goqu.C("user_profile_id").Eq(userID))

	_, err = update.Executor().Exec()
	if err != nil {
		log.Println("Update error:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user profile", "details": err.Error()})
		return
	}

	// Fetch and return the updated user profile
	var updatedUser models.UserProfile
	_, err = initializers.DB.From("user_profile").
		Select("*").
		Where(goqu.C("user_profile_id").Eq(userID)).
		ScanStruct(&updatedUser)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User updated but failed to retrieve updated data", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "User profile updated successfully",
		"user":    updatedUser,
	})
}

func DeleteUserAccount(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	// Authorization: user can only delete their own account unless they're an admin
	if userID != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to delete this account"})
		return
	}

	// Verify the user exists
	var existingUser models.UserProfile
	found, err := initializers.DB.From("user_profile").
		Select("*").
		Where(goqu.C("user_profile_id").Eq(userID)).
		ScanStruct(&existingUser)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	log.Printf("Starting account deletion for user_profile_id: %d", userID)

	// Helper function to safely delete from optional tables (handles missing tables gracefully)
	safeDeleteOptional := func(tableName string, condition goqu.Expression) error {
		_, err := initializers.DB.Delete(tableName).
			Where(condition).
			Executor().Exec()
		if err != nil {
			// Check if error is "relation does not exist" - if so, just log and continue
			if strings.Contains(err.Error(), "relation") && strings.Contains(err.Error(), "does not exist") {
				log.Printf("Optional table %s does not exist, skipping deletion", tableName)
				return nil
			}
			return err
		}
		return nil
	}

	// Begin cascade deletion - order matters to avoid foreign key constraint violations
	// Based on actual database schema

	// 1. Delete push tokens (optional table - may not exist in all databases)
	err = safeDeleteOptional("user_push_tokens", goqu.C("user_profile_id").Eq(userID))
	if err != nil {
		log.Printf("Failed to delete user_push_tokens: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete push tokens", "details": err.Error()})
		return
	}

	// 2. Delete password reset tokens (optional table)
	err = safeDeleteOptional("password_reset_tokens", goqu.C("user_profile_id").Eq(userID))
	if err != nil {
		log.Printf("Failed to delete password_reset_tokens: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete password reset tokens", "details": err.Error()})
		return
	}

	// 2b. Revoke any linked OAuth provider tokens before the identity rows
	// below are deleted (Apple mandates revoke-on-account-delete). This is
	// best-effort by design — a failed revoke must never block deletion.
	revokeIdentityTokens(c.Request.Context(), userID)

	// 2c. Delete OAuth identity links, pending links, and refresh tokens
	// (optional tables — added by migrations 026/027)
	for _, table := range []string{"user_external_identity", "oauth_pending_link", "auth_refresh_token"} {
		err = safeDeleteOptional(table, goqu.C("user_profile_id").Eq(userID))
		if err != nil {
			log.Printf("Failed to delete %s: %v", table, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete OAuth account data", "details": err.Error()})
			return
		}
	}

	// 3. Delete prayer session details (must delete BEFORE prayer_session due to FK)
	// prayer_session_detail links to prayer_session, not directly to user
	_, err = initializers.DB.Delete("prayer_session_detail").
		Where(goqu.L("prayer_session_id IN (SELECT prayer_session_id FROM prayer_session WHERE user_profile_id = ?)", userID)).
		Executor().Exec()
	if err != nil {
		log.Printf("Failed to delete prayer_session_detail: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete prayer session details", "details": err.Error()})
		return
	}

	// 4. Delete prayer sessions
	_, err = initializers.DB.Delete("prayer_session").
		Where(goqu.C("user_profile_id").Eq(userID)).
		Executor().Exec()
	if err != nil {
		log.Printf("Failed to delete prayer_session: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete prayer sessions", "details": err.Error()})
		return
	}

	// 5. Delete user stats
	_, err = initializers.DB.Delete("user_stats").
		Where(goqu.C("user_profile_id").Eq(userID)).
		Executor().Exec()
	if err != nil {
		log.Printf("Failed to delete user_stats: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user stats", "details": err.Error()})
		return
	}

	// 6. Delete user preferences
	_, err = initializers.DB.Delete("user_preferences").
		Where(goqu.C("user_profile_id").Eq(userID)).
		Executor().Exec()
	if err != nil {
		log.Printf("Failed to delete user_preferences: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user preferences", "details": err.Error()})
		return
	}

	// 7. Delete notifications
	_, err = initializers.DB.Delete("notification").
		Where(goqu.C("user_profile_id").Eq(userID)).
		Executor().Exec()
	if err != nil {
		log.Printf("Failed to delete notifications: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete notifications", "details": err.Error()})
		return
	}

	// 8. Delete group invites created by this user
	// Note: group_invite has ON DELETE CASCADE for group_profile_id, so it will auto-delete when groups are deleted
	// We only need to delete invites created by this user
	_, err = initializers.DB.Delete("group_invite").
		Where(goqu.C("created_by").Eq(userID)).
		Executor().Exec()
	if err != nil {
		log.Printf("Failed to delete group_invite: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete group invites", "details": err.Error()})
		return
	}

	// 9. Remove user from all groups
	_, err = initializers.DB.Delete("user_group").
		Where(goqu.C("user_profile_id").Eq(userID)).
		Executor().Exec()
	if err != nil {
		log.Printf("Failed to delete user_group: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove user from groups", "details": err.Error()})
		return
	}

	// 10. Delete user's personal prayer access records (access_type = 'user')
	_, err = initializers.DB.Delete("prayer_access").
		Where(
			goqu.C("access_type").Eq("user"),
			goqu.C("access_type_id").Eq(userID),
		).
		Executor().Exec()
	if err != nil {
		log.Printf("Failed to delete prayer_access: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete prayer access records", "details": err.Error()})
		return
	}

	// 11. Delete prayer analytics for prayers created by this user (optional table)
	// Must delete BEFORE deleting prayers due to FK constraint
	err = safeDeleteOptional("prayer_analytics", goqu.L("prayer_id IN (SELECT prayer_id FROM prayer WHERE created_by = ?)", userID))
	if err != nil {
		log.Printf("Failed to delete prayer_analytics: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete prayer analytics", "details": err.Error()})
		return
	}

	// 12. Delete prayers created by this user
	// Note: Group prayers will remain for other group members
	_, err = initializers.DB.Delete("prayer").
		Where(goqu.C("created_by").Eq(userID)).
		Executor().Exec()
	if err != nil {
		log.Printf("Failed to delete prayers: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete prayers", "details": err.Error()})
		return
	}

	// 13. Finally, hard delete the user profile
	_, err = initializers.DB.Delete("user_profile").
		Where(goqu.C("user_profile_id").Eq(userID)).
		Executor().Exec()
	if err != nil {
		log.Printf("Failed to delete user profile: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user account", "details": err.Error()})
		return
	}

	log.Printf("Successfully hard deleted account for user_profile_id: %d", userID)

	c.JSON(http.StatusOK, gin.H{
		"message": "Account deleted successfully",
	})
}

// selfSubjectDB is the subset of goqu operations getOrCreateSelfPrayerSubject
// needs; both *goqu.Database and *goqu.TxDatabase satisfy it, so the subject
// can be created inside a caller's transaction.
type selfSubjectDB interface {
	From(from ...interface{}) *goqu.SelectDataset
	Insert(table interface{}) *goqu.InsertDataset
}

// GetOrCreateSelfPrayerSubject finds or creates a "self" prayer_subject for a user.
// A "self" prayer_subject is one where the user is praying for themselves.
// This is identified by: created_by = user_profile_id AND user_profile_id = user_profile_id (linked to self)
func GetOrCreateSelfPrayerSubject(user models.UserProfile) (int, error) {
	return getOrCreateSelfPrayerSubject(initializers.DB, user)
}

func getOrCreateSelfPrayerSubject(db selfSubjectDB, user models.UserProfile) (int, error) {
	// First, try to find an existing "self" prayer_subject
	var existingSubjectID int
	found, err := db.From("prayer_subject").
		Select("prayer_subject_id").
		Where(
			goqu.And(
				goqu.C("created_by").Eq(user.User_Profile_ID),
				goqu.C("user_profile_id").Eq(user.User_Profile_ID),
			),
		).
		ScanVal(&existingSubjectID)

	if err != nil {
		return 0, fmt.Errorf("failed to check for existing self prayer_subject: %v", err)
	}

	if found {
		return existingSubjectID, nil
	}

	// No existing "self" prayer_subject, create one
	displayName := strings.TrimSpace(user.First_Name + " " + user.Last_Name)
	if displayName == "" {
		displayName = user.Username
	}
	if displayName == "" {
		displayName = "Me"
	}

	newSubject := models.PrayerSubject{
		Prayer_Subject_Type:         "individual",
		Prayer_Subject_Display_Name: displayName,
		User_Profile_ID:             &user.User_Profile_ID,
		Use_Linked_User_Photo:       true,
		Link_Status:                 "linked",
		Display_Sequence:            0,
		Created_By:                  user.User_Profile_ID,
		Updated_By:                  user.User_Profile_ID,
	}

	insert := db.Insert("prayer_subject").Rows(newSubject).Returning("prayer_subject_id")

	var insertedID int
	_, err = insert.Executor().ScanVal(&insertedID)
	if err != nil {
		return 0, fmt.Errorf("failed to create self prayer_subject: %v", err)
	}

	log.Printf("Created self prayer_subject %d for user %d", insertedID, user.User_Profile_ID)
	return insertedID, nil
}
