package controllers

import (
	"os"
	"time"

	"github.com/PrayerLoop/models"
	"github.com/golang-jwt/jwt/v4"
)

// generateAccessToken mints the prayerloop JWT. Every auth path (password
// login, OAuth login, confirm-link) must issue tokens through this single
// helper so CheckAuth always sees the same {id, exp, role} shape.
func generateAccessToken(user models.UserProfile) (string, error) {
	role := "user"
	if user.Admin {
		role = "admin"
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id":   user.User_Profile_ID,
		"exp":  time.Now().Add(time.Hour * 24).Unix(),
		"role": role,
	})

	return token.SignedString([]byte(os.Getenv("SECRET")))
}
