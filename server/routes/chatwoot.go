package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"github.com/verbeux-ai/whatsmiau/server/controllers"
)

func Chatwoot(app *echo.Echo) {
	controller := controllers.NewChatwootWebhook(whatsmiau.Get())

	// 🔥 Padrão profissional
	app.POST("/webhook/chatwoot/:instance", controller.Handle)
}
