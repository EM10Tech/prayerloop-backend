package controllers

import (
	"fmt"
	"log"
	"strings"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"

	"github.com/doug-martin/goqu/v9"
)

// EnsureCircleContactsForUser idempotently creates the contact graph for a user
// joining (or already in) a prayer circle:
//
//   a) The user's own group-type prayer_subject for this circle, linked to the
//      shared group_profile via prayer_subject_group_profile.
//   b) For every OTHER current member m of the circle:
//      - An individual-type prayer_subject owned by userID linked to m
//        (user_profile_id=m, link_status='linked', name from user_profile).
//      - The reciprocal: an individual-type prayer_subject owned by m linked
//        to userID.
//
// Note: by schema (migration 025), prayer_subject_group_profile only stores
// group-type subjects (the trigger rejects others). Individual contacts are
// not tracked there; circle membership is inferred at delete-time via the
// linked user_profile_id (see DeletePrayerSubject's guard).
//
// Idempotency: the unique_user_per_group constraint protects the group-type
// link; check-then-insert keyed on (created_by, user_profile_id) protects the
// individual prayer_subject rows.
func EnsureCircleContactsForUser(userID, groupProfileID int, groupName string) error {
	return initializers.DB.WithTx(func(tx *goqu.TxDatabase) error {
		// 1) User's own group-type prayer_subject for this circle.
		if err := ensureGroupContactForUser(tx, userID, groupProfileID, groupName); err != nil {
			return fmt.Errorf("ensure group contact for user %d / group %d: %w", userID, groupProfileID, err)
		}

		// 2) Individual contacts for every other current member.
		var otherMemberIDs []int
		err := tx.From("user_group").
			Select("user_profile_id").
			Where(
				goqu.C("group_profile_id").Eq(groupProfileID),
				goqu.C("is_active").IsTrue(),
				goqu.C("user_profile_id").Neq(userID),
			).
			ScanVals(&otherMemberIDs)
		if err != nil {
			return fmt.Errorf("fetch other members of group %d: %w", groupProfileID, err)
		}

		for _, m := range otherMemberIDs {
			if err := ensureIndividualContact(tx, userID, m); err != nil {
				return fmt.Errorf("ensure user %d -> member %d contact: %w", userID, m, err)
			}
			if err := ensureIndividualContact(tx, m, userID); err != nil {
				return fmt.Errorf("ensure member %d -> user %d contact: %w", m, userID, err)
			}
		}

		return nil
	})
}

// RemoveCircleContactsForLeavingUser tears down a user's group-type
// prayer_subject for one circle. Individual prayer_subject rows are kept so
// the user retains contacts they may still want to pray for; the delete guard
// will simply stop blocking them once the user_group row is gone (since the
// "shared circle" check no longer matches).
func RemoveCircleContactsForLeavingUser(userID, groupProfileID int) error {
	return initializers.DB.WithTx(func(tx *goqu.TxDatabase) error {
		var groupSubjectID int
		found, err := tx.From("prayer_subject_group_profile").
			Select(goqu.I("prayer_subject.prayer_subject_id")).
			Join(
				goqu.T("prayer_subject"),
				goqu.On(goqu.Ex{"prayer_subject_group_profile.prayer_subject_id": goqu.I("prayer_subject.prayer_subject_id")}),
			).
			Where(
				goqu.C("group_profile_id").Table("prayer_subject_group_profile").Eq(groupProfileID),
				goqu.C("prayer_subject_type").Table("prayer_subject").Eq("group"),
				goqu.C("created_by").Table("prayer_subject").Eq(userID),
			).
			ScanVal(&groupSubjectID)
		if err != nil {
			return fmt.Errorf("lookup group-type prayer_subject for user %d / group %d: %w", userID, groupProfileID, err)
		}
		if !found {
			return nil
		}

		// CASCADE on prayer_subject_group_profile drops the join row implicitly.
		if _, err := tx.Delete("prayer_subject").
			Where(goqu.C("prayer_subject_id").Eq(groupSubjectID)).
			Executor().Exec(); err != nil {
			return fmt.Errorf("delete group-type prayer_subject %d: %w", groupSubjectID, err)
		}

		return nil
	})
}

// RemoveAllCircleContacts deletes every member's group-type prayer_subject for
// a circle that is being torn down. CASCADE clears the join rows. Individual
// prayer_subject rows stay; the delete guard naturally stops blocking them
// once the underlying user_group rows are gone.
//
// Must be called BEFORE the group_profile row is deleted, while the join rows
// still exist to be queried.
func RemoveAllCircleContacts(groupProfileID int) error {
	return initializers.DB.WithTx(func(tx *goqu.TxDatabase) error {
		var groupSubjectIDs []int
		err := tx.From("prayer_subject_group_profile").
			Select(goqu.I("prayer_subject.prayer_subject_id")).
			Join(
				goqu.T("prayer_subject"),
				goqu.On(goqu.Ex{"prayer_subject_group_profile.prayer_subject_id": goqu.I("prayer_subject.prayer_subject_id")}),
			).
			Where(
				goqu.C("group_profile_id").Table("prayer_subject_group_profile").Eq(groupProfileID),
				goqu.C("prayer_subject_type").Table("prayer_subject").Eq("group"),
			).
			ScanVals(&groupSubjectIDs)
		if err != nil {
			return fmt.Errorf("collect group-type prayer_subjects for group %d: %w", groupProfileID, err)
		}

		if len(groupSubjectIDs) == 0 {
			return nil
		}

		if _, err := tx.Delete("prayer_subject").
			Where(goqu.C("prayer_subject_id").In(groupSubjectIDs)).
			Executor().Exec(); err != nil {
			return fmt.Errorf("delete group-type prayer_subjects for group %d: %w", groupProfileID, err)
		}

		return nil
	})
}

// ensureGroupContactForUser ensures the user has a group-type prayer_subject
// for this circle, plus the prayer_subject_group_profile join row.
//
// If the join row already exists for (group_profile_id, created_by) we treat
// it as already-set-up and exit. Otherwise, if CreateGroup already inserted
// a group-type prayer_subject for this user (recorded on
// group_profile.prayer_subject_id), we link THAT one rather than creating a
// duplicate. Only as a last resort do we insert a fresh prayer_subject.
func ensureGroupContactForUser(tx *goqu.TxDatabase, userID, groupProfileID int, groupName string) error {
	var existingSubjectID int
	found, err := tx.From("prayer_subject_group_profile").
		Select("prayer_subject_id").
		Where(
			goqu.C("group_profile_id").Eq(groupProfileID),
			goqu.C("created_by").Eq(userID),
		).
		ScanVal(&existingSubjectID)
	if err != nil {
		return fmt.Errorf("check existing join row: %w", err)
	}
	if found {
		return nil
	}

	subjectID, err := findExistingGroupSubject(tx, userID, groupProfileID, groupName)
	if err != nil {
		return err
	}

	if subjectID == 0 {
		subject := models.PrayerSubject{
			Prayer_Subject_Type:         "group",
			Prayer_Subject_Display_Name: groupName,
			Display_Sequence:            0,
			Link_Status:                 "unlinked",
			Created_By:                  userID,
			Updated_By:                  userID,
		}
		insert := tx.Insert("prayer_subject").Rows(subject).Returning("prayer_subject_id")
		if _, err := insert.Executor().ScanVal(&subjectID); err != nil {
			return fmt.Errorf("insert group-type prayer_subject: %w", err)
		}
	}

	if _, err := tx.Insert("prayer_subject_group_profile").Rows(goqu.Record{
		"prayer_subject_id": subjectID,
		"group_profile_id":  groupProfileID,
		"created_by":        userID,
	}).Executor().Exec(); err != nil {
		return fmt.Errorf("insert join row for group-type prayer_subject %d: %w", subjectID, err)
	}

	return nil
}

// findExistingGroupSubject looks for a group-type prayer_subject created by
// userID for this circle that ISN'T yet wired into prayer_subject_group_profile.
// We check group_profile.prayer_subject_id first (the canonical anchor written
// by CreateGroup), then fall back to a name + creator match. Returns 0 when
// nothing matches.
func findExistingGroupSubject(tx *goqu.TxDatabase, userID, groupProfileID int, groupName string) (int, error) {
	var anchorSubjectID int
	found, err := tx.From("group_profile").
		Select("prayer_subject_id").
		Where(
			goqu.C("group_profile_id").Eq(groupProfileID),
			goqu.C("prayer_subject_id").IsNotNull(),
		).
		ScanVal(&anchorSubjectID)
	if err != nil {
		return 0, fmt.Errorf("look up group_profile.prayer_subject_id: %w", err)
	}

	if found && anchorSubjectID > 0 {
		var subj struct {
			Prayer_Subject_ID   int    `db:"prayer_subject_id"`
			Prayer_Subject_Type string `db:"prayer_subject_type"`
			Created_By          int    `db:"created_by"`
		}
		ok, err := tx.From("prayer_subject").
			Select("prayer_subject_id", "prayer_subject_type", "created_by").
			Where(goqu.C("prayer_subject_id").Eq(anchorSubjectID)).
			ScanStruct(&subj)
		if err != nil {
			return 0, fmt.Errorf("verify anchor prayer_subject %d: %w", anchorSubjectID, err)
		}
		if ok && subj.Prayer_Subject_Type == "group" && subj.Created_By == userID {
			return anchorSubjectID, nil
		}
	}

	var fallbackID int
	found, err = tx.From("prayer_subject").
		Select("prayer_subject_id").
		Where(
			goqu.C("created_by").Eq(userID),
			goqu.C("prayer_subject_type").Eq("group"),
			goqu.C("prayer_subject_display_name").Eq(groupName),
		).
		Where(goqu.L(
			"NOT EXISTS (SELECT 1 FROM prayer_subject_group_profile psgp WHERE psgp.prayer_subject_id = prayer_subject.prayer_subject_id)",
		)).
		ScanVal(&fallbackID)
	if err != nil {
		return 0, fmt.Errorf("fallback lookup for unlinked group prayer_subject: %w", err)
	}
	if found {
		return fallbackID, nil
	}

	return 0, nil
}

// ensureIndividualContact ensures ownerID has an individual-type prayer_subject
// linked to subjectUserID. Keyed on (created_by, user_profile_id, type) so the
// same pair across multiple circles reuses one row.
//
// We do NOT write into prayer_subject_group_profile for individuals — that
// table's trigger only accepts group-type subjects. Circle-membership is
// inferred at delete-time via user_profile_id + user_group.
func ensureIndividualContact(tx *goqu.TxDatabase, ownerID, subjectUserID int) error {
	var subjectID int
	found, err := tx.From("prayer_subject").
		Select("prayer_subject_id").
		Where(
			goqu.C("created_by").Eq(ownerID),
			goqu.C("user_profile_id").Eq(subjectUserID),
			goqu.C("prayer_subject_type").Eq("individual"),
		).
		ScanVal(&subjectID)
	if err != nil {
		return fmt.Errorf("lookup individual prayer_subject (owner=%d, subject=%d): %w", ownerID, subjectUserID, err)
	}
	if found {
		return nil
	}

	displayName, err := lookupUserDisplayName(tx, subjectUserID)
	if err != nil {
		return fmt.Errorf("lookup display name for user %d: %w", subjectUserID, err)
	}

	subject := models.PrayerSubject{
		Prayer_Subject_Type:         "individual",
		Prayer_Subject_Display_Name: displayName,
		Display_Sequence:            0,
		User_Profile_ID:             &subjectUserID,
		Use_Linked_User_Photo:       true,
		Link_Status:                 "linked",
		Created_By:                  ownerID,
		Updated_By:                  ownerID,
	}
	insert := tx.Insert("prayer_subject").Rows(subject).Returning("prayer_subject_id")
	if _, err := insert.Executor().ScanVal(&subjectID); err != nil {
		return fmt.Errorf("insert individual prayer_subject (owner=%d, subject=%d): %w", ownerID, subjectUserID, err)
	}

	return nil
}

// lookupUserDisplayName builds a human-readable display name from user_profile,
// falling back to the username if first/last are blank.
func lookupUserDisplayName(tx *goqu.TxDatabase, userID int) (string, error) {
	var u struct {
		First_Name string `db:"first_name"`
		Last_Name  string `db:"last_name"`
		Username   string `db:"username"`
	}
	found, err := tx.From("user_profile").
		Select("first_name", "last_name", "username").
		Where(goqu.C("user_profile_id").Eq(userID)).
		ScanStruct(&u)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("user_profile %d not found", userID)
	}
	name := strings.TrimSpace(u.First_Name + " " + u.Last_Name)
	if name == "" {
		name = u.Username
	}
	if name == "" {
		name = fmt.Sprintf("User %d", userID)
	}
	return name, nil
}

// logCircleContactErr matches the existing non-fatal pattern in CreateGroup —
// a partial failure in contact wiring should be logged but not fail the parent
// request.
func logCircleContactErr(op string, err error) {
	if err != nil {
		log.Printf("circle contacts: %s failed: %v", op, err)
	}
}
