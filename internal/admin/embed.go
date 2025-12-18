package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
)

//go:embed all:dist
var adminFS embed.FS

// Handler returns a Fiber handler that serves the admin dashboard
func Handler() fiber.Handler {
	// Get the dist subdirectory
	distFS, err := fs.Sub(adminFS, "dist")
	if err != nil {
		panic(err)
	}

	// Create filesystem middleware
	fsHandler := filesystem.New(filesystem.Config{
		Root:       http.FS(distFS),
		PathPrefix: "",
		Browse:     false,
	})

	return func(c *fiber.Ctx) error {
		// Get the path without the /admin prefix
		path := c.Path()
		path = strings.TrimPrefix(path, "/admin")
		if path == "" {
			path = "/"
		}

		// Check if it's a file request (has extension)
		if strings.Contains(path, ".") {
			// Serve the file
			c.Path(path)
			return fsHandler(c)
		}

		// For all other paths, serve index.html (SPA routing)
		c.Path("/index.html")
		return fsHandler(c)
	}
}
