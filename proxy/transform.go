package proxy

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var errFileNotFound = errors.New("file not found")

//go:embed web/*
var WebFS embed.FS

func loadFromDisk(webRoot, cleanPath string) ([]byte, bool) {
	if webRoot == "" {
		return nil, false
	}

	root, err := os.OpenRoot(webRoot)
	if err != nil {
		return nil, false
	}
	defer root.Close()

	fi, statErr := root.Stat(cleanPath)
	if statErr != nil || fi.IsDir() {
		return nil, false
	}

	content, readErr := root.ReadFile(cleanPath)
	if readErr != nil {
		return nil, false
	}

	return content, true
}

// GetStatic retrieves static assets from either an override filesystem or the embedded WebFS.
func GetStatic(webRoot, fpath string) ([]byte, string, error) {
	cleanPath, _, _ := strings.Cut(fpath, "?")
	if cleanPath == "/" || cleanPath == "" {
		cleanPath = "index.html"
	}
	cleanPath = strings.TrimPrefix(cleanPath, "/")

	// 1. If webRoot is provided and exists on disk, check local disk first
	if data, ok := loadFromDisk(webRoot, cleanPath); ok {
		return data, DetectMimeType(cleanPath), nil
	}

	// 2. Read from embedded WebFS (path is web/cleanPath)
	embedPath := filepath.ToSlash(filepath.Join("web", cleanPath))
	data, err := WebFS.ReadFile(embedPath)
	if err == nil {
		return data, DetectMimeType(cleanPath), nil
	}

	return nil, "", fmt.Errorf("%w: %s", errFileNotFound, cleanPath)
}

// DetectMimeType returns content-type by file extension.
func DetectMimeType(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".js"):
		return "application/javascript"
	case strings.HasSuffix(lower, ".css"):
		return "text/css"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm"):
		return "text/html"
	case strings.HasSuffix(lower, ".otf"):
		return "font/opentype"
	case strings.HasSuffix(lower, ".woff"):
		return "font/woff"
	case strings.HasSuffix(lower, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(lower, ".ttf"):
		return "font/ttf"
	case strings.HasSuffix(lower, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(lower, ".eot"):
		return "application/vnd.ms-fontobject"
	case strings.HasSuffix(lower, ".json"):
		return "application/json"
	case strings.HasSuffix(lower, ".xml"):
		return "application/xml"
	default:
		return "text/plain"
	}
}

// InjectJS injects JavaScript script tags before </body> or at the end of HTML.
func InjectJS(htmlsrc []byte, scriptPaths ...string) []byte {
	var tags bytes.Buffer
	for _, p := range scriptPaths {
		fmt.Fprintf(&tags, `<script type="text/javascript" src="%s"></script>`, p)
	}
	tagBytes := tags.Bytes()

	lower := bytes.ToLower(htmlsrc)
	bodyIdx := bytes.LastIndex(lower, []byte("</body>"))
	if bodyIdx != -1 {
		res := make([]byte, 0, clampedCapAdd(len(htmlsrc), len(tagBytes)))
		res = append(res, htmlsrc[:bodyIdx]...)
		res = append(res, tagBytes...)
		res = append(res, htmlsrc[bodyIdx:]...)

		return res
	}

	return append(htmlsrc, tagBytes...)
}
