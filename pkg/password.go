package pkg

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"

	"golang.org/x/crypto/bcrypt"
)

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(bytes), err
}

func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// GenerateSecureToken generates a cryptographically secure random token of the specified length in bytes
func GenerateSecureTokenLength(length int) (string, error) {
	// Create a byte slice to hold the random bytes
	token := make([]byte, length)

	// Read random bytes from the crypto/rand package
	_, err := rand.Read(token)
	if err != nil {
		return "", err
	}

	// Encode the random bytes to a hex string
	return hex.EncodeToString(token), nil
}

func GenerateSecureToken() (string, error) {
	return GenerateSecureTokenLength(32)
}

// GenerateOTP generates a 6-digit One-Time Password (OTP)
func GenerateOTP() (string, error) {
	// Define the maximum value (1000000) for a 6-digit OTP (000000 to 999999)
	max := big.NewInt(1000000)

	// Generate a random number in the range [0, 999999]
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}

	// Format the number as a 6-digit string with leading zeros if necessary
	otp := fmt.Sprintf("%06d", n.Int64())

	return otp, nil
}
