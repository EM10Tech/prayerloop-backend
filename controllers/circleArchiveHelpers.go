package controllers

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/PrayerLoop/initializers"

	"github.com/doug-martin/goqu/v9"
)

// defaultRestoreBudget applies when RESTORE_BUDGET is unset or invalid.
const defaultRestoreBudget = 3

// restoreBudgetWindow is the rolling period the budget is counted over.
//
// Rolling rather than calendar-month, deliberately: a calendar month has a
// cliff where three restores burn on the 31st and reset on the 1st, which is
// both gameable and confusing to explain when it bites. Rolling needs no extra
// column -- it is a bound on circle_restore_event.restored_at.
const restoreBudgetWindow = 30 * 24 * time.Hour

// defaultRestoreUndoWindow applies when RESTORE_UNDO_WINDOW_SECONDS is unset or
// invalid.
//
// This window is the ONLY thing user_group.archived_at exists for. Without it
// the server cannot tell a ten-second-old fat-finger from a three-week-old
// archive, so every restore would charge the budget and mis-tapping one row in
// the bulk archive chooser would permanently cost a restore.
const defaultRestoreUndoWindow = 5 * time.Minute

// RestoreUndoWindow is how long after archiving a restore is free and unlogged.
//
// Env-tunable for the same reason FreeCircleLimit and RestoreBudget are, plus
// one specific to it: at the default five minutes, every hand-driven test
// (Bruno, curl) archives and restores well inside the window, so the budget
// never charges and the whole metering path looks broken when it is in fact
// working. Setting RESTORE_UNDO_WINDOW_SECONDS=0 disables the window and makes
// every restore chargeable, which is the only practical way to exercise the
// budget without hand-editing archived_at in SQL.
//
// 0 is a legitimate value and must not fall back to the default -- that is the
// entire point of the knob.
func RestoreUndoWindow() time.Duration {
	raw := os.Getenv("RESTORE_UNDO_WINDOW_SECONDS")
	if raw == "" {
		return defaultRestoreUndoWindow
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		log.Printf("invalid RESTORE_UNDO_WINDOW_SECONDS %q, using %s", raw, defaultRestoreUndoWindow)
		return defaultRestoreUndoWindow
	}

	return time.Duration(seconds) * time.Second
}

// RestoreBudget is the number of budget-charged restores a free-tier user gets
// per rolling 30 days.
//
// Read from the environment at each call for the same reason FreeCircleLimit
// is (package vars initialize before LoadEnvVariables runs). Raising it is the
// kill switch if the budget turns out to be tuned wrong in production -- the
// alternative would be a redeploy.
func RestoreBudget() int {
	raw := os.Getenv("RESTORE_BUDGET")
	if raw == "" {
		return defaultRestoreBudget
	}

	budget, err := strconv.Atoi(raw)
	if err != nil || budget < 0 {
		log.Printf("invalid RESTORE_BUDGET %q, using %d", raw, defaultRestoreBudget)
		return defaultRestoreBudget
	}

	return budget
}

// CircleEntryGateEnabled reports whether clients should arm the over-limit
// entry gate -- the forced archive-or-subscribe choice a free user meets when
// their active circle count exceeds the limit.
//
// This exists because the gate is the riskiest behavior in the archiving
// feature: it blocks people from opening circles they already have. Every other
// limit here is enforced server-side, but the gate is necessarily a client
// decision (the server cannot refuse a navigation), so without a server-driven
// switch the only way to disarm a misfiring gate would be a mobile release --
// and an OTA reaches every build on the same SDK version at once.
//
// Raising FREE_CIRCLE_LIMIT alone does NOT disarm the gate for someone already
// over the new limit, which is why this is a separate switch rather than a
// derived one. Defaults to enabled; set CIRCLE_ENTRY_GATE_ENABLED=false to kill
// it without a deploy.
func CircleEntryGateEnabled() bool {
	raw := os.Getenv("CIRCLE_ENTRY_GATE_ENABLED")
	if raw == "" {
		return true
	}

	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		log.Printf("invalid CIRCLE_ENTRY_GATE_ENABLED %q, leaving the gate enabled", raw)
		return true
	}

	return enabled
}

// restoresUsedInWindow counts budget-charged restores for userID inside the
// rolling window.
//
// Only charged restores are ever written to circle_restore_event -- premium
// restores and undo-window restores are deliberately not logged -- so this is
// a count of spend, not of every restore that ever happened.
func restoresUsedInWindow(userID int) (int, error) {
	var count int
	_, err := initializers.DB.From("circle_restore_event").
		Select(goqu.COUNT("*")).
		Where(
			goqu.C("user_profile_id").Eq(userID),
			goqu.C("restored_at").Gt(time.Now().Add(-restoreBudgetWindow)),
		).
		ScanVal(&count)
	if err != nil {
		return 0, fmt.Errorf("count restores in window for user %d: %w", userID, err)
	}

	return count, nil
}

// restoreBudgetRemaining reports how many charged restores userID has left.
// Never negative: a budget lowered by ops below what someone already spent
// should read as 0, not as a negative number the client would have to render.
func restoreBudgetRemaining(userID int) (int, error) {
	used, err := restoresUsedInWindow(userID)
	if err != nil {
		return 0, err
	}

	remaining := RestoreBudget() - used
	if remaining < 0 {
		return 0, nil
	}

	return remaining, nil
}

// withinUndoWindow reports whether archivedAt is recent enough that restoring
// is free. A NULL archived_at (nil) is not within the window -- it means the
// row was never archived, so there is nothing to undo.
//
// A zero-length window (RESTORE_UNDO_WINDOW_SECONDS=0) makes every restore
// chargeable: time.Since is always > 0, so the strict < can never hold.
func withinUndoWindow(archivedAt *time.Time) bool {
	if archivedAt == nil {
		return false
	}

	return time.Since(*archivedAt) < RestoreUndoWindow()
}
