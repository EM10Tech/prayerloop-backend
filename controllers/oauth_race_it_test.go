package controllers

// Opt-in integration test for the OAuth auto-create double-tap race: fires
// concurrent createOAuthUser calls for the same (provider, sub) against a
// real Postgres and asserts exactly one account is created and every request
// returns it. Skipped unless RACE_IT_DSN is set, e.g.:
//
//	RACE_IT_DSN="host=localhost port=5432 user=... password=... dbname=prayerloop_test sslmode=disable" \
//	  go test ./controllers/ -run TestOAuthAutoCreateRaceIntegration -count=1 -v

import (
	"database/sql"
	"os"
	"sync"
	"testing"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/services"
	"github.com/doug-martin/goqu/v9"
	_ "github.com/lib/pq"
)

func TestOAuthAutoCreateRaceIntegration(t *testing.T) {
	dsn := os.Getenv("RACE_IT_DSN")
	if dsn == "" {
		t.Skip("set RACE_IT_DSN to run this integration test")
	}

	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}

	prevDB := initializers.DB
	initializers.DB = goqu.New("postgres", sqlDB)
	defer func() { initializers.DB = prevDB }()

	const sub = "race_it_sub_1"
	cleanup := func() {
		mustExec := func(q string) {
			if _, err := sqlDB.Exec(q); err != nil {
				t.Fatalf("cleanup %q: %v", q, err)
			}
		}
		mustExec(`DELETE FROM user_external_identity WHERE provider_user_id = '` + sub + `'`)
		mustExec(`DELETE FROM prayer_subject WHERE user_profile_id IN (SELECT user_profile_id FROM user_profile WHERE username LIKE 'pc_` + sub + `%')`)
		mustExec(`DELETE FROM user_profile WHERE username LIKE 'pc_` + sub + `%'`)
	}
	cleanup()
	defer cleanup()

	identity := &services.ProviderIdentity{
		Sub:       sub,
		Email:     "race-it@example.com",
		FirstName: "Race",
		LastName:  "It",
	}

	const n = 5
	users := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			u, err := createOAuthUser("planning_center", identity, nil)
			errs[i] = err
			if u != nil {
				users[i] = u.User_Profile_ID
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Errorf("request %d errored (should have recovered idempotently): %v", i, errs[i])
		}
	}
	for i := 1; i < n; i++ {
		if users[i] != users[0] {
			t.Errorf("request %d returned user %d, want %d (all must return the same account)", i, users[i], users[0])
		}
	}

	var profileCount, identityCount, subjectCount int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM user_profile WHERE username LIKE 'pc_` + sub + `%'`).Scan(&profileCount); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT count(*) FROM user_external_identity WHERE provider_user_id = '` + sub + `'`).Scan(&identityCount); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT count(*) FROM prayer_subject WHERE user_profile_id IN (SELECT user_profile_id FROM user_profile WHERE username LIKE 'pc_` + sub + `%')`).Scan(&subjectCount); err != nil {
		t.Fatal(err)
	}
	if profileCount != 1 || identityCount != 1 || subjectCount != 1 {
		t.Errorf("row counts after race: user_profile=%d user_external_identity=%d prayer_subject=%d, want 1/1/1",
			profileCount, identityCount, subjectCount)
	}
}
