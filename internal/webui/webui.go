// Package webui carries the static page. The assets are compiled into the
// binary so deploying is one file, with an escape hatch for editing them live.
package webui

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

//go:embed assets
var embedded embed.FS

// Assets returns a handler for the static files and the bytes of index.html.
//
// When dir is non-empty the files are read from disk on every request, which
// is what you want while editing CSS. Otherwise they come from the binary.
func Assets(dir string) (http.Handler, []byte, error) {
	var files fs.FS

	if dir != "" {
		info, err := os.Stat(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("read assets from %s: %w", dir, err)
		}
		if !info.IsDir() {
			return nil, nil, fmt.Errorf("assets path %s is not a directory", dir)
		}
		files = os.DirFS(dir)
	} else {
		sub, err := fs.Sub(embedded, "assets")
		if err != nil {
			return nil, nil, fmt.Errorf("open embedded assets: %w", err)
		}
		files = sub
	}

	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return nil, nil, fmt.Errorf("read index.html: %w", err)
	}

	return http.FileServerFS(files), index, nil
}

// DevDir finds the asset directory next to the source, so `-dev` works from a
// checkout without anyone having to type the path.
func DevDir() string {
	candidates := []string{
		filepath.Join("internal", "webui", "assets"),
		filepath.Join("..", "..", "internal", "webui", "assets"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return ""
}
