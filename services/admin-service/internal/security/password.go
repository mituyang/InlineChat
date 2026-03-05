package security

import "golang.org/x/crypto/bcrypt"

// HashPassword 使用 bcrypt 按给定 cost 生成密码哈希。
func HashPassword(raw string, cost int) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(raw), cost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}
