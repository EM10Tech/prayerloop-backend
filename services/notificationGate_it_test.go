package services

// Opt-in integration test for the archive notification gate
// (CIRCLE_ARCHIVE_DESIGN.md §D.2). Exercises canReceiveNotificationForPrayer
// and IsActiveCircleMember against a real Postgres, because the interesting
// part of both is the SQL semantics -- a sqlmock test would only assert that
// a hardcoded row round-trips, which proves nothing about the query.
//
// Skipped unless NOTIF_GATE_IT_DSN is set, e.g.:
//
//	NOTIF_GATE_IT_DSN="host=/var/run/postgresql dbname=prayerloop_gate_test sslmode=disable" \
//	  go test ./services/ -run TestNotificationGateIntegration -count=1 -v
//
// The database must already have the schema (build one with
// `psql -d <db> -f database_init.sql` from prayerloop-psql). Each subtest
// builds its own fixtures and rolls nothing back, so point this at a scratch
// database, not a real one.

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/PrayerLoop/initializers"
	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/postgres"
	_ "github.com/lib/pq"
)

// runID namespaces every fixture this run creates. user_profile.username is
// UNIQUE, so without it a second run against the same database dies on a
// duplicate-key error during fixture setup -- which reads like a real test
// failure and cost me a confusing minute the first time.
var runID = fmt.Sprintf("%d", time.Now().UnixNano())

// gateFixture is one self-contained scenario: a prayer, optionally shared to a
// circle, and a recipient whose membership state we control.
type gateFixture struct {
	prayerID    int
	groupID     int
	recipientID int
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("fixture exec failed: %v\nquery: %s", err, query)
	}
}

func mustScanInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var id int
	if err := db.QueryRow(query, args...).Scan(&id); err != nil {
		t.Fatalf("fixture scan failed: %v\nquery: %s", err, query)
	}
	return id
}

// newGateFixture creates a fresh user, circle, and prayer. The prayer is always
// authored by a separate author account so that the recipient's access is
// controlled purely by the grants the caller adds.
func newGateFixture(t *testing.T, db *sql.DB, rawLabel string) gateFixture {
	t.Helper()
	label := rawLabel + "_" + runID

	authorID := mustScanInt(t, db,
		`INSERT INTO user_profile (username, first_name, last_name, created_by, updated_by)
		 VALUES ($1, 'Author', 'Test', 1, 1) RETURNING user_profile_id`,
		"gate_author_"+label)

	recipientID := mustScanInt(t, db,
		`INSERT INTO user_profile (username, first_name, last_name, created_by, updated_by)
		 VALUES ($1, 'Recipient', 'Test', 1, 1) RETURNING user_profile_id`,
		"gate_recipient_"+label)

	groupID := mustScanInt(t, db,
		`INSERT INTO group_profile (group_name, is_active, created_by, updated_by)
		 VALUES ($1, TRUE, $2, $2) RETURNING group_profile_id`,
		"gate_circle_"+label, authorID)

	subjectID := mustScanInt(t, db,
		`INSERT INTO prayer_subject (prayer_subject_type, prayer_subject_display_name, created_by, updated_by)
		 VALUES ('individual', $1, $2, $2) RETURNING prayer_subject_id`,
		"gate_subject_"+label, authorID)

	prayerID := mustScanInt(t, db,
		`INSERT INTO prayer (prayer_type, title, prayer_subject_id, created_by, updated_by)
		 VALUES ('request', $1, $2, $3, $3) RETURNING prayer_id`,
		"gate_prayer_"+label, subjectID, authorID)

	return gateFixture{prayerID: prayerID, groupID: groupID, recipientID: recipientID}
}

func (f gateFixture) shareToCircle(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db,
		`INSERT INTO prayer_access (prayer_id, access_type, access_type_id, created_by, updated_by)
		 VALUES ($1, 'group', $2, 1, 1)`,
		f.prayerID, f.groupID)
}

func (f gateFixture) grantDirectToRecipient(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db,
		`INSERT INTO prayer_access (prayer_id, access_type, access_type_id, created_by, updated_by)
		 VALUES ($1, 'user', $2, 1, 1)`,
		f.prayerID, f.recipientID)
}

func (f gateFixture) addRecipientToCircle(t *testing.T, db *sql.DB, active bool) {
	t.Helper()
	if active {
		mustExec(t, db,
			`INSERT INTO user_group (user_profile_id, group_profile_id, is_active, archived_at, updated_by, created_by)
			 VALUES ($1, $2, TRUE, NULL, 1, 1)`,
			f.recipientID, f.groupID)
		return
	}
	// archived membership: exactly what ArchiveCircles writes
	mustExec(t, db,
		`INSERT INTO user_group (user_profile_id, group_profile_id, is_active, archived_at, updated_by, created_by)
		 VALUES ($1, $2, FALSE, CURRENT_TIMESTAMP, 1, 1)`,
		f.recipientID, f.groupID)
}

func TestNotificationGateIntegration(t *testing.T) {
	dsn := os.Getenv("NOTIF_GATE_IT_DSN")
	if dsn == "" {
		t.Skip("set NOTIF_GATE_IT_DSN to run this integration test")
	}

	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}

	original := initializers.DB
	initializers.DB = goqu.New("postgres", sqlDB)
	t.Cleanup(func() { initializers.DB = original })

	t.Run("active circle member is notified", func(t *testing.T) {
		f := newGateFixture(t, sqlDB, "active")
		f.shareToCircle(t, sqlDB)
		f.addRecipientToCircle(t, sqlDB, true)

		allowed, err := canReceiveNotificationForPrayer(f.prayerID, f.recipientID)
		if err != nil {
			t.Fatalf("gate errored: %v", err)
		}
		if !allowed {
			t.Fatal("active member should be notified, gate said no")
		}
	})

	// The bug this gate exists to fix: before it, an archived member kept
	// receiving comment pushes that deep-link into a circle no longer in
	// their list.
	t.Run("archived member is NOT notified", func(t *testing.T) {
		f := newGateFixture(t, sqlDB, "archived")
		f.shareToCircle(t, sqlDB)
		f.addRecipientToCircle(t, sqlDB, false)

		allowed, err := canReceiveNotificationForPrayer(f.prayerID, f.recipientID)
		if err != nil {
			t.Fatalf("gate errored: %v", err)
		}
		if allowed {
			t.Fatal("archived member must not be notified, gate said yes")
		}
	})

	// The access_type='user' branch. A prayer shared directly to someone
	// involves no circle at all, so a user with zero circles must still be
	// notified -- this is the case that makes the gate more than a
	// membership check, and the one a naive is_active filter would break.
	t.Run("direct user grant is notified even with no circles", func(t *testing.T) {
		f := newGateFixture(t, sqlDB, "direct")
		f.grantDirectToRecipient(t, sqlDB)
		// deliberately: no circle membership row at all

		allowed, err := canReceiveNotificationForPrayer(f.prayerID, f.recipientID)
		if err != nil {
			t.Fatalf("gate errored: %v", err)
		}
		if !allowed {
			t.Fatal("direct user grant should be notified, gate said no")
		}
	})

	// A user who archived the circle keeps their own prayers in their personal
	// list, so they must still hear about comments on them. Their deep link
	// degrades to prayer-only via getSharedGroupForCommentNotification
	// returning nil, which is correct -- but the notification must still send.
	t.Run("author who archived the circle still hears about their own prayer", func(t *testing.T) {
		f := newGateFixture(t, sqlDB, "author_archived")
		f.shareToCircle(t, sqlDB)
		f.grantDirectToRecipient(t, sqlDB)
		f.addRecipientToCircle(t, sqlDB, false)

		allowed, err := canReceiveNotificationForPrayer(f.prayerID, f.recipientID)
		if err != nil {
			t.Fatalf("gate errored: %v", err)
		}
		if !allowed {
			t.Fatal("author retaining a user grant should be notified despite archiving, gate said no")
		}
	})

	t.Run("non-member with no grant is NOT notified", func(t *testing.T) {
		f := newGateFixture(t, sqlDB, "stranger")
		f.shareToCircle(t, sqlDB)
		// deliberately: no membership, no direct grant

		allowed, err := canReceiveNotificationForPrayer(f.prayerID, f.recipientID)
		if err != nil {
			t.Fatalf("gate errored: %v", err)
		}
		if allowed {
			t.Fatal("stranger must not be notified, gate said yes")
		}
	})

	t.Run("IsActiveCircleMember distinguishes active from archived", func(t *testing.T) {
		active := newGateFixture(t, sqlDB, "iacm_active")
		active.addRecipientToCircle(t, sqlDB, true)

		ok, err := IsActiveCircleMember(active.groupID, active.recipientID)
		if err != nil {
			t.Fatalf("errored: %v", err)
		}
		if !ok {
			t.Fatal("active member reported as not a member")
		}

		archived := newGateFixture(t, sqlDB, "iacm_archived")
		archived.addRecipientToCircle(t, sqlDB, false)

		ok, err = IsActiveCircleMember(archived.groupID, archived.recipientID)
		if err != nil {
			t.Fatalf("errored: %v", err)
		}
		if ok {
			t.Fatal("archived member reported as an active member")
		}
	})

	t.Run("IsActiveCircleMember returns false for a total stranger", func(t *testing.T) {
		f := newGateFixture(t, sqlDB, "iacm_stranger")

		ok, err := IsActiveCircleMember(f.groupID, f.recipientID)
		if err != nil {
			t.Fatalf("errored: %v", err)
		}
		if ok {
			t.Fatal("non-member reported as an active member")
		}
	})
}
