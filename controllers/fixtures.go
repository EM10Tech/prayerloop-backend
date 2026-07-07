package controllers

import (
	"time"

	"github.com/PrayerLoop/models"
	"golang.org/x/crypto/bcrypt"
)

// Test fixture data for use in tests

// MockUser creates a sample user profile for testing
func MockUser() models.UserProfile {
	phone := "1234567890"
	return models.UserProfile{
		User_Profile_ID: 1,
		Username:        "testuser",
		First_Name:      "Test",
		Last_Name:       "User",
		Email:           "test@example.com",
		Phone_Number:    &phone,
		Admin:           false,
		Created_By:      1,
		Updated_By:      1,
		Datetime_Create: time.Now(),
		Datetime_Update: time.Now(),
	}
}

// MockUserWithPassword creates a sample user with a bcrypt hashed password
// Password is "password123" - use this in tests
func MockUserWithPassword() models.UserProfile {
	phone := "1234567890"
	// Pre-hashed password for "password123"
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	hashedPasswordStr := string(hashedPassword)
	return models.UserProfile{
		User_Profile_ID: 1,
		Username:        "testuser",
		Password:        &hashedPasswordStr,
		First_Name:      "Test",
		Last_Name:       "User",
		Email:           "test@example.com",
		Phone_Number:    &phone,
		Admin:           false,
		Created_By:      1,
		Updated_By:      1,
		Datetime_Create: time.Now(),
		Datetime_Update: time.Now(),
	}
}

// MockAdminUser creates a sample admin user for testing
func MockAdminUser() models.UserProfile {
	phone := "9876543210"
	return models.UserProfile{
		User_Profile_ID: 2,
		Username:        "adminuser",
		First_Name:      "Admin",
		Last_Name:       "User",
		Email:           "admin@example.com",
		Phone_Number:    &phone,
		Admin:           true,
		Created_By:      1,
		Updated_By:      1,
		Datetime_Create: time.Now(),
		Datetime_Update: time.Now(),
	}
}

// MockAdminUserWithPassword creates a sample admin user with a bcrypt hashed password
// Password is "admin123" - use this in tests
func MockAdminUserWithPassword() models.UserProfile {
	phone := "9876543210"
	// Pre-hashed password for "admin123"
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	hashedPasswordStr := string(hashedPassword)
	return models.UserProfile{
		User_Profile_ID: 2,
		Username:        "adminuser",
		Password:        &hashedPasswordStr,
		First_Name:      "Admin",
		Last_Name:       "User",
		Email:           "admin@example.com",
		Phone_Number:    &phone,
		Admin:           true,
		Created_By:      1,
		Updated_By:      1,
		Datetime_Create: time.Now(),
		Datetime_Update: time.Now(),
	}
}

// MockGroupProfile creates a sample group for testing
func MockGroupProfile() models.GroupProfile {
	prayerSubjectID := 100
	return models.GroupProfile{
		Group_Profile_ID:       1,
		Group_Name:             "Test Group",
		Group_Description:      "A test group",
		Is_Active:              true,
		Created_By:             1,
		Updated_By:             1,
		Datetime_Create:        time.Now(),
		Datetime_Update:        time.Now(),
		Prayer_Subject_ID:      &prayerSubjectID,
		Group_Display_Sequence: 0,
	}
}
