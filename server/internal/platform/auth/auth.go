package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
)

var (
	ErrNoToken    = errors.New("no token")
	ErrBadToken   = errors.New("bad token")
	ErrDisabled   = errors.New("user disabled")
	ErrTokenStale = errors.New("token stale")
)

// Principal is the authenticated caller.
type Principal struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// Claims is the JWT payload.
type Claims struct {
	Role string `json:"role"`
	TV   int    `json:"tv"`
	jwt.RegisteredClaims
}

const ctxKey = "principal"

// ---------- password hashing (argon2id) ----------

func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const (
		timeCost = 3
		memory   = 64 * 1024
		threads  = 2
		keyLen   = 32
	)
	key := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, keyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory, timeCost, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var memory uint32
	var timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---------- JWT ----------

func Sign(secret string, p Principal, tv int, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		Role: p.Role,
		TV:   tv,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprint(p.ID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    "font-label",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(secret))
}

func Parse(secret, raw string) (*Claims, error) {
	tok, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrBadToken
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, ErrBadToken
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, ErrBadToken
	}
	return claims, nil
}

// ---------- middleware ----------

// Loader fetches the live user record and its token version.
type Loader func(ctx context.Context, id int64) (Principal, int, string, error)

// Middleware authenticates the bearer token and checks token_version.
func Middleware(secret string, load Loader) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearer(c)
		if raw == "" {
			abort(c, ErrNoToken)
			return
		}
		claims, err := Parse(secret, raw)
		if err != nil {
			abort(c, err)
			return
		}
		var id int64
		if _, err := fmt.Sscan(claims.Subject, &id); err != nil || id <= 0 {
			abort(c, ErrBadToken)
			return
		}
		p, tv, status, err := load(c.Request.Context(), id)
		if err != nil {
			abort(c, ErrBadToken)
			return
		}
		if status != "active" {
			abort(c, ErrDisabled)
			return
		}
		if tv != claims.TV {
			abort(c, ErrTokenStale)
			return
		}
		c.Set(ctxKey, p)
		c.Next()
	}
}

func bearer(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func abort(c *gin.Context, err error) {
	msg := "请先登录"
	switch {
	case errors.Is(err, ErrDisabled):
		msg = "账号已被停用，请联系管理员"
	case errors.Is(err, ErrTokenStale):
		msg = "登录已失效，请重新登录"
	}
	c.AbortWithStatusJSON(401, gin.H{"error": gin.H{"code": "UNAUTHORIZED", "message": msg}})
}

// Require restricts a route to the given roles.
func Require(roles ...string) gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, r := range roles {
		allowed[r] = true
	}
	return func(c *gin.Context) {
		p, ok := Current(c)
		if !ok {
			abort(c, ErrNoToken)
			return
		}
		if !allowed[p.Role] {
			c.AbortWithStatusJSON(403, gin.H{"error": gin.H{"code": "FORBIDDEN", "message": "没有权限执行该操作"}})
			return
		}
		c.Next()
	}
}

// Current returns the authenticated principal.
func Current(c *gin.Context) (Principal, bool) {
	v, ok := c.Get(ctxKey)
	if !ok {
		return Principal{}, false
	}
	p, ok := v.(Principal)
	return p, ok
}

// MustCurrent is Current for handlers that sit behind Middleware.
func MustCurrent(c *gin.Context) Principal {
	p, _ := Current(c)
	return p
}
