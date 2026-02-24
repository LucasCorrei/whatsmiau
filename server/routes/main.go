package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/verbeux-ai/whatsmiau/server/middleware"
)

func Load(app *echo.Echo) {
	// Webhook SEM autenticação - registrado ANTES do middleware
	webhookGroup := app.Group("/webhook")
	Webhook(webhookGroup)
	
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
