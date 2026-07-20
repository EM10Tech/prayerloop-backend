package controllers

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"
	"github.com/PrayerLoop/services"
	"github.com/doug-martin/goqu/v9"
)

// getModeratorIDsForPrayer returns user IDs who can moderate comments on this prayer
// (prayer creator + linked subject user if exists)
func getModeratorIDsForPrayer(prayerID int) ([]int, error) {
	var prayer models.Prayer
	query := initializers.DB.From("prayer").
		Where(goqu.C("prayer_id").Eq(prayerID))

	_, err := query.ScanStruct(&prayer)
	if err != nil {
		return nil, err
	}

	moderatorIDs := []int{prayer.Created_By}

	// Check if prayer has a linked subject
	if prayer.Prayer_Subject_ID != nil {
		var subjectUserID *int
		subjectQuery := initializers.DB.From("prayer_subject").
			Select("user_profile_id").
			Where(goqu.C("prayer_subject_id").Eq(*prayer.Prayer_Subject_ID))

		_, _ = subjectQuery.ScanVal(&subjectUserID)
		if subjectUserID != nil {
			moderatorIDs = append(moderatorIDs, *subjectUserID)
		}
	}

	return moderatorIDs, nil
}

// isModerator checks if userID is a moderator for the given prayer
func isModerator(userID int, moderatorIDs []int) bool {
	for _, id := range moderatorIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// GetPrayerComments retrieves all comments for a prayer with privacy filtering
func GetPrayerComments(c *gin.Context) {
	userID := c.MustGet("currentUser").(models.UserProfile).User_Profile_ID

	prayerID, err := strconv.Atoi(c.Param("prayer_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer ID", "details": err.Error()})
		return
	}

	// Check prayer_access: user must have access to prayer
	hasAccess, err := userHasPrayerAccess(userID, prayerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check prayer access", "details": err.Error()})
		return
	}
	if !hasAccess {
		c.JSON(http.StatusForbidden, gin.H{"error": "No access to this prayer"})
		return
	}

	// Get moderator IDs (prayer creator + linked subject)
	moderatorIDs, err := getModeratorIDsForPrayer(prayerID)
	if err != nil {
		log.Printf("Failed to get moderator IDs: %v", err)
		moderatorIDs = []int{} // Continue with empty moderator list
	}

	// Query comments with privacy filter
	query := initializers.DB.From("prayer_comment").
		Select(
			goqu.I("prayer_comment.comment_id"),
			goqu.I("prayer_comment.prayer_id"),
			goqu.I("prayer_comment.user_profile_id"),
			goqu.I("prayer_comment.comment_text"),
			goqu.I("prayer_comment.is_private"),
			goqu.I("prayer_comment.is_hidden"),
			goqu.I("prayer_comment.datetime_create"),
			goqu.I("prayer_comment.datetime_update"),
			goqu.I("user_profile.first_name").As("commenter_name"),
		).
		Join(
			goqu.T("user_profile"),
			goqu.On(goqu.I("prayer_comment.user_profile_id").Eq(goqu.I("user_profile.user_profile_id"))),
		).
		Where(goqu.I("prayer_comment.prayer_id").Eq(prayerID)).
		Where(goqu.I("prayer_comment.is_hidden").Eq(false)).
		Order(goqu.I("prayer_comment.datetime_create").Asc())

	// Apply privacy filter: show public comments OR own comments OR private comments if user is moderator
	if len(moderatorIDs) > 0 && isModerator(userID, moderatorIDs) {
		// Moderators see all non-hidden comments (both public and private)
		// No additional WHERE clause needed
	} else {
		// Non-moderators see: public comments OR their own private comments
		query = query.Where(
			goqu.Or(
				goqu.I("prayer_comment.is_private").Eq(false),
				goqu.I("prayer_comment.user_profile_id").Eq(userID),
			),
		)
	}

	var comments []models.CommentWithUser
	err = query.ScanStructs(&comments)
	if err != nil {
		log.Printf("Failed to fetch comments: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch comments"})
		return
	}

	// Return empty array if no comments found
	if comments == nil {
		comments = []models.CommentWithUser{}
	}

	c.JSON(http.StatusOK, gin.H{
		"comments": comments,
	})
}

// CreateComment creates a new comment on a prayer
func CreateComment(c *gin.Context) {
	userID := c.MustGet("currentUser").(models.UserProfile).User_Profile_ID

	prayerID, err := strconv.Atoi(c.Param("prayer_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer ID", "details": err.Error()})
		return
	}

	var commentData struct {
		CommentText string `json:"commentText" binding:"required"`
		IsPrivate   *bool  `json:"isPrivate"`
	}

	if err := c.BindJSON(&commentData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "details": err.Error()})
		return
	}

	// Validate comment_text length (max 500 characters)
	if len(commentData.CommentText) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Comment text exceeds maximum length of 500 characters"})
		return
	}

	if commentData.CommentText == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Comment text is required"})
		return
	}

	// Check prayer_access permission
	hasAccess, err := userHasPrayerAccess(userID, prayerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check prayer access", "details": err.Error()})
		return
	}
	if !hasAccess {
		c.JSON(http.StatusForbidden, gin.H{"error": "No access to this prayer"})
		return
	}

	// Check prayer is not hidden/archived (prevent commenting on deleted prayers)
	var prayer models.Prayer
	prayerFound, err := initializers.DB.From("prayer").
		Where(goqu.C("prayer_id").Eq(prayerID)).
		ScanStruct(&prayer)

	if err != nil || !prayerFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer not found"})
		return
	}

	if prayer.Deleted {
		c.JSON(http.StatusForbidden, gin.H{"error": "Cannot comment on a deleted prayer"})
		return
	}

	// Default is_private to false if not provided
	isPrivate := false
	if commentData.IsPrivate != nil {
		isPrivate = *commentData.IsPrivate
	}

	// Insert into prayer_comment table
	commentInsert := models.Comment{
		Prayer_ID:       prayerID,
		User_Profile_ID: userID,
		Comment_Text:    commentData.CommentText,
		Is_Private:      isPrivate,
		Is_Hidden:       false,
		Created_By:      userID,
		Updated_By:      userID,
	}

	insert := initializers.DB.Insert("prayer_comment").
		Rows(commentInsert).
		Returning("comment_id", "datetime_create", "datetime_update")

	var insertedComment struct {
		Comment_ID      int    `db:"comment_id"`
		Datetime_Create string `db:"datetime_create"`
		Datetime_Update string `db:"datetime_update"`
	}

	_, err = insert.Executor().ScanStruct(&insertedComment)
	if err != nil {
		log.Printf("Failed to create comment: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create comment", "details": err.Error()})
		return
	}

	// Get commenter name for response
	var commenterName string
	_, _ = initializers.DB.From("user_profile").
		Select("first_name").
		Where(goqu.C("user_profile_id").Eq(userID)).
		ScanVal(&commenterName)

	// Trigger notifications after successful comment creation
	services.NotifyUsersOfNewComment(prayerID, insertedComment.Comment_ID, userID)

	c.JSON(http.StatusCreated, gin.H{
		"message": "Comment created successfully",
		"comment": gin.H{
			"commentId":       insertedComment.Comment_ID,
			"prayerId":        prayerID,
			"userProfileId":   userID,
			"commentText":     commentData.CommentText,
			"isPrivate":       isPrivate,
			"isHidden":        false,
			"datetimeCreate":  insertedComment.Datetime_Create,
			"datetimeUpdate":  insertedComment.Datetime_Update,
			"commenterName":   commenterName,
		},
	})
}

// UpdateComment updates an existing comment (user must own the comment)
func UpdateComment(c *gin.Context) {
	userID := c.MustGet("currentUser").(models.UserProfile).User_Profile_ID

	commentID, err := strconv.Atoi(c.Param("comment_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid comment ID", "details": err.Error()})
		return
	}

	var updateData struct {
		CommentText string `json:"commentText" binding:"required"`
	}

	if err := c.BindJSON(&updateData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "details": err.Error()})
		return
	}

	// Validate comment_text length (max 500 characters)
	if len(updateData.CommentText) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Comment text exceeds maximum length of 500 characters"})
		return
	}

	// Check if comment exists and user owns it
	var existingComment models.Comment
	commentFound, err := initializers.DB.From("prayer_comment").
		Where(goqu.C("comment_id").Eq(commentID)).
		ScanStruct(&existingComment)

	if err != nil || !commentFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "Comment not found"})
		return
	}

	// User must own the comment
	if existingComment.User_Profile_ID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You can only edit your own comments"})
		return
	}

	// Update comment_text and datetime_update
	updateQuery := initializers.DB.Update("prayer_comment").
		Set(goqu.Record{
			"comment_text":    updateData.CommentText,
			"updated_by":      userID,
			"datetime_update": goqu.L("NOW()"),
		}).
		Where(goqu.C("comment_id").Eq(commentID))

	result, err := updateQuery.Executor().Exec()
	if err != nil {
		log.Printf("Failed to update comment: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update comment", "details": err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No rows were updated"})
		return
	}

	// Fetch updated comment to return
	var updatedComment models.Comment
	_, err = initializers.DB.From("prayer_comment").
		Select(
			"comment_id",
			"prayer_id",
			"user_profile_id",
			"comment_text",
			"is_private",
			"is_hidden",
			"datetime_create",
			"datetime_update",
		).
		Where(goqu.C("comment_id").Eq(commentID)).
		ScanStruct(&updatedComment)

	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "Comment updated successfully"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Comment updated successfully",
		"comment": updatedComment,
	})
}

// DeleteComment deletes a comment (user must own comment OR be moderator)
func DeleteComment(c *gin.Context) {
	userID := c.MustGet("currentUser").(models.UserProfile).User_Profile_ID

	commentID, err := strconv.Atoi(c.Param("comment_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid comment ID", "details": err.Error()})
		return
	}

	// Check if comment exists
	var existingComment models.Comment
	commentFound, err := initializers.DB.From("prayer_comment").
		Where(goqu.C("comment_id").Eq(commentID)).
		ScanStruct(&existingComment)

	if err != nil || !commentFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "Comment not found"})
		return
	}

	// Get moderator IDs for this prayer
	moderatorIDs, err := getModeratorIDsForPrayer(existingComment.Prayer_ID)
	if err != nil {
		log.Printf("Failed to get moderator IDs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check permissions"})
		return
	}

	// User must own comment OR be moderator
	canDelete := existingComment.User_Profile_ID == userID || isModerator(userID, moderatorIDs)
	if !canDelete {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to delete this comment"})
		return
	}

	// Hard delete the comment
	deleteQuery := initializers.DB.Delete("prayer_comment").
		Where(goqu.C("comment_id").Eq(commentID))

	result, err := deleteQuery.Executor().Exec()
	if err != nil {
		log.Printf("Failed to delete comment: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete comment", "details": err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No rows were deleted"})
		return
	}

	c.JSON(http.StatusNoContent, nil)
}

// HideComment hides a comment (soft delete, only moderators can hide)
func HideComment(c *gin.Context) {
	userID := c.MustGet("currentUser").(models.UserProfile).User_Profile_ID

	commentID, err := strconv.Atoi(c.Param("comment_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid comment ID", "details": err.Error()})
		return
	}

	// Check if comment exists
	var existingComment models.Comment
	commentFound, err := initializers.DB.From("prayer_comment").
		Where(goqu.C("comment_id").Eq(commentID)).
		ScanStruct(&existingComment)

	if err != nil || !commentFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "Comment not found"})
		return
	}

	// Get moderator IDs for this prayer
	moderatorIDs, err := getModeratorIDsForPrayer(existingComment.Prayer_ID)
	if err != nil {
		log.Printf("Failed to get moderator IDs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check permissions"})
		return
	}

	// Only moderators can hide comments
	if !isModerator(userID, moderatorIDs) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only moderators can hide comments"})
		return
	}

	// Set is_hidden = true (soft delete)
	updateQuery := initializers.DB.Update("prayer_comment").
		Set(goqu.Record{
			"is_hidden":       true,
			"updated_by":      userID,
			"datetime_update": goqu.L("NOW()"),
		}).
		Where(goqu.C("comment_id").Eq(commentID))

	result, err := updateQuery.Executor().Exec()
	if err != nil {
		log.Printf("Failed to hide comment: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hide comment", "details": err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No rows were updated"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Comment hidden successfully"})
}

// ToggleCommentPrivacy toggles the is_private flag on a comment (user must own comment)
func ToggleCommentPrivacy(c *gin.Context) {
	userID := c.MustGet("currentUser").(models.UserProfile).User_Profile_ID

	commentID, err := strconv.Atoi(c.Param("comment_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid comment ID", "details": err.Error()})
		return
	}

	// Check if comment exists and user owns it
	var existingComment models.Comment
	commentFound, err := initializers.DB.From("prayer_comment").
		Where(goqu.C("comment_id").Eq(commentID)).
		ScanStruct(&existingComment)

	if err != nil || !commentFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "Comment not found"})
		return
	}

	// User must own the comment
	if existingComment.User_Profile_ID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You can only change privacy on your own comments"})
		return
	}

	// Toggle is_private
	newPrivacy := !existingComment.Is_Private

	updateQuery := initializers.DB.Update("prayer_comment").
		Set(goqu.Record{
			"is_private":      newPrivacy,
			"updated_by":      userID,
			"datetime_update": goqu.L("NOW()"),
		}).
		Where(goqu.C("comment_id").Eq(commentID))

	result, err := updateQuery.Executor().Exec()
	if err != nil {
		log.Printf("Failed to toggle comment privacy: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to toggle comment privacy", "details": err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No rows were updated"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "Comment privacy toggled successfully",
		"isPrivate": newPrivacy,
	})
}
