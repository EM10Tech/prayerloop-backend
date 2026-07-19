package controllers

import (
	"log"
	"net/http"
	"time"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"
	"github.com/PrayerLoop/services"

	"github.com/doug-martin/goqu/v9"
	"github.com/gin-gonic/gin"
)

// circleIDsRequest is the body shared by archive and restore.
//
// Bulk by design. There is no partial-failure handling anywhere in the mobile
// client, so N archives as N sequential requests is exactly the shape to
// avoid: a chooser that loops `for id := range selected { archive(id) }` can
// strand the user at "3 of 6 archived" with no way to express it. One request,
// one transaction, all-or-nothing.
type circleIDsRequest struct {
	GroupIDs []int `json:"groupIds" binding:"required"`
}

// archiveTargetRow is a circle the caller actually holds an active membership
// in, resolved before any mutation so the archive can be scoped to the caller's
// own rows and the notification model has the creator to hand.
type archiveTargetRow struct {
	GroupID   int    `db:"group_id"`
	GroupName string `db:"group_name"`
	CreatedBy int    `db:"created_by"`
}

// archivedCircleResponse is the list-only shape of an archived circle.
// Deliberately minimal: name and member count. Never prayers -- an archived
// circle has no entry point, so returning its contents would build a surface
// the product explicitly does not have.
// Both db and json tags are required: goqu maps result columns by the db tag
// (it cannot infer them from json), and dropping either silently 500s at
// runtime with "unable to find corresponding field to column".
type archivedCircleResponse struct {
	GroupID           int       `db:"group_id" json:"groupId"`
	GroupName         string    `db:"group_name" json:"groupName"`
	GroupDescription  string    `db:"group_description" json:"groupDescription"`
	ActiveMemberCount int       `db:"active_member_count" json:"activeMemberCount"`
	ArchivedAt        time.Time `db:"archived_at" json:"archivedAt"`
	IsCreator         bool      `db:"is_creator" json:"isCreator"`
}

// GetArchivedCircles returns the caller's archived memberships plus their
// current restore budget.
//
// Deliberately bypasses the is_active filter every other circle read applies.
// This is the ONLY thing that makes an archived circle visible again --
// GetUserGroups filters it out server-side the instant is_active goes false --
// so this endpoint is not optional alongside ArchiveCircles. Shipping archive
// without it makes archiving indistinguishable from deletion.
func GetArchivedCircles(c *gin.Context) {
	user := c.MustGet("currentUser").(models.UserProfile)

	var rows []archivedCircleResponse
	err := initializers.DB.From("user_group").
		Select(
			goqu.I("group_profile.group_profile_id").As("group_id"),
			goqu.I("group_profile.group_name").As("group_name"),
			goqu.COALESCE(goqu.I("group_profile.group_description"), "").As("group_description"),
			goqu.I("user_group.archived_at").As("archived_at"),
			goqu.L("(group_profile.created_by = ?)", user.User_Profile_ID).As("is_creator"),
			goqu.L(`(SELECT COUNT(*) FROM user_group m
			         WHERE m.group_profile_id = group_profile.group_profile_id
			           AND m.is_active = TRUE)`).As("active_member_count"),
		).
		Join(
			goqu.T("group_profile"),
			goqu.On(goqu.Ex{"group_profile.group_profile_id": goqu.I("user_group.group_profile_id")}),
		).
		Where(goqu.Ex{
			"user_group.user_profile_id": user.User_Profile_ID,
			"user_group.is_active":       false,
			// A circle deleted for everyone is not "archived" -- the user has no
			// restore to perform, so surfacing it would offer an action that
			// cannot succeed.
			"group_profile.is_active": true,
		}).
		Order(goqu.I("user_group.archived_at").Desc()).
		ScanStructs(&rows)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch archived circles", "details": err.Error()})
		return
	}

	if rows == nil {
		rows = []archivedCircleResponse{}
	}

	remaining, err := restoreBudgetRemaining(user.User_Profile_ID)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to compute restore budget", "details": err.Error()})
		return
	}

	isPremium, err := isPremiumUser(user.User_Profile_ID)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check subscription", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"circles": rows,
		// Premium users have no active-circle limit, so they never need to swap
		// and the budget is meaningless to them. Reported as unlimited rather
		// than as a number the client would render as a restriction.
		"restoreBudget": gin.H{
			"unlimited":  isPremium,
			"remaining":  remaining,
			"total":      RestoreBudget(),
			"windowDays": int(restoreBudgetWindow.Hours() / 24),
		},
	})
}

// ArchiveCircles flips is_active to false for the caller's OWN membership rows.
//
// Never touches any other member's row: archiving is per-membership, so the
// circle keeps running unchanged for everyone else and no other member's circle
// count moves. Archiving is free and unmetered -- only restoring is budgeted.
func ArchiveCircles(c *gin.Context) {
	user := c.MustGet("currentUser").(models.UserProfile)

	var req circleIDsRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.GroupIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "groupIds must not be empty"})
		return
	}

	// Resolve which of the requested circles the caller actually has an active
	// membership in, before mutating anything. Scoping to the caller's own rows
	// is what makes "own memberships only" true rather than aspirational.
	var targets []archiveTargetRow
	err := initializers.DB.From("user_group").
		Select(
			goqu.I("group_profile.group_profile_id").As("group_id"),
			goqu.I("group_profile.group_name").As("group_name"),
			goqu.I("group_profile.created_by").As("created_by"),
		).
		Join(
			goqu.T("group_profile"),
			goqu.On(goqu.Ex{"group_profile.group_profile_id": goqu.I("user_group.group_profile_id")}),
		).
		Where(goqu.Ex{
			"user_group.user_profile_id":  user.User_Profile_ID,
			"user_group.is_active":        true,
			"user_group.group_profile_id": req.GroupIDs,
			"group_profile.is_active":     true,
		}).
		ScanStructs(&targets)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve circles to archive", "details": err.Error()})
		return
	}

	if len(targets) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "No active memberships found for the given circles"})
		return
	}

	// All-or-nothing. The client cannot express a partial archive, so a failure
	// midway must leave the user exactly where they started rather than in a
	// state their UI has no way to describe.
	archivedAt := time.Now()
	txErr := initializers.DB.WithTx(func(tx *goqu.TxDatabase) error {
		ids := make([]int, 0, len(targets))
		for _, t := range targets {
			ids = append(ids, t.GroupID)
		}

		_, err := tx.Update("user_group").
			Set(goqu.Record{
				"is_active":   false,
				"archived_at": archivedAt,
				"updated_by":  user.User_Profile_ID,
			}).
			Where(goqu.Ex{
				"user_profile_id":  user.User_Profile_ID,
				"group_profile_id": ids,
				"is_active":        true,
			}).
			Executor().Exec()
		return err
	})
	if txErr != nil {
		log.Println(txErr)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to archive circles", "details": txErr.Error()})
		return
	}

	// Notifications fire on archive, never on a subscription lapse: a lapse
	// archives nothing, so there would be nothing true to announce, and
	// announcing a billing event to third parties is exactly what this design
	// rules out. Dispatched after commit -- a push about an archive that rolled
	// back would be a lie.
	go notifyOfArchivedCircles(user, targets)

	remaining, budgetErr := restoreBudgetRemaining(user.User_Profile_ID)
	if budgetErr != nil {
		// Non-fatal: the archive itself committed. Report it without the budget
		// rather than failing a request that already succeeded.
		log.Println(budgetErr)
	}

	archivedIDs := make([]int, 0, len(targets))
	for _, t := range targets {
		archivedIDs = append(archivedIDs, t.GroupID)
	}

	c.JSON(http.StatusOK, gin.H{
		"archivedGroupIds":       archivedIDs,
		"restoreBudgetRemaining": remaining,
		// Echoed so the client can show an undo affordance that expires in step
		// with the server's own window rather than guessing at it. Reads the
		// live value, so tuning RESTORE_UNDO_WINDOW_SECONDS moves the client's
		// countdown too instead of leaving it lying.
		"undoWindowSeconds": int(RestoreUndoWindow().Seconds()),
	})
}

// RestoreCircles flips is_active back to true for the caller's own archived
// memberships.
//
// Check order (each step exists for a reason, do not reorder casually):
//  1. premium            -> skip the budget entirely; they have no limit to rotate around
//  2. undo window        -> free, and not logged; this is what makes a fat-finger cheap
//  3. budget exhausted   -> 403 RESTORE_BUDGET_EXHAUSTED
//  4. circle limit       -> 403 CIRCLE_LIMIT_REACHED
//  5. restore + log the charged ones
//
// There is deliberately no client-settable "exempt" flag. The forced choice at
// the entry gate only ever archives -- it never restores -- so there is nothing
// for it to be exempt from, and a flag in the request body would just be a
// budget bypass any modified client could set.
func RestoreCircles(c *gin.Context) {
	user := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	var req circleIDsRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.GroupIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "groupIds must not be empty"})
		return
	}

	type archivedRow struct {
		GroupID    int        `db:"group_id"`
		ArchivedAt *time.Time `db:"archived_at"`
	}
	var rows []archivedRow
	err := initializers.DB.From("user_group").
		Select(
			goqu.I("user_group.group_profile_id").As("group_id"),
			goqu.I("user_group.archived_at").As("archived_at"),
		).
		Join(
			goqu.T("group_profile"),
			goqu.On(goqu.Ex{"group_profile.group_profile_id": goqu.I("user_group.group_profile_id")}),
		).
		Where(goqu.Ex{
			"user_group.user_profile_id":  user.User_Profile_ID,
			"user_group.is_active":        false,
			"user_group.group_profile_id": req.GroupIDs,
			"group_profile.is_active":     true,
		}).
		ScanStructs(&rows)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve circles to restore", "details": err.Error()})
		return
	}

	if len(rows) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "No archived memberships found for the given circles"})
		return
	}

	isPremium, err := isPremiumUser(user.User_Profile_ID)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check subscription", "details": err.Error()})
		return
	}

	// Split into free (undo window) and chargeable. Only the chargeable ones
	// count against the budget or get logged.
	chargeable := make([]int, 0, len(rows))
	free := make([]int, 0, len(rows))
	for _, r := range rows {
		if withinUndoWindow(r.ArchivedAt) {
			free = append(free, r.GroupID)
			continue
		}
		chargeable = append(chargeable, r.GroupID)
	}

	// Admins and premium users bypass both the budget and the limit.
	bypassLimits := isPremium || isAdmin

	if !bypassLimits && len(chargeable) > 0 {
		used, err := restoresUsedInWindow(user.User_Profile_ID)
		if err != nil {
			log.Println(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check restore budget", "details": err.Error()})
			return
		}
		if used+len(chargeable) > RestoreBudget() {
			c.JSON(http.StatusForbidden, gin.H{
				"error":     "You have used all of your circle restores for now. Upgrade to prayerloop Infinite to restore without limits.",
				"code":      "RESTORE_BUDGET_EXHAUSTED",
				"limit":     RestoreBudget(),
				"used":      used,
				"requested": len(chargeable),
			})
			return
		}
	}

	if !bypassLimits {
		// count + N <= limit, computed directly. isUnderCircleLimit answers a
		// different question ("may I add ONE more?") and returns count = 0 for
		// admins, so reusing it here would silently allow an over-limit bulk
		// restore.
		count, err := activeCircleCount(user.User_Profile_ID)
		if err != nil {
			log.Println(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check circle limit", "details": err.Error()})
			return
		}
		if count+len(rows) > FreeCircleLimit() {
			c.JSON(http.StatusForbidden, gin.H{
				"error":     "Restoring these circles would put you over the free prayer circle limit. Archive another circle or upgrade to prayerloop Infinite.",
				"code":      "CIRCLE_LIMIT_REACHED",
				"limit":     FreeCircleLimit(),
				"current":   count,
				"requested": len(rows),
			})
			return
		}
	}

	restoreIDs := make([]int, 0, len(rows))
	for _, r := range rows {
		restoreIDs = append(restoreIDs, r.GroupID)
	}

	txErr := initializers.DB.WithTx(func(tx *goqu.TxDatabase) error {
		if _, err := tx.Update("user_group").
			Set(goqu.Record{
				"is_active":   true,
				"archived_at": nil,
				"updated_by":  user.User_Profile_ID,
			}).
			Where(goqu.Ex{
				"user_profile_id":  user.User_Profile_ID,
				"group_profile_id": restoreIDs,
				"is_active":        false,
			}).
			Executor().Exec(); err != nil {
			return err
		}

		// Log ONLY the budget-charged restores. Undo-window restores and
		// premium/admin restores are deliberately absent from the ledger, which
		// is why circle_restore_event is a record of spend rather than of every
		// restore that ever happened.
		if bypassLimits || len(chargeable) == 0 {
			return nil
		}

		events := make([]goqu.Record, 0, len(chargeable))
		for _, id := range chargeable {
			events = append(events, goqu.Record{
				"user_profile_id":  user.User_Profile_ID,
				"group_profile_id": id,
			})
		}
		_, err := tx.Insert("circle_restore_event").Rows(events).Executor().Exec()
		return err
	})
	if txErr != nil {
		log.Println(txErr)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to restore circles", "details": txErr.Error()})
		return
	}

	remaining, budgetErr := restoreBudgetRemaining(user.User_Profile_ID)
	if budgetErr != nil {
		log.Println(budgetErr)
	}

	// Restoring notifies nobody. Returning to normal does not need announcing;
	// the roster marker simply un-greys. This halves the notification surface.
	c.JSON(http.StatusOK, gin.H{
		"restoredGroupIds":       restoreIDs,
		"freeRestoreGroupIds":    free,
		"restoreBudgetRemaining": remaining,
	})
}

// notifyOfArchivedCircles dispatches the archive notification model for a bulk
// archive:
//
//	member archives someone else's circle -> the circle creator, and only them
//	creator archives their own circle     -> every other active member
//	creator archives, sole member         -> nobody
//	restore                               -> nobody, ever
//
// Coalescing matters here: one archive action over six circles must not fan out
// six pushes to the same person if they happen to lead several of them. That is
// handled inside the services layer, which batches per recipient.
func notifyOfArchivedCircles(actor models.UserProfile, targets []archiveTargetRow) {
	actorName := actor.First_Name
	if actorName == "" {
		actorName = actor.Username
	}

	// Circles the actor did not create, grouped by creator so one leader who
	// happens to run several of them receives a single coalesced notification
	// rather than one per circle.
	byCreator := make(map[int][]string)

	for _, t := range targets {
		if t.CreatedBy == actor.User_Profile_ID {
			// The creator archived their own circle -> tell every other active
			// member, because there is nobody above them to tell. Sole-member
			// circles notify nobody; the service handles that case.
			services.NotifyMembersOfCreatorArchived(actor.User_Profile_ID, actorName, t.GroupID, t.GroupName)
			continue
		}
		byCreator[t.CreatedBy] = append(byCreator[t.CreatedBy], t.GroupName)
	}

	for creatorID, groupNames := range byCreator {
		services.NotifyCreatorOfMemberArchived(creatorID, actor.User_Profile_ID, actorName, groupNames)
	}
}
