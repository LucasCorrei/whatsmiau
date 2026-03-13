package routes

import (
	"github.com/labstack/echo/v4"
	sgp "github.com/verbeux-ai/whatsmiau/lib/sgp"
	"github.com/verbeux-ai/whatsmiau/server/middleware"
)

func Load(app *echo.Echo) {
	// Rotas SEM autenticação global
	webhookGroup := app.Group("/webhook")
	Webhook(webhookGroup)
	SGP(webhookGroup.Group("/sgp"), sgp.GetService())

	// Middleware de autenticação só para rotas V1
	v1Group := app.Group("/v1")
	v1Group.Use(middleware.Simplify(middleware.Auth))
	V1(v1Group)
}

func V1(group *echo.Group) {
	Root(group)
	Instance(group.Group("/instance"))
	Message(group.Group("/instance/:instance/message"))
	Chat(group.Group("/instance/:instance/chat"))
	ChatEVO(group.Group("/chat"))
	MessageEVO(group.Group("/message"))
}
