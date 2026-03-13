package controllers

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/verbeux-ai/whatsmiau/interfaces"
	sgplib "github.com/verbeux-ai/whatsmiau/lib/sgp"
	"github.com/verbeux-ai/whatsmiau/utils"
	"go.uber.org/zap"
)

type SGP struct {
	repo interfaces.InstanceRepository
	sgp  *sgplib.SGPService
}

func NewSGP(repo interfaces.InstanceRepository, sgp *sgplib.SGPService) *SGP {
	return &SGP{repo: repo, sgp: sgp}
}

type sgpGenerateRequest struct {
	Instance string `json:"instance"` // instância pode vir no body também
	Token    string `json:"token"`    // token da instância para autenticação
	Numero   string `json:"numero"`
	Mensagem string `json:"mensagem"`
}

func (s *SGP) GenerateRequest(ctx echo.Context) error {
	// instanceID: URL param tem prioridade, fallback para body
	instanceID := ctx.Param("id")

	var req sgpGenerateRequest
	if err := ctx.Bind(&req); err != nil {
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "invalid request body")
	}

	if instanceID == "" {
		instanceID = req.Instance
	}
	if instanceID == "" {
		return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "missing instance id")
	}

	c := ctx.Request().Context()
	instances, err := s.repo.List(c, instanceID)
	if err != nil || len(instances) == 0 {
		return utils.HTTPFail(ctx, http.StatusNotFound, nil, "instance not found")
	}

	inst := instances[0]

	// Validação de token SGP
	if inst.SGPToken != "" && req.Token != inst.SGPToken {
		return utils.HTTPFail(ctx, http.StatusUnauthorized, nil, "invalid token")
	}

	// Validação de IP (se configurado)
	if inst.SGPAllowedIPs != "" {
		clientIP := ctx.RealIP()
		allowed := false
		for _, ip := range strings.Split(inst.SGPAllowedIPs, ",") {
			if strings.TrimSpace(ip) == clientIP {
				allowed = true
				break
			}
		}
		if !allowed {
			return utils.HTTPFail(ctx, http.StatusForbidden, nil, "IP not allowed")
		}
	}

	if !inst.SGPEnabled {
		return utils.HTTPFail(ctx, http.StatusForbidden, nil, "SGP integration not enabled for this instance")
	}

	if req.Numero == "" || req.Mensagem == "" {
		return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "campos 'numero' e 'mensagem' são obrigatórios")
	}

	go func() {
		if err := s.sgp.HandleRequest(instanceID, req.Numero, req.Mensagem); err != nil {
			zap.L().Error("sgp: erro ao processar requisição",
				zap.String("instanceId", instanceID),
				zap.Error(err))
		}
	}()

	return ctx.JSON(http.StatusOK, map[string]string{"status": "success"})
}
