package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// APIError is a business error carrying an HTTP status and a machine code.
type APIError struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
}

func (e *APIError) Error() string { return e.Message }

func New(status int, code, msg string) *APIError {
	return &APIError{Status: status, Code: code, Message: msg}
}

func BadRequest(msg string) *APIError   { return New(http.StatusBadRequest, "BAD_REQUEST", msg) }
func NotFound(msg string) *APIError     { return New(http.StatusNotFound, "NOT_FOUND", msg) }
func Conflict(msg string) *APIError     { return New(http.StatusConflict, "CONFLICT", msg) }
func Forbidden(msg string) *APIError    { return New(http.StatusForbidden, "FORBIDDEN", msg) }
func Unauthorized(msg string) *APIError { return New(http.StatusUnauthorized, "UNAUTHORIZED", msg) }

func Validation(fields map[string]string, msg string) *APIError {
	return &APIError{Status: http.StatusUnprocessableEntity, Code: "VALIDATION_FAILED", Message: msg, Fields: fields}
}

func LimitExceeded(msg string) *APIError {
	return New(http.StatusTooManyRequests, "LIMIT_EXCEEDED", msg)
}

// OK writes the success envelope.
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"data": data, "request_id": RequestIDOf(c)})
}

// Fail writes the error envelope and aborts the chain.
func Fail(c *gin.Context, err error) {
	var ae *APIError
	if !errors.As(err, &ae) {
		slog.Error("unhandled error", "err", err, "request_id", RequestIDOf(c), "path", c.FullPath())
		ae = New(http.StatusInternalServerError, "INTERNAL", "服务端错误，请稍后重试")
	}
	body := gin.H{"code": ae.Code, "message": ae.Message}
	if len(ae.Fields) > 0 {
		body["fields"] = ae.Fields
	}
	c.AbortWithStatusJSON(ae.Status, gin.H{"error": body, "request_id": RequestIDOf(c)})
}

// RequestID middleware assigns/propagates a request id.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-Id")
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		c.Set("request_id", id)
		c.Header("X-Request-Id", id)
		c.Next()
	}
}

func RequestIDOf(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Recover converts panics into 500 responses.
func Recover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic", "recover", r, "path", c.Request.URL.Path, "request_id", RequestIDOf(c))
				Fail(c, New(http.StatusInternalServerError, "INTERNAL", "服务端错误，请稍后重试"))
			}
		}()
		c.Next()
	}
}

// AccessLog logs one line per request using slog.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
			"request_id", RequestIDOf(c),
		)
	}
}

// CORS is only needed for local development (vite dev server on another port).
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-Id,Idempotency-Key")
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// ParamInt64 reads a path parameter as int64.
func ParamInt64(c *gin.Context, name string) (int64, error) {
	raw := c.Param(name)
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return 0, BadRequest("路径参数 " + name + " 非法")
	}
	return v, nil
}

// QueryInt reads an int query parameter with a default.
func QueryInt(c *gin.Context, name string, def int) int {
	raw := c.Query(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}

// QueryInt64 reads an optional int64 query parameter.
func QueryInt64(c *gin.Context, name string) int64 {
	raw := c.Query(name)
	if raw == "" {
		return 0
	}
	v, _ := strconv.ParseInt(raw, 10, 64)
	return v
}

// Paging returns page (1-based) and page size clamped to [1,100].
func Paging(c *gin.Context) (int, int) {
	page := QueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	size := QueryInt(c, "page_size", 20)
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}
