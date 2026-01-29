package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
)

type telegramUser struct {
	ID int64 `json:"id"`
}

func Validate(initData string, botToken string, allowedUserID int64) error {
	params, err := url.ParseQuery(initData)
	if err != nil {
		return err
	}

	hash := params.Get("hash")
	if hash == "" {
		return errors.New("missing hash")
	}

	// Check user first
	userJSON := params.Get("user")
	if userJSON == "" {
		return errors.New("missing user")
	}

	var user telegramUser
	if err := json.Unmarshal([]byte(userJSON), &user); err != nil {
		return err
	}

	if user.ID != allowedUserID {
		return errors.New("unauthorized user")
	}

	// Build data check string
	params.Del("hash")

	var keys []string
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+params.Get(k))
	}
	dataCheckString := strings.Join(parts, "\n")

	// Calculate expected hash
	secretKey := hmac.New(sha256.New, []byte("WebAppData"))
	secretKey.Write([]byte(botToken))

	h := hmac.New(sha256.New, secretKey.Sum(nil))
	h.Write([]byte(dataCheckString))
	expectedHash := hex.EncodeToString(h.Sum(nil))

	if !hmac.Equal([]byte(hash), []byte(expectedHash)) {
		return errors.New("invalid hash")
	}

	return nil
}
