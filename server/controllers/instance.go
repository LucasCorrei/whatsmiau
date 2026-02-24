package controllers

import (
	"encoding/base64"
	"errors"
	"math/rand/v2"
	"net/http"

	"github.com/verbeux-ai/whatsmiau/env"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"github.com/verbeux-ai/whatsmiau/models"
	"github.com/verbeux-ai/whatsmiau/repositories/instances"
	"go.mau.fi/whatsmeow/types"

	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v4"
	"github.com/skip2/go-qrcode"
	"github.com/verbeux-ai/whatsmiau/interfaces"
	"github.com/verbeux-ai/whatsmiau/server/dto"
	"github.com/verbeux-ai/whatsmiau/utils"
	"go.uber.org/zap"
)

type Instance struct {
	repo      interfaces.InstanceRepository
	whatsmiau *whatsmiau.Whatsmiau
}

func NewInstances(repository interfaces.InstanceRepository, whatsmiau *whatsmiau.Whatsmiau) *Instance {
	return &Instance{
		repo:      repository,
		whatsmiau: whatsmiau,
	}
}

func (s *Instance) Create(ctx echo.Context) error {
	var request dto.CreateInstanceRequest

	if err := ctx.Bind(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}

	if err := validator.New().Struct(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusBadRequest, err, "invalid request body")
	}

	instanceID := request.InstanceName
	if instanceID == "" {
		instanceID = request.ID
	}
	if instanceID == "" {
		return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "instanceName is required")
	}

	if request.Instance == nil {
		request.Instance = &models.Instance{}
	}

	request.Instance.ID = instanceID
	request.Instance.RemoteJID = ""

	// Proxy automático
	if request.Instance.ProxyHost == "" && len(env.Env.ProxyAddresses) > 0 {
		rd := rand.IntN(len(env.Env.ProxyAddresses))
		proxyUrl := env.Env.ProxyAddresses[rd]

		proxy, err := parseProxyURL(proxyUrl)
		if err != nil {
			return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "invalid proxy url on env")
		}

		request.Instance.ProxyHost = proxy.Host
		request.Instance.ProxyPort = proxy.Port
		request.Instance.ProxyProtocol = proxy.Protocol
		request.Instance.ProxyUsername = proxy.Username
		request.Instance.ProxyPassword = proxy.Password
	}

	// =========================
	// Chatwoot
	// =========================

	if request.Instance.ChatwootEnabled {
		inboxID, err := s.whatsmiau.ChatwootService.EnsureInbox(request.Instance)
		if err != nil {
			return utils.HTTPFail(ctx, 500, err, "failed to ensure chatwoot inbox")
		}
		request.Instance.ChatwootInboxID = inboxID
	}

	if err := s.repo.Create(ctx.Request().Context(), request.Instance); err != nil {
		zap.L().Error("failed to create instance", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to create instance")
	}

	return ctx.JSON(http.StatusCreated, dto.CreateInstanceResponse{
		Instance: request.Instance,
	})
}

func (s *Instance) Update(ctx echo.Context) error {
	var request dto.UpdateInstanceRequest

	if err := ctx.Bind(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}

	if err := validator.New().Struct(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusBadRequest, err, "invalid request body")
	}

	instance, err := s.repo.GetByID(ctx.Request().Context(), request.ID)
	if err != nil {
		return utils.HTTPFail(ctx, http.StatusNotFound, err, "instance not found")
	}

	instance.ChatwootEnabled = request.ChatwootEnabled
	instance.ChatwootURL = request.ChatwootURL
	instance.ChatwootToken = request.ChatwootToken
	instance.ChatwootAccountID = request.ChatwootAccountID
	instance.ChatwootInboxName = request.ChatwootInboxName

	if instance.ChatwootEnabled {
		inboxID, err := s.whatsmiau.ChatwootService.EnsureInbox(instance)
		if err != nil {
			return utils.HTTPFail(ctx, 500, err, "failed to ensure chatwoot inbox")
		}
		instance.ChatwootInboxID = inboxID
	}

	if err := s.repo.Save(ctx.Request().Context(), instance); err != nil {
		return utils.HTTPFail(ctx, 500, err, "failed to update instance")
	}

	return ctx.JSON(http.StatusOK, dto.UpdateInstanceResponse{
		Instance: instance,
	})
}
