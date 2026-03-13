package routes

import (
	"github.com/labstack/echo/v4"
	sgplib "github.com/verbeux-ai/whatsmiau/lib/sgp"
	"github.com/verbeux-ai/whatsmiau/repositories/instances"
	"github.com/verbeux-ai/whatsmiau/server/controllers"
	"github.com/verbeux-ai/whatsmiau/services"
)

func SGP(group *echo.Group, svc *sgplib.SGPService) {
	repo := instances.NewRedis(services.Redis())
	ctrl := controllers.NewSGP(repo, svc)
	group.POST("/:id", ctrl.GenerateRequest)
}
