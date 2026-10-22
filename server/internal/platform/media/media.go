package media

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"
)

// Sign returns a short-lived HMAC for one image variant, so <img src> works
// without an Authorization header and links expire quickly.
func Sign(secret string, imageID int64, variant string, exp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d|%s|%d", imageID, variant, exp)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func Verify(secret string, imageID int64, variant string, exp int64, sig string) bool {
	if exp < time.Now().Unix() {
		return false
	}
	return hmac.Equal([]byte(Sign(secret, imageID, variant, exp)), []byte(sig))
}

// URL builds a signed media URL path (relative, front-end prefixes the API base).
func URL(secret string, imageID int64, variant string, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	sig := Sign(secret, imageID, variant, exp)
	return "/media/" + strconv.FormatInt(imageID, 10) + "/" + variant +
		"?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + sig
}
