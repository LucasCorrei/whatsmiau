package ui

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/labstack/echo/v4"
)

//go:embed index.html
var staticFiles embed.FS

// Register mounts the dashboard at /ui (no auth required).
func Register(app *echo.Echo) {
	sub, err := fs.Sub(staticFiles, ".")
	if err != nil {
		panic(err)
	}
	handler := echo.WrapHandler(http.FileServer(http.FS(sub)))
	app.GET("/ui", func(c echo.Context) error {
		c.Request().URL.Path = "/"
		return handler(c)
	})
	app.GET("/ui/*", func(c echo.Context) error {
		return handler(c)
	})
}
