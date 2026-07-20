package controllers

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"
	"github.com/doug-martin/goqu/v9"
)

// RecordPrayer records that a user prayed for a specific prayer
// POST /prayers/:prayer_id/analytics
func RecordPrayer(c *gin.Context) {
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

	// Check if prayer_analytics record exists
	var existingAnalytics models.PrayerAnalytics
	analyticsFound, err := initializers.DB.From("prayer_analytics").
		Where(goqu.C("prayer_id").Eq(prayerID)).
		ScanStruct(&existingAnalytics)

	if err != nil {
		log.Printf("Failed to fetch prayer analytics: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer analytics"})
		return
	}

	if analyticsFound {
		// Check 5-minute cooldown: if same user prayed within last 5 minutes, return existing stats
		if existingAnalytics.Last_Prayed_By != nil && *existingAnalytics.Last_Prayed_By == userID &&
			existingAnalytics.Datetime_Last_Prayed != nil {
			timeSinceLastPrayer := time.Since(*existingAnalytics.Datetime_Last_Prayed)
			if timeSinceLastPrayer < 5*time.Minute {
				// Within cooldown - return existing stats without updating
				c.JSON(http.StatusOK, gin.H{
					"message": "Prayer recorded",
					"analytics": models.PrayerAnalyticsResponse{
						TotalPrayers:   existingAnalytics.Total_Prayers,
						NumUniqueUsers: existingAnalytics.Num_Unique_Users,
					},
				})
				return
			}
		}

		// Update existing record
		updateRecord := goqu.Record{
			"total_prayers":        goqu.L("total_prayers + 1"),
			"datetime_last_prayed": goqu.L("NOW()"),
			"last_prayed_by":       userID,
		}

		// Increment num_unique_users if this is a different user than last time
		if existingAnalytics.Last_Prayed_By == nil || *existingAnalytics.Last_Prayed_By != userID {
			updateRecord["num_unique_users"] = goqu.L("num_unique_users + 1")
		}

		updateQuery := initializers.DB.Update("prayer_analytics").
			Set(updateRecord).
			Where(goqu.C("prayer_id").Eq(prayerID)).
			Returning("total_prayers", "num_unique_users")

		var updatedAnalytics struct {
			Total_Prayers    int `db:"total_prayers"`
			Num_Unique_Users int `db:"num_unique_users"`
		}

		_, err = updateQuery.Executor().ScanStruct(&updatedAnalytics)
		if err != nil {
			log.Printf("Failed to update prayer analytics: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update prayer analytics"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message": "Prayer recorded",
			"analytics": models.PrayerAnalyticsResponse{
				TotalPrayers:   updatedAnalytics.Total_Prayers,
				NumUniqueUsers: updatedAnalytics.Num_Unique_Users,
			},
		})
	} else {
		// Create new record (omit prayer_analytics_id to let SERIAL auto-generate)
		now := time.Now()
		newAnalyticsRecord := goqu.Record{
			"prayer_id":            prayerID,
			"total_prayers":        1,
			"datetime_last_prayed": now,
			"last_prayed_by":       userID,
			"num_unique_users":     1,
			"num_shares":           0,
		}

		insert := initializers.DB.Insert("prayer_analytics").
			Rows(newAnalyticsRecord).
			Returning("total_prayers", "num_unique_users")

		var insertedAnalytics struct {
			Total_Prayers    int `db:"total_prayers"`
			Num_Unique_Users int `db:"num_unique_users"`
		}

		_, err = insert.Executor().ScanStruct(&insertedAnalytics)
		if err != nil {
			log.Printf("Failed to create prayer analytics: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create prayer analytics"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message": "Prayer recorded",
			"analytics": models.PrayerAnalyticsResponse{
				TotalPrayers:   insertedAnalytics.Total_Prayers,
				NumUniqueUsers: insertedAnalytics.Num_Unique_Users,
			},
		})
	}
}

// GetPrayerAnalytics retrieves aggregate analytics for a prayer
// GET /prayers/:prayer_id/analytics
func GetPrayerAnalytics(c *gin.Context) {
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

	// Query prayer_analytics for this prayer_id
	var analytics models.PrayerAnalytics
	analyticsFound, err := initializers.DB.From("prayer_analytics").
		Select("total_prayers", "num_unique_users").
		Where(goqu.C("prayer_id").Eq(prayerID)).
		ScanStruct(&analytics)

	if err != nil {
		log.Printf("Failed to fetch prayer analytics: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer analytics"})
		return
	}

	if !analyticsFound {
		// No analytics record exists yet - return zeros
		c.JSON(http.StatusOK, gin.H{
			"analytics": models.PrayerAnalyticsResponse{
				TotalPrayers:   0,
				NumUniqueUsers: 0,
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"analytics": models.PrayerAnalyticsResponse{
			TotalPrayers:   analytics.Total_Prayers,
			NumUniqueUsers: analytics.Num_Unique_Users,
		},
	})
}
