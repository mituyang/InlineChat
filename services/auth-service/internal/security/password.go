package security

import "golang.org/x/crypto/bcrypt"

func HashPassword(raw string, cost int) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(raw), cost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func ComparePassword(hash string, raw string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(raw))
}
