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
	"github.com/doug-martin/goqu/v9"
)

// GetUserPrayerSubjects returns all prayer subjects for a user with their nested prayers
func GetUserPrayerSubjects(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if userID != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to view this user's prayer subjects"})
		return
	}

	// First, get all prayer subjects for the user
	var prayerSubjects []models.PrayerSubject
	dbErr := initializers.DB.From("prayer_subject").
		Select("*").
		Where(goqu.C("created_by").Eq(userID)).
		Order(goqu.C("display_sequence").Asc()).
		ScanStructsContext(c, &prayerSubjects)

	if dbErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer subjects", "details": dbErr.Error()})
		return
	}

	// If no prayer subjects, return empty array
	if len(prayerSubjects) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"message":        "No prayer subjects found.",
			"prayerSubjects": []models.PrayerSubjectWithPrayers{},
		})
		return
	}

	// Build response with nested prayers for each subject
	var result []models.PrayerSubjectWithPrayers

	for _, subject := range prayerSubjects {
		// Get prayers for this subject
		var prayers []models.UserPrayer
		dbErr := initializers.DB.From("prayer_access").
			Select(
				goqu.L("CASE WHEN prayer_access.access_type = 'user' THEN prayer_access.access_type_id ELSE prayer_access.created_by END").As("user_profile_id"),
				goqu.I("prayer.prayer_id"),
				goqu.I("prayer_access.prayer_access_id"),
				goqu.I("prayer_access.display_sequence"),
				goqu.I("prayer.subject_display_sequence"),
				goqu.I("prayer.prayer_type"),
				goqu.I("prayer.is_private"),
				goqu.I("prayer.title"),
				goqu.I("prayer.prayer_description"),
				goqu.I("prayer.is_answered"),
				goqu.I("prayer.prayer_priority"),
				goqu.I("prayer.prayer_subject_id"),
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
					goqu.Ex{"prayer.deleted": false},
					goqu.Or(
						goqu.And(
							goqu.Ex{"prayer_access.access_type": "user"},
							goqu.Ex{"prayer_access.access_type_id": userID},
							goqu.Ex{"prayer.prayer_subject_id": subject.Prayer_Subject_ID},
						),
						goqu.And(
							goqu.Ex{"prayer_access.access_type": "subject"},
							goqu.Ex{"prayer_access.access_type_id": subject.Prayer_Subject_ID},
							goqu.Ex{"prayer_access.created_by": userID},
						),
					),
				),
			).
			Order(
				// Sort by is_answered: false/NULL first (active), true last (answered)
				// Use COALESCE to treat NULL as false for consistent sorting
				goqu.L("COALESCE(prayer.is_answered, false)").Asc(),
				goqu.I("prayer.subject_display_sequence").Asc(),
			).
			ScanStructsContext(c, &prayers)

		if dbErr != nil {
			log.Printf("Failed to fetch prayers for subject %d: %v", subject.Prayer_Subject_ID, dbErr)
			prayers = []models.UserPrayer{}
		}

		if prayers == nil {
			prayers = []models.UserPrayer{}
		}

		// Check if sequences need resequencing (detect gaps or duplicates)
		// Only resequence if we have prayers and detect issues
		if len(prayers) > 0 {
			needsResequence := false
			seenSequences := make(map[int]bool)
			for i, prayer := range prayers {
				// Check for duplicates or non-contiguous sequences
				if seenSequences[prayer.Subject_Display_Sequence] || prayer.Subject_Display_Sequence != i {
					needsResequence = true
					break
				}
				seenSequences[prayer.Subject_Display_Sequence] = true
			}

			if needsResequence {
				log.Printf("Detected non-contiguous sequences for subject %d, resequencing...", subject.Prayer_Subject_ID)
				if err := resequencePrayersInSubject(userID, subject.Prayer_Subject_ID); err != nil {
					log.Printf("Warning: Failed to resequence prayers for subject %d: %v", subject.Prayer_Subject_ID, err)
				} else {
					// Re-fetch prayers after resequencing to get correct order
					// IMPORTANT: Reset slice first to avoid appending duplicates
					prayers = []models.UserPrayer{}
					dbErr = initializers.DB.From("prayer_access").
						Select(
							goqu.L("CASE WHEN prayer_access.access_type = 'user' THEN prayer_access.access_type_id ELSE prayer_access.created_by END").As("user_profile_id"),
							goqu.I("prayer.prayer_id"),
							goqu.I("prayer_access.prayer_access_id"),
							goqu.I("prayer_access.display_sequence"),
							goqu.I("prayer.subject_display_sequence"),
							goqu.I("prayer.prayer_type"),
							goqu.I("prayer.is_private"),
							goqu.I("prayer.title"),
							goqu.I("prayer.prayer_description"),
							goqu.I("prayer.is_answered"),
							goqu.I("prayer.prayer_priority"),
							goqu.I("prayer.prayer_subject_id"),
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
								goqu.Ex{"prayer.deleted": false},
								goqu.Or(
									goqu.And(
										goqu.Ex{"prayer_access.access_type": "user"},
										goqu.Ex{"prayer_access.access_type_id": userID},
										goqu.Ex{"prayer.prayer_subject_id": subject.Prayer_Subject_ID},
									),
									goqu.And(
										goqu.Ex{"prayer_access.access_type": "subject"},
										goqu.Ex{"prayer_access.access_type_id": subject.Prayer_Subject_ID},
										goqu.Ex{"prayer_access.created_by": userID},
									),
								),
							),
						).
						Order(
							goqu.L("COALESCE(prayer.is_answered, false)").Asc(),
							goqu.I("prayer.subject_display_sequence").Asc(),
						).
						ScanStructsContext(c, &prayers)

					if dbErr != nil {
						log.Printf("Failed to re-fetch prayers for subject %d after resequencing: %v", subject.Prayer_Subject_ID, dbErr)
					}
				}
			}
		}

		subjectWithPrayers := models.PrayerSubjectWithPrayers{
			Prayer_Subject_ID:           subject.Prayer_Subject_ID,
			Prayer_Subject_Type:         subject.Prayer_Subject_Type,
			Prayer_Subject_Display_Name: subject.Prayer_Subject_Display_Name,
			Notes:                       subject.Notes,
			Display_Sequence:            subject.Display_Sequence,
			Photo_S3_Key:                subject.Photo_S3_Key,
			User_Profile_ID:             subject.User_Profile_ID,
			Use_Linked_User_Photo:       subject.Use_Linked_User_Photo,
			Link_Status:                 subject.Link_Status,
			Phone_Number:                subject.Phone_Number,
			Email:                       subject.Email,
			Datetime_Create:             subject.Datetime_Create,
			Datetime_Update:             subject.Datetime_Update,
			Created_By:                  subject.Created_By,
			Updated_By:                  subject.Updated_By,
			Prayers:                     prayers,
		}

		result = append(result, subjectWithPrayers)
	}

	c.JSON(http.StatusOK, gin.H{
		"message":        "Prayer subjects retrieved successfully.",
		"prayerSubjects": result,
	})
}

// CreatePrayerSubject creates a new prayer subject for a user
func CreatePrayerSubject(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if currentUser.User_Profile_ID != userID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to create prayer subjects for this user"})
		return
	}

	var newSubject models.PrayerSubjectCreate
	if err := c.BindJSON(&newSubject); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate required fields
	if strings.TrimSpace(newSubject.Prayer_Subject_Display_Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Display name is required"})
		return
	}

	// Validate prayer subject type
	validTypes := map[string]bool{"individual": true, "family": true, "group": true}
	if newSubject.Prayer_Subject_Type == "" {
		newSubject.Prayer_Subject_Type = "individual" // Default to individual
	} else if !validTypes[newSubject.Prayer_Subject_Type] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer subject type. Must be 'individual', 'family', or 'group'"})
		return
	}

	// Get next display_sequence for the user
	// Use COALESCE to handle NULL when no prayer_subjects exist (returns -1, so next is 0)
	var maxSequence int
	_, err = initializers.DB.From("prayer_subject").
		Select(goqu.L("COALESCE(MAX(display_sequence), -1)")).
		Where(goqu.C("created_by").Eq(userID)).
		ScanVal(&maxSequence)

	if err != nil {
		log.Println("Failed to get max display_sequence:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to determine display order", "details": err.Error()})
		return
	}

	nextSequence := maxSequence + 1

	// Default useLinkedUserPhoto to false if not provided
	useLinkedUserPhoto := false
	if newSubject.Use_Linked_User_Photo != nil {
		useLinkedUserPhoto = *newSubject.Use_Linked_User_Photo
	}

	// Determine link status - match UpdatePrayerSubject behavior
	linkStatus := "unlinked"
	if newSubject.User_Profile_ID != nil && *newSubject.User_Profile_ID > 0 {
		// Link to any valid user (self or other) - authorization for editing is
		// controlled by the linked user's identity, not link_status
		linkStatus = "linked"
	}

	newPrayerSubject := models.PrayerSubject{
		Prayer_Subject_Type:         newSubject.Prayer_Subject_Type,
		Prayer_Subject_Display_Name: strings.TrimSpace(newSubject.Prayer_Subject_Display_Name),
		Notes:                       newSubject.Notes,
		Display_Sequence:            nextSequence,
		Photo_S3_Key:                newSubject.Photo_S3_Key,
		User_Profile_ID:             newSubject.User_Profile_ID,
		Use_Linked_User_Photo:       useLinkedUserPhoto,
		Link_Status:                 linkStatus,
		Phone_Number:                newSubject.Phone_Number,
		Email:                       newSubject.Email,
		Created_By:                  userID,
		Updated_By:                  userID,
	}

	insert := initializers.DB.Insert("prayer_subject").Rows(newPrayerSubject).Returning("prayer_subject_id")

	var insertedID int
	_, err = insert.Executor().ScanVal(&insertedID)
	if err != nil {
		log.Println("Failed to create prayer subject:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create prayer subject", "details": err.Error()})
		return
	}

	// Fetch the created subject to return
	var createdSubject models.PrayerSubject
	_, err = initializers.DB.From("prayer_subject").
		Select("*").
		Where(goqu.C("prayer_subject_id").Eq(insertedID)).
		ScanStruct(&createdSubject)

	if err != nil {
		c.JSON(http.StatusCreated, gin.H{
			"message":          "Prayer subject created successfully",
			"prayerSubjectId":  insertedID,
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":       "Prayer subject created successfully",
		"prayerSubject": createdSubject,
	})
}

// UpdatePrayerSubject updates an existing prayer subject
func UpdatePrayerSubject(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	subjectID, err := strconv.Atoi(c.Param("prayer_subject_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer subject ID", "details": err.Error()})
		return
	}

	// Verify the prayer subject exists and user has permission
	var existingSubject models.PrayerSubject
	found, err := initializers.DB.From("prayer_subject").
		Select("*").
		Where(goqu.C("prayer_subject_id").Eq(subjectID)).
		ScanStruct(&existingSubject)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer subject", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer subject not found"})
		return
	}

	// Check permission - must be creator or admin
	if existingSubject.Created_By != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to update this prayer subject"})
		return
	}

	var updateData models.PrayerSubjectUpdate
	if err := c.BindJSON(&updateData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// DEBUG: Log received update data
	log.Printf("UpdatePrayerSubject received data: %+v", updateData)

	// Build update record
	updateRecord := goqu.Record{
		"updated_by":      currentUser.User_Profile_ID,
		"datetime_update": time.Now(),
	}

	if updateData.Prayer_Subject_Display_Name != nil {
		displayName := strings.TrimSpace(*updateData.Prayer_Subject_Display_Name)
		if displayName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Display name cannot be empty"})
			return
		}
		updateRecord["prayer_subject_display_name"] = displayName
	}

	if updateData.Prayer_Subject_Type != nil {
		subjectType := strings.TrimSpace(*updateData.Prayer_Subject_Type)
		// Validate type is one of the allowed values
		if subjectType != "individual" && subjectType != "family" && subjectType != "group" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer subject type. Must be 'individual', 'family', or 'group'"})
			return
		}
		// If changing from family/group to individual, check for existing members
		if subjectType == "individual" && (existingSubject.Prayer_Subject_Type == "family" || existingSubject.Prayer_Subject_Type == "group") {
			var memberCount int
			_, err := initializers.DB.From("prayer_subject_membership").
				Select(goqu.COUNT("*")).
				Where(goqu.C("group_prayer_subject_id").Eq(subjectID)).
				ScanVal(&memberCount)
			if err == nil && memberCount > 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot change to individual type while members exist. Remove all members first."})
				return
			}
		}
		updateRecord["prayer_subject_type"] = subjectType
	}

	if updateData.Notes != nil {
		updateRecord["notes"] = updateData.Notes
	}

	if updateData.Photo_S3_Key != nil {
		updateRecord["photo_s3_key"] = updateData.Photo_S3_Key
	}

	if updateData.Use_Linked_User_Photo != nil {
		updateRecord["use_linked_user_photo"] = *updateData.Use_Linked_User_Photo
	}

	if updateData.User_Profile_ID != nil {
		// Allow setting userProfileId to link this contact to a Prayerloop user
		// Value of 0 or negative means unlink (set to null)
		if *updateData.User_Profile_ID > 0 {
			updateRecord["user_profile_id"] = *updateData.User_Profile_ID
			updateRecord["link_status"] = "linked"
		} else {
			updateRecord["user_profile_id"] = nil
			updateRecord["link_status"] = "unlinked"
		}
	}

	if updateData.Phone_Number != nil {
		// Empty string means clear the phone number
		if strings.TrimSpace(*updateData.Phone_Number) == "" {
			updateRecord["phone_number"] = nil
		} else {
			updateRecord["phone_number"] = *updateData.Phone_Number
		}
	}

	if updateData.Email != nil {
		// Empty string means clear the email
		if strings.TrimSpace(*updateData.Email) == "" {
			updateRecord["email"] = nil
		} else {
			updateRecord["email"] = *updateData.Email
		}
	}

	// Check if there are fields to update beyond updated_by and datetime_update
	if len(updateRecord) <= 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No valid fields provided for update"})
		return
	}

	// DEBUG: Log updateRecord being sent to database
	log.Printf("UpdatePrayerSubject updateRecord: %+v", updateRecord)

	// Perform update
	update := initializers.DB.Update("prayer_subject").
		Set(updateRecord).
		Where(goqu.C("prayer_subject_id").Eq(subjectID))

	_, err = update.Executor().Exec()
	if err != nil {
		log.Println("Failed to update prayer subject:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update prayer subject", "details": err.Error()})
		return
	}

	// Fetch and return the updated subject
	var updatedSubject models.PrayerSubject
	_, err = initializers.DB.From("prayer_subject").
		Select("*").
		Where(goqu.C("prayer_subject_id").Eq(subjectID)).
		ScanStruct(&updatedSubject)

	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "Prayer subject updated successfully"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "Prayer subject updated successfully",
		"prayerSubject": updatedSubject,
	})
}

// DeletePrayerSubject deletes a prayer subject and optionally its prayers
func DeletePrayerSubject(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	subjectID, err := strconv.Atoi(c.Param("prayer_subject_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer subject ID", "details": err.Error()})
		return
	}

	// Verify the prayer subject exists and user has permission
	var existingSubject models.PrayerSubject
	found, err := initializers.DB.From("prayer_subject").
		Select("*").
		Where(goqu.C("prayer_subject_id").Eq(subjectID)).
		ScanStruct(&existingSubject)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer subject", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer subject not found"})
		return
	}

	// Check permission - must be creator or admin
	if existingSubject.Created_By != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to delete this prayer subject"})
		return
	}

	// Check if this is a "self" subject (user_profile_id = created_by) - prevent deletion
	if existingSubject.User_Profile_ID != nil && *existingSubject.User_Profile_ID == existingSubject.Created_By {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot delete your personal 'self' prayer subject"})
		return
	}

	// Block deletion of contacts that originate from a circle the requesting
	// user is still a member of. Two cases:
	//
	//   1) Group-type subjects are tracked in prayer_subject_group_profile.
	//      Block when the linked group_profile is one the user is in.
	//   2) Individual contacts aren't in psgp by schema (the trigger only
	//      accepts group-type subjects), so we infer the link semantically:
	//      if the contact's user_profile_id is a member of any active
	//      user_group the requesting user is also in, block.
	var psgpHits int
	_, err = initializers.DB.From("prayer_subject_group_profile").
		Select(goqu.COUNT(goqu.I("prayer_subject_group_profile.prayer_subject_group_profile_id"))).
		Join(
			goqu.T("user_group"),
			goqu.On(goqu.Ex{"prayer_subject_group_profile.group_profile_id": goqu.I("user_group.group_profile_id")}),
		).
		Where(
			goqu.C("prayer_subject_id").Table("prayer_subject_group_profile").Eq(subjectID),
			goqu.C("user_profile_id").Table("user_group").Eq(currentUser.User_Profile_ID),
			goqu.C("is_active").Table("user_group").IsTrue(),
		).
		ScanVal(&psgpHits)
	if err != nil {
		log.Println("Failed to check group-type circle linkage:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check circle membership", "details": err.Error()})
		return
	}

	sharedCircleHits := 0
	if existingSubject.User_Profile_ID != nil && *existingSubject.User_Profile_ID > 0 {
		_, err = initializers.DB.From(goqu.T("user_group").As("ug_subject")).
			Select(goqu.COUNT(goqu.I("ug_subject.user_group_id"))).
			Join(
				goqu.T("user_group").As("ug_current"),
				goqu.On(goqu.Ex{"ug_subject.group_profile_id": goqu.I("ug_current.group_profile_id")}),
			).
			Where(
				goqu.C("user_profile_id").Table("ug_subject").Eq(*existingSubject.User_Profile_ID),
				goqu.C("user_profile_id").Table("ug_current").Eq(currentUser.User_Profile_ID),
				goqu.C("is_active").Table("ug_subject").IsTrue(),
				goqu.C("is_active").Table("ug_current").IsTrue(),
			).
			ScanVal(&sharedCircleHits)
		if err != nil {
			log.Println("Failed to check shared-circle membership for individual contact:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check circle membership", "details": err.Error()})
			return
		}
	}

	if psgpHits > 0 || sharedCircleHits > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "This contact is from a prayer circle you're a member of and can't be deleted"})
		return
	}

	// Check for associated prayers
	var prayerCount int
	_, err = initializers.DB.From("prayer").
		Select(goqu.COUNT("prayer_id")).
		Where(
			goqu.C("prayer_subject_id").Eq(subjectID),
			goqu.C("deleted").Eq(false),
		).
		ScanVal(&prayerCount)

	if err != nil {
		log.Println("Failed to count prayers for subject:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check associated prayers", "details": err.Error()})
		return
	}

	// If there are associated prayers, require explicit confirmation or reassign them
	deletePrayers := c.Query("deletePrayers") == "true"
	reassignToSelf := c.Query("reassignToSelf") == "true"

	if prayerCount > 0 {
		if !deletePrayers && !reassignToSelf {
			c.JSON(http.StatusConflict, gin.H{
				"error":       fmt.Sprintf("Prayer subject has %d associated prayers", prayerCount),
				"prayerCount": prayerCount,
				"options":     "Add ?deletePrayers=true to soft-delete prayers, or ?reassignToSelf=true to reassign them to your personal subject",
			})
			return
		}

		if reassignToSelf {
			// Get or create self subject
			selfSubjectID, err := GetOrCreateSelfPrayerSubject(currentUser)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get self prayer subject", "details": err.Error()})
				return
			}

			// Reassign prayers to self
			updatePrayers := initializers.DB.Update("prayer").
				Set(goqu.Record{
					"prayer_subject_id": selfSubjectID,
					"updated_by":        currentUser.User_Profile_ID,
					"datetime_update":   time.Now(),
				}).
				Where(goqu.C("prayer_subject_id").Eq(subjectID))

			_, err = updatePrayers.Executor().Exec()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reassign prayers", "details": err.Error()})
				return
			}
		} else if deletePrayers {
			// Soft-delete the associated prayers
			updatePrayers := initializers.DB.Update("prayer").
				Set(goqu.Record{
					"deleted":         true,
					"updated_by":      currentUser.User_Profile_ID,
					"datetime_update": time.Now(),
				}).
				Where(goqu.C("prayer_subject_id").Eq(subjectID))

			_, err = updatePrayers.Executor().Exec()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete associated prayers", "details": err.Error()})
				return
			}
		}
	}

	// Delete the prayer subject
	_, err = initializers.DB.Delete("prayer_subject").
		Where(goqu.C("prayer_subject_id").Eq(subjectID)).
		Executor().Exec()

	if err != nil {
		log.Println("Failed to delete prayer subject:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete prayer subject", "details": err.Error()})
		return
	}

	// Re-sequence remaining subjects
	err = resequencePrayerSubjects(existingSubject.Created_By)
	if err != nil {
		log.Printf("Warning: Failed to resequence prayer subjects: %v", err)
		// Don't fail the request, just log
	}

	c.JSON(http.StatusOK, gin.H{"message": "Prayer subject deleted successfully"})
}

// ReorderPrayerSubjects allows manual ordering of prayer subjects
func ReorderPrayerSubjects(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if currentUser.User_Profile_ID != userID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to reorder this user's prayer subjects"})
		return
	}

	var reorderData struct {
		Subjects []struct {
			PrayerSubjectID int `json:"prayerSubjectId"`
			DisplaySequence int `json:"displaySequence"`
		} `json:"subjects"`
	}

	if err := c.BindJSON(&reorderData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate request has subjects to reorder
	if len(reorderData.Subjects) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No subjects provided for reordering"})
		return
	}

	// Validate that all displaySequence values are unique (within the provided subjects)
	sequenceMap := make(map[int]bool)
	for _, subject := range reorderData.Subjects {
		if subject.DisplaySequence < 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Invalid displaySequence %d: must be non-negative", subject.DisplaySequence),
			})
			return
		}
		if sequenceMap[subject.DisplaySequence] {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Duplicate displaySequence %d: each subject must have a unique sequence", subject.DisplaySequence),
			})
			return
		}
		sequenceMap[subject.DisplaySequence] = true
	}

	// Update each subject's display_sequence
	for _, subject := range reorderData.Subjects {
		updateQuery := initializers.DB.Update("prayer_subject").
			Set(goqu.Record{
				"display_sequence": subject.DisplaySequence,
				"updated_by":       currentUser.User_Profile_ID,
				"datetime_update":  time.Now(),
			}).
			Where(
				goqu.C("prayer_subject_id").Eq(subject.PrayerSubjectID),
				goqu.C("created_by").Eq(userID),
			)

		_, err := updateQuery.Executor().Exec()
		if err != nil {
			log.Println("Failed to update prayer subject display sequence:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder prayer subjects", "details": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Prayer subjects reordered successfully"})
}

// ReorderPrayerSubjectPrayers allows manual ordering of prayers within a prayer subject
func ReorderPrayerSubjectPrayers(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	subjectID, err := strconv.Atoi(c.Param("prayer_subject_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer subject ID", "details": err.Error()})
		return
	}

	// Verify the prayer subject exists and user has permission
	var existingSubject models.PrayerSubject
	found, err := initializers.DB.From("prayer_subject").
		Select("*").
		Where(goqu.C("prayer_subject_id").Eq(subjectID)).
		ScanStruct(&existingSubject)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer subject", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer subject not found"})
		return
	}

	// Check permission - must be creator or admin
	if existingSubject.Created_By != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to reorder prayers in this prayer subject"})
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

	// Get total count of non-deleted prayers for this prayer subject that the user has access to
	// This must match how GetUserPrayerSubjects fetches prayers (via prayer_access join)
	var totalPrayers int
	countQuery := initializers.DB.From("prayer_access").
		Select(goqu.COUNT(goqu.DISTINCT("prayer.prayer_id"))).
		Join(
			goqu.T("prayer"),
			goqu.On(goqu.Ex{"prayer_access.prayer_id": goqu.I("prayer.prayer_id")}),
		).
		Where(
			goqu.And(
				goqu.Ex{"prayer_access.access_type": "user"},
				goqu.Ex{"prayer_access.access_type_id": currentUser.User_Profile_ID},
				goqu.Ex{"prayer.prayer_subject_id": subjectID},
				goqu.Ex{"prayer.deleted": false},
			),
		)

	_, err = countQuery.ScanVal(&totalPrayers)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count prayers", "details": err.Error()})
		return
	}

	// Also get the actual prayer IDs from the database for comparison
	var dbPrayerIDs []int
	err = initializers.DB.From("prayer_access").
		Select(goqu.DISTINCT("prayer.prayer_id")).
		Join(
			goqu.T("prayer"),
			goqu.On(goqu.Ex{"prayer_access.prayer_id": goqu.I("prayer.prayer_id")}),
		).
		Where(
			goqu.And(
				goqu.Ex{"prayer_access.access_type": "user"},
				goqu.Ex{"prayer_access.access_type_id": currentUser.User_Profile_ID},
				goqu.Ex{"prayer.prayer_subject_id": subjectID},
				goqu.Ex{"prayer.deleted": false},
			),
		).
		ScanVals(&dbPrayerIDs)
	if err != nil {
		dbPrayerIDs = []int{}
	}

	// Validate that all prayers are included in the request
	if len(reorderData.Prayers) != totalPrayers {
		// Build request prayer IDs for error response
		requestPrayerIDs := make([]int, len(reorderData.Prayers))
		for i, p := range reorderData.Prayers {
			requestPrayerIDs[i] = p.PrayerID
		}

		// Find which prayer IDs are in request but not in DB
		dbPrayerIDMap := make(map[int]bool)
		for _, id := range dbPrayerIDs {
			dbPrayerIDMap[id] = true
		}
		var extraInRequest []int
		for _, id := range requestPrayerIDs {
			if !dbPrayerIDMap[id] {
				extraInRequest = append(extraInRequest, id)
			}
		}

		// Find which prayer IDs are in DB but not in request
		requestPrayerIDMap := make(map[int]bool)
		for _, id := range requestPrayerIDs {
			requestPrayerIDMap[id] = true
		}
		var missingFromRequest []int
		for _, id := range dbPrayerIDs {
			if !requestPrayerIDMap[id] {
				missingFromRequest = append(missingFromRequest, id)
			}
		}

		c.JSON(http.StatusBadRequest, gin.H{
			"error":              fmt.Sprintf("Invalid reorder request: expected %d prayers, got %d. All prayers for this subject must be included.", totalPrayers, len(reorderData.Prayers)),
			"dbPrayerCount":      totalPrayers,
			"requestPrayerCount": len(reorderData.Prayers),
			"dbPrayerIDs":        dbPrayerIDs,
			"requestPrayerIDs":   requestPrayerIDs,
			"extraInRequest":     extraInRequest,
			"missingFromRequest": missingFromRequest,
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
				"error": fmt.Sprintf("Duplicate displaySequence %d: each value must be unique", prayer.DisplaySequence),
			})
			return
		}
		sequenceMap[prayer.DisplaySequence] = true
	}

	// Update each prayer's subject_display_sequence
	for _, prayer := range reorderData.Prayers {
		updateQuery := initializers.DB.Update("prayer").
			Set(goqu.Record{
				"subject_display_sequence": prayer.DisplaySequence,
				"updated_by":               currentUser.User_Profile_ID,
				"datetime_update":          time.Now(),
			}).
			Where(
				goqu.C("prayer_id").Eq(prayer.PrayerID),
				goqu.C("prayer_subject_id").Eq(subjectID),
			)

		_, err := updateQuery.Executor().Exec()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder prayers", "details": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Prayers reordered successfully"})
}

// resequencePrayersInSubject ensures subject_display_sequence values are contiguous (0, 1, 2, ...)
// for all prayers in a prayer subject that a user has access to.
// This cleans up any gaps from deleted prayers or duplicate sequences from legacy data.
func resequencePrayersInSubject(userID int, subjectID int) error {
	// Get all prayers for this subject that the user has access to, ordered by current sequence
	type PrayerSeq struct {
		Prayer_ID                int `db:"prayer_id"`
		Subject_Display_Sequence int `db:"subject_display_sequence"`
	}
	var prayers []PrayerSeq

	err := initializers.DB.From("prayer_access").
		Select(
			goqu.I("prayer.prayer_id"),
			goqu.I("prayer.subject_display_sequence"),
		).
		Join(
			goqu.T("prayer"),
			goqu.On(goqu.Ex{"prayer_access.prayer_id": goqu.I("prayer.prayer_id")}),
		).
		Where(
			goqu.And(
				goqu.Ex{"prayer_access.access_type": "user"},
				goqu.Ex{"prayer_access.access_type_id": userID},
				goqu.Ex{"prayer.prayer_subject_id": subjectID},
				goqu.Ex{"prayer.deleted": false},
			),
		).
		Order(goqu.I("prayer.subject_display_sequence").Asc()).
		ScanStructs(&prayers)

	if err != nil {
		return err
	}

	// Update any prayers that don't have the correct sequence
	for i, prayer := range prayers {
		if prayer.Subject_Display_Sequence != i {
			log.Printf("Resequencing prayer %d: %d -> %d", prayer.Prayer_ID, prayer.Subject_Display_Sequence, i)
			_, err := initializers.DB.Update("prayer").
				Set(goqu.Record{"subject_display_sequence": i}).
				Where(goqu.C("prayer_id").Eq(prayer.Prayer_ID)).
				Executor().Exec()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// resequencePrayerSubjects ensures display_sequence values are contiguous (0, 1, 2, ...)
func resequencePrayerSubjects(userID int) error {
	var subjects []models.PrayerSubject
	err := initializers.DB.From("prayer_subject").
		Select("prayer_subject_id", "display_sequence").
		Where(goqu.C("created_by").Eq(userID)).
		Order(goqu.C("display_sequence").Asc()).
		ScanStructs(&subjects)

	if err != nil {
		return err
	}

	for i, subject := range subjects {
		if subject.Display_Sequence != i {
			_, err := initializers.DB.Update("prayer_subject").
				Set(goqu.Record{"display_sequence": i}).
				Where(goqu.C("prayer_subject_id").Eq(subject.Prayer_Subject_ID)).
				Executor().Exec()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// GetSubjectMembers returns all members of a family/group prayer subject
func GetSubjectMembers(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	subjectID, err := strconv.Atoi(c.Param("prayer_subject_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer subject ID", "details": err.Error()})
		return
	}

	// Verify the prayer subject exists and user has permission
	var existingSubject models.PrayerSubject
	found, err := initializers.DB.From("prayer_subject").
		Select("*").
		Where(goqu.C("prayer_subject_id").Eq(subjectID)).
		ScanStruct(&existingSubject)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer subject", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer subject not found"})
		return
	}

	// Check permission - must be creator or admin
	if existingSubject.Created_By != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to view members of this prayer subject"})
		return
	}

	// Verify this is a family or group subject
	if existingSubject.Prayer_Subject_Type == "individual" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Individual prayer subjects cannot have members"})
		return
	}

	// Get all members with their details
	var members []models.PrayerSubjectMemberDetail
	err = initializers.DB.From("prayer_subject_membership").
		Select(
			goqu.I("prayer_subject_membership.prayer_subject_membership_id"),
			goqu.I("prayer_subject_membership.member_prayer_subject_id"),
			goqu.I("prayer_subject_membership.membership_role"),
			goqu.I("prayer_subject_membership.datetime_create"),
			goqu.I("prayer_subject_membership.created_by"),
			goqu.I("prayer_subject.prayer_subject_display_name").As("member_display_name"),
			goqu.I("prayer_subject.prayer_subject_type").As("member_type"),
			goqu.I("prayer_subject.photo_s3_key").As("member_photo_s3_key"),
			goqu.I("prayer_subject.user_profile_id").As("member_user_profile_id"),
			goqu.I("user_profile.phone_number").As("member_phone_number"),
		).
		Join(
			goqu.T("prayer_subject"),
			goqu.On(goqu.Ex{"prayer_subject_membership.member_prayer_subject_id": goqu.I("prayer_subject.prayer_subject_id")}),
		).
		LeftJoin(
			goqu.T("user_profile"),
			goqu.On(goqu.Ex{"prayer_subject.user_profile_id": goqu.I("user_profile.user_profile_id")}),
		).
		Where(goqu.C("group_prayer_subject_id").Eq(subjectID)).
		Order(goqu.I("prayer_subject_membership.datetime_create").Asc()).
		ScanStructs(&members)

	if err != nil {
		log.Println("Failed to fetch subject members:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch members", "details": err.Error()})
		return
	}

	if members == nil {
		members = []models.PrayerSubjectMemberDetail{}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Members retrieved successfully",
		"members": members,
	})
}

// GetSubjectParentGroups returns the family/group prayer subjects that an individual belongs to
func GetSubjectParentGroups(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	subjectID, err := strconv.Atoi(c.Param("prayer_subject_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer subject ID", "details": err.Error()})
		return
	}

	// Verify the prayer subject exists and user has permission
	var existingSubject models.PrayerSubject
	found, err := initializers.DB.From("prayer_subject").
		Select("*").
		Where(goqu.C("prayer_subject_id").Eq(subjectID)).
		ScanStruct(&existingSubject)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer subject", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer subject not found"})
		return
	}

	// Check permission - must be creator or admin
	if existingSubject.Created_By != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to view parent groups of this prayer subject"})
		return
	}

	// Get all parent groups/families with their details
	var parents []struct {
		PrayerSubjectMembershipID int       `db:"prayer_subject_membership_id" json:"prayerSubjectMembershipId"`
		GroupPrayerSubjectID      int       `db:"group_prayer_subject_id" json:"groupPrayerSubjectId"`
		MembershipRole            *string   `db:"membership_role" json:"membershipRole"`
		DatetimeCreate            time.Time `db:"datetime_create" json:"datetimeCreate"`
		CreatedBy                 int       `db:"created_by" json:"createdBy"`
		GroupDisplayName          string    `db:"group_display_name" json:"groupDisplayName"`
		GroupType                 string    `db:"group_type" json:"groupType"`
		GroupPhotoS3Key           *string   `db:"group_photo_s3_key" json:"groupPhotoS3Key"`
	}

	err = initializers.DB.From("prayer_subject_membership").
		Select(
			goqu.I("prayer_subject_membership.prayer_subject_membership_id"),
			goqu.I("prayer_subject_membership.group_prayer_subject_id"),
			goqu.I("prayer_subject_membership.membership_role"),
			goqu.I("prayer_subject_membership.datetime_create"),
			goqu.I("prayer_subject_membership.created_by"),
			goqu.I("prayer_subject.prayer_subject_display_name").As("group_display_name"),
			goqu.I("prayer_subject.prayer_subject_type").As("group_type"),
			goqu.I("prayer_subject.photo_s3_key").As("group_photo_s3_key"),
		).
		Join(
			goqu.T("prayer_subject"),
			goqu.On(goqu.Ex{"prayer_subject_membership.group_prayer_subject_id": goqu.I("prayer_subject.prayer_subject_id")}),
		).
		Where(goqu.C("member_prayer_subject_id").Eq(subjectID)).
		Order(goqu.I("prayer_subject_membership.datetime_create").Asc()).
		ScanStructs(&parents)

	if err != nil {
		log.Println("Failed to fetch parent groups:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch parent groups", "details": err.Error()})
		return
	}

	if parents == nil {
		parents = []struct {
			PrayerSubjectMembershipID int       `db:"prayer_subject_membership_id" json:"prayerSubjectMembershipId"`
			GroupPrayerSubjectID      int       `db:"group_prayer_subject_id" json:"groupPrayerSubjectId"`
			MembershipRole            *string   `db:"membership_role" json:"membershipRole"`
			DatetimeCreate            time.Time `db:"datetime_create" json:"datetimeCreate"`
			CreatedBy                 int       `db:"created_by" json:"createdBy"`
			GroupDisplayName          string    `db:"group_display_name" json:"groupDisplayName"`
			GroupType                 string    `db:"group_type" json:"groupType"`
			GroupPhotoS3Key           *string   `db:"group_photo_s3_key" json:"groupPhotoS3Key"`
		}{}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Parent groups retrieved successfully",
		"parents": parents,
	})
}

// AddMemberToSubject adds an individual prayer subject to a family/group prayer subject
func AddMemberToSubject(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	groupSubjectID, err := strconv.Atoi(c.Param("prayer_subject_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer subject ID", "details": err.Error()})
		return
	}

	// Verify the group prayer subject exists and user has permission
	var groupSubject models.PrayerSubject
	found, err := initializers.DB.From("prayer_subject").
		Select("*").
		Where(goqu.C("prayer_subject_id").Eq(groupSubjectID)).
		ScanStruct(&groupSubject)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer subject", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer subject not found"})
		return
	}

	// Check permission - must be creator or admin
	if groupSubject.Created_By != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to add members to this prayer subject"})
		return
	}

	// Verify this is a family or group subject
	if groupSubject.Prayer_Subject_Type == "individual" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot add members to an individual prayer subject. Only family or group subjects can have members."})
		return
	}

	var memberData models.PrayerSubjectMembershipCreate
	if err := c.BindJSON(&memberData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify the member prayer subject exists and belongs to this user
	var memberSubject models.PrayerSubject
	found, err = initializers.DB.From("prayer_subject").
		Select("*").
		Where(goqu.C("prayer_subject_id").Eq(memberData.Member_Prayer_Subject_ID)).
		ScanStruct(&memberSubject)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch member prayer subject", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Member prayer subject not found"})
		return
	}

	// Check ownership of member subject
	if memberSubject.Created_By != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to add this prayer subject as a member"})
		return
	}

	// Verify member is an individual (only individuals can be added to families/groups)
	if memberSubject.Prayer_Subject_Type != "individual" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only individual prayer subjects can be added as members to families or groups"})
		return
	}

	// Prevent adding a subject to itself
	if memberData.Member_Prayer_Subject_ID == groupSubjectID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot add a prayer subject as a member of itself"})
		return
	}

	// Check if membership already exists
	var existingCount int
	_, err = initializers.DB.From("prayer_subject_membership").
		Select(goqu.COUNT("prayer_subject_membership_id")).
		Where(
			goqu.C("member_prayer_subject_id").Eq(memberData.Member_Prayer_Subject_ID),
			goqu.C("group_prayer_subject_id").Eq(groupSubjectID),
		).
		ScanVal(&existingCount)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing membership", "details": err.Error()})
		return
	}

	if existingCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "This member is already part of this family/group"})
		return
	}

	// Create the membership
	newMembership := models.PrayerSubjectMembership{
		Member_Prayer_Subject_ID: memberData.Member_Prayer_Subject_ID,
		Group_Prayer_Subject_ID:  groupSubjectID,
		Membership_Role:          memberData.Membership_Role,
		Created_By:               currentUser.User_Profile_ID,
	}

	insert := initializers.DB.Insert("prayer_subject_membership").Rows(newMembership).Returning("prayer_subject_membership_id")

	var insertedID int
	_, err = insert.Executor().ScanVal(&insertedID)
	if err != nil {
		log.Println("Failed to create membership:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add member", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":                    "Member added successfully",
		"prayerSubjectMembershipId":  insertedID,
		"memberPrayerSubjectId":      memberData.Member_Prayer_Subject_ID,
		"groupPrayerSubjectId":       groupSubjectID,
	})
}

// RemoveMemberFromSubject removes an individual from a family/group prayer subject
func RemoveMemberFromSubject(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	groupSubjectID, err := strconv.Atoi(c.Param("prayer_subject_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer subject ID", "details": err.Error()})
		return
	}

	memberSubjectID, err := strconv.Atoi(c.Param("member_prayer_subject_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid member prayer subject ID", "details": err.Error()})
		return
	}

	// Verify the group prayer subject exists and user has permission
	var groupSubject models.PrayerSubject
	found, err := initializers.DB.From("prayer_subject").
		Select("*").
		Where(goqu.C("prayer_subject_id").Eq(groupSubjectID)).
		ScanStruct(&groupSubject)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer subject", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer subject not found"})
		return
	}

	// Check permission - must be creator or admin
	if groupSubject.Created_By != currentUser.User_Profile_ID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to remove members from this prayer subject"})
		return
	}

	// Check if membership exists
	var membershipID int
	found, err = initializers.DB.From("prayer_subject_membership").
		Select("prayer_subject_membership_id").
		Where(
			goqu.C("member_prayer_subject_id").Eq(memberSubjectID),
			goqu.C("group_prayer_subject_id").Eq(groupSubjectID),
		).
		ScanVal(&membershipID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check membership", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Membership not found"})
		return
	}

	// Delete the membership
	_, err = initializers.DB.Delete("prayer_subject_membership").
		Where(goqu.C("prayer_subject_membership_id").Eq(membershipID)).
		Executor().Exec()

	if err != nil {
		log.Println("Failed to delete membership:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove member", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Member removed successfully"})
}
