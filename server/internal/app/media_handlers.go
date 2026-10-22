package app

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/shangyue/font-label/server/internal/platform/media"
)

// handleMedia serves image variants behind a short-lived HMAC signature so
// <img src> works without an Authorization header and links expire quickly.
func (s *Server) handleMedia(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.Status(http.StatusNotFound)
		return
	}
	variant := c.Param("variant")
	switch variant {
	case "thumb", "preview", "original":
	default:
		c.Status(http.StatusNotFound)
		return
	}
	exp, err := strconv.ParseInt(c.Query("exp"), 10, 64)
	if err != nil {
		c.Status(http.StatusForbidden)
		return
	}
	if !media.Verify(s.cfg.MediaSecret, id, variant, exp, c.Query("sig")) {
		c.Status(http.StatusForbidden)
		return
	}

	var relPath, thumb, preview string
	err = s.pool().QueryRow(c.Request.Context(),
		`SELECT rel_path, COALESCE(thumb_path,''), COALESCE(preview_path,'') FROM images WHERE id=$1`, id).
		Scan(&relPath, &thumb, &preview)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	rel := relPath
	switch variant {
	case "thumb":
		if thumb != "" {
			rel = thumb
		}
	case "preview":
		if preview != "" {
			rel = preview
		}
	}
	f, err := s.store.Open(rel)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	contentType := "image/jpeg"
	if strings.HasSuffix(rel, ".png") {
		contentType = "image/png"
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	http.ServeContent(c.Writer, c.Request, rel, st.ModTime(), f)
}
