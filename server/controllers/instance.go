package controllers

import (
	"encoding/base64"
	"net/http"

	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"github.com/verbeux-ai/whatsmiau/models"

	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v4"
	"github.com/skip2/go-qrcode"
	"github.com/verbeux-ai/whatsmiau/interfaces"
	"github.com/verbeux-ai/whatsmiau/server/dto"
	"github.com/verbeux-ai/whatsmiau/utils"
	"go.mau.fi/whatsmeow/types"
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
	// ================================
	// 1️⃣ Bind do request DTO
	// ================================
	var request dto.CreateInstanceRequest

	if err := ctx.Bind(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}

	// ================================
	// 2️⃣ Validação do DTO
	// ================================
	if err := validator.New().Struct(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusBadRequest, err, "invalid request body")
	}

	// ================================
	// 3️⃣ Mapear DTO → Model
	// ================================
	instance := models.Instance{
		ID:          request.InstanceName, // instanceName → ID
		Integration: request.Integration,
		Token:       request.Token,
		QRCode:      request.QRCode,
		Number:      request.Number,

		RejectCall:      request.RejectCall,
		MsgCall:         request.MsgCall,
		GroupsIgnore:    request.GroupsIgnore,
		AlwaysOnline:    request.AlwaysOnline,
		ReadMessages:    request.ReadMessages,
		ReadStatus:      request.ReadStatus,
		SyncFullHistory: request.SyncFullHistory,

		Webhook: request.Webhook,

		// Chatwoot
		ChatwootAccountID:               request.ChatwootAccountID,
		ChatwootToken:                   request.ChatwootToken,
		ChatwootURL:                     request.ChatwootURL,
		ChatwootSignMsg:                 request.ChatwootSignMsg,
		ChatwootReopenConversation:      request.ChatwootReopenConversation,
		ChatwootConversationPending:     request.ChatwootConversationPending,
		ChatwootImportContacts:          request.ChatwootImportContacts,
		ChatwootNameInbox:               request.ChatwootNameInbox,
		ChatwootMergeBrazilContacts:     request.ChatwootMergeBrazilContacts,
		ChatwootImportMessages:          request.ChatwootImportMessages,
		ChatwootDaysLimitImportMessages: request.ChatwootDaysLimitImportMessages,
		ChatwootOrganization:            request.ChatwootOrganization,
		ChatwootLogo:                    request.ChatwootLogo,

		// Proxy
		InstanceProxy: models.InstanceProxy{
			ProxyHost:     request.ProxyHost,
			ProxyPort:     request.ProxyPort,
			ProxyProtocol: request.ProxyProtocol,
			ProxyUsername: request.ProxyUsername,
			ProxyPassword: request.ProxyPassword,
		},
	}

	c := ctx.Request().Context()

	// ================================
	// 4️⃣ Criar no banco
	// ================================
	if err := s.repo.Create(c, &instance); err != nil {
		zap.L().Error("failed to create instance", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to create instance")
	}

	// ================================
	// 5️⃣ Resposta
	// ================================
	return ctx.JSON(http.StatusCreated, dto.CreateInstanceResponse{
		Instance: instance,
	})
}

func (s *Instance) Update(ctx echo.Context) error {
	// ================================
	// 1️⃣ Bind do request
	// ================================
	var request dto.UpdateInstanceRequest

	if err := ctx.Bind(&request); err != nil {
		return utils.HTTPFail(
			ctx,
			http.StatusUnprocessableEntity,
			err,
			"failed to bind request body",
		)
	}

	// ================================
	// 2️⃣ Validação do DTO
	// ================================
	if err := validator.New().Struct(&request); err != nil {
		return utils.HTTPFail(
			ctx,
			http.StatusBadRequest,
			err,
			"invalid request body",
		)
	}

	c := ctx.Request().Context()

	// ================================
	// 3️⃣ Buscar instância atual
	// ================================
	currentList, err := s.repo.List(c, request.ID)
	if err != nil {
		return utils.HTTPFail(
			ctx,
			http.StatusInternalServerError,
			err,
			"failed to list instances",
		)
	}

	if len(currentList) == 0 {
		return utils.HTTPFail(
			ctx,
			http.StatusNotFound,
			nil,
			"instance not found",
		)
	}

	current := currentList[0]

	// ================================
	// 4️⃣ Atualizações parciais (PATCH)
	// ================================

	// ---------- Webhook ----------
	if request.Webhook != nil {
		// Garante que não vai dar nil panic
		if current.Webhook == nil {
			current.Webhook = &models.InstanceWebhook{}
		}

		if request.Webhook.Url != "" {
			current.Webhook.Url = request.Webhook.Url
		}

		if request.Webhook.Base64 != nil {
			current.Webhook.Base64 = request.Webhook.Base64
		}

		if request.Webhook.Events != nil {
			current.Webhook.Events = request.Webhook.Events
		}

		if request.Webhook.Headers != nil {
			current.Webhook.Headers = request.Webhook.Headers
		}

		if request.Webhook.ByEvents != nil {
			current.Webhook.ByEvents = request.Webhook.ByEvents
		}
	}

	// ---------- Chatwoot ----------
	if request.ChatwootURL != nil {
		current.ChatwootURL = *request.ChatwootURL
	}
	if request.ChatwootToken != nil {
		current.ChatwootToken = *request.ChatwootToken
	}
	if request.ChatwootAccountID != nil {
		current.ChatwootAccountID = *request.ChatwootAccountID
	}
	if request.ChatwootSignMsg != nil {
		current.ChatwootSignMsg = *request.ChatwootSignMsg
	}
	if request.ChatwootReopenConversation != nil {
		current.ChatwootReopenConversation = *request.ChatwootReopenConversation
	}
	if request.ChatwootConversationPending != nil {
		current.ChatwootConversationPending = *request.ChatwootConversationPending
	}
	if request.ChatwootImportContacts != nil {
		current.ChatwootImportContacts = *request.ChatwootImportContacts
	}
	if request.ChatwootNameInbox != nil {
		current.ChatwootNameInbox = *request.ChatwootNameInbox
	}
	if request.ChatwootMergeBrazilContacts != nil {
		current.ChatwootMergeBrazilContacts = *request.ChatwootMergeBrazilContacts
	}
	if request.ChatwootImportMessages != nil {
		current.ChatwootImportMessages = *request.ChatwootImportMessages
	}
	if request.ChatwootDaysLimitImportMessages != nil {
		current.ChatwootDaysLimitImportMessages = *request.ChatwootDaysLimitImportMessages
	}
	if request.ChatwootOrganization != nil {
		current.ChatwootOrganization = *request.ChatwootOrganization
	}
	if request.ChatwootLogo != nil {
		current.ChatwootLogo = *request.ChatwootLogo
	}

	// ---------- Proxy ----------
	if request.ProxyHost != nil {
		current.ProxyHost = *request.ProxyHost
	}
	if request.ProxyPort != nil {
		current.ProxyPort = *request.ProxyPort
	}
	if request.ProxyProtocol != nil {
		current.ProxyProtocol = *request.ProxyProtocol
	}
	if request.ProxyUsername != nil {
		current.ProxyUsername = *request.ProxyUsername
	}
	if request.ProxyPassword != nil {
		current.ProxyPassword = *request.ProxyPassword
	}

	// ================================
	// 5️⃣ Persistir no banco
	// ================================
	updated, err := s.repo.Update(c, request.ID, &current)
	if err != nil {
		return utils.HTTPFail(
			ctx,
			http.StatusInternalServerError,
			err,
			"failed to update instance",
		)
	}

	// ================================
	// 6️⃣ Resposta
	// ================================
	return ctx.JSON(
		http.StatusOK,
		dto.UpdateInstanceResponse{
			Instance: updated,
		},
	)
}

func (s *Instance) List(ctx echo.Context) error {
	c := ctx.Request().Context()
	var request dto.ListInstancesRequest
	if err := ctx.Bind(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}
	if request.InstanceName == "" {
		request.InstanceName = request.ID
	}

	result, err := s.repo.List(c, request.InstanceName)
	if err != nil {
		zap.L().Error("failed to list instances", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to list instances")
	}

	var response []dto.ListInstancesResponse
	for _, instance := range result {
		jid, err := types.ParseJID(instance.RemoteJID)
		if err != nil {
			zap.L().Error("failed to parse jid", zap.Error(err))
		}

		response = append(response, dto.ListInstancesResponse{
			Instance:     &instance,
			OwnerJID:     jid.ToNonAD().String(),
			InstanceName: instance.ID,
		})
	}

	if len(response) == 0 {
		return ctx.JSON(http.StatusOK, []string{})
	}

	return ctx.JSON(http.StatusOK, response)
}

func (s *Instance) Connect(ctx echo.Context) error {
	c := ctx.Request().Context()
	var request dto.ConnectInstanceRequest
	if err := ctx.Bind(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}

	result, err := s.repo.List(c, request.ID)
	if err != nil {
		zap.L().Error("failed to list instances", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to list instances")
	}

	if len(result) == 0 {
		return utils.HTTPFail(ctx, http.StatusNotFound, err, "instance not found")
	}

	qrCode, err := s.whatsmiau.Connect(c, request.ID)
	if err != nil {
		zap.L().Error("failed to connect instance", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to connect instance")
	}
	if qrCode != "" {
		png, err := qrcode.Encode(qrCode, qrcode.Medium, 512)
		if err != nil {
			zap.L().Error("failed to encode qrcode", zap.Error(err))
			return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to encode qrcode")
		}
		return ctx.JSON(http.StatusOK, dto.ConnectInstanceResponse{
			Message:   "If instance restart this instance could be lost if you cannot connect",
			Connected: false,
			Base64:    "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		})
	}

	return ctx.JSON(http.StatusOK, dto.ConnectInstanceResponse{
		Message:   "instance already connected",
		Connected: true,
	})
}

func (s *Instance) ConnectQRBuffer(ctx echo.Context) error {
	c := ctx.Request().Context()
	var request dto.ConnectInstanceRequest
	if err := ctx.Bind(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}

	result, err := s.repo.List(c, request.ID)
	if err != nil {
		zap.L().Error("failed to list instances", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to list instances")
	}

	if len(result) == 0 {
		return utils.HTTPFail(ctx, http.StatusNotFound, err, "instance not found")
	}

	qrCode, err := s.whatsmiau.Connect(c, request.ID)
	if err != nil {
		zap.L().Error("failed to connect instance", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to connect instance")
	}
	if qrCode != "" {
		png, err := qrcode.Encode(qrCode, qrcode.Medium, 256)
		if err != nil {
			zap.L().Error("failed to encode qrcode", zap.Error(err))
			return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to encode qrcode")
		}
		return ctx.Blob(http.StatusOK, "image/png", png)
	}

	return ctx.NoContent(http.StatusOK)
}

func (s *Instance) Status(ctx echo.Context) error {
	c := ctx.Request().Context()
	var request dto.ConnectInstanceRequest
	if err := ctx.Bind(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}

	result, err := s.repo.List(c, request.ID)
	if err != nil {
		zap.L().Error("failed to list instances", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to list instances")
	}

	if len(result) == 0 {
		return utils.HTTPFail(ctx, http.StatusNotFound, err, "instance not found")
	}

	status, err := s.whatsmiau.Status(request.ID)
	if err != nil {
		zap.L().Error("failed to get status instance", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to get status instance")
	}

	return ctx.JSON(http.StatusOK, dto.StatusInstanceResponse{
		ID:     request.ID,
		Status: string(status),
		Instance: &dto.StatusInstanceResponseEvolutionCompatibility{
			InstanceName: request.ID,
			State:        string(status),
		},
	})
}

func (s *Instance) Logout(ctx echo.Context) error {
	c := ctx.Request().Context()
	var request dto.DeleteInstanceRequest
	if err := ctx.Bind(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}

	result, err := s.repo.List(c, request.ID)
	if err != nil {
		zap.L().Error("failed to list instances", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to list instances")
	}

	if len(result) == 0 {
		return utils.HTTPFail(ctx, http.StatusNotFound, err, "instance not found")
	}

	if err := s.whatsmiau.Logout(c, request.ID); err != nil {
		zap.L().Error("failed to logout instance", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to logout instance")
	}

	return ctx.JSON(http.StatusOK, dto.DeleteInstanceResponse{
		Message: "instance logout successfully",
	})
}

func (s *Instance) Delete(ctx echo.Context) error {
	c := ctx.Request().Context()
	var request dto.DeleteInstanceRequest
	if err := ctx.Bind(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}

	result, err := s.repo.List(c, request.ID)
	if err != nil {
		zap.L().Error("failed to list instances", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to list instances")
	}

	if len(result) == 0 {
		return ctx.JSON(http.StatusOK, dto.DeleteInstanceResponse{
			Message: "instance doesn't exists",
		})
	}

	if err := s.whatsmiau.Logout(ctx.Request().Context(), request.ID); err != nil {
		zap.L().Error("failed to disconnect instance", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to logout instance")
	}

	if err := s.repo.Delete(c, request.ID); err != nil {
		zap.L().Error("failed to delete instance", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to delete instance")
	}

	return ctx.JSON(http.StatusOK, dto.DeleteInstanceResponse{
		Message: "instance deleted",
	})
}
