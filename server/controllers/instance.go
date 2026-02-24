package controllers

import (
	"encoding/base64"
	"errors"
	"math/rand/v2"
	"net/http"

	"github.com/verbeux-ai/whatsmiau/env"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"github.com/verbeux-ai/whatsmiau/models"
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

	// ==============================
	// Proxy automático
	// ==============================
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

	// ==============================
	// CHATWOOT - Usando configs da INSTÂNCIA
	// ==============================
	if request.Instance.ChatwootEnabled {
		// Validação: verificar se os campos obrigatórios estão preenchidos
		if request.Instance.ChatwootUrl == "" {
			return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "chatwootUrl is required when chatwoot is enabled")
		}
		if request.Instance.ChatwootToken == "" {
			return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "chatwootToken is required when chatwoot is enabled")
		}
		if request.Instance.ChatwootAccountId == 0 {
			return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "chatwootAccountId is required when chatwoot is enabled")
		}

		// Criar/verificar inbox no Chatwoot usando as configurações DA INSTÂNCIA
		inboxID, err := s.whatsmiau.ChatwootService.EnsureInbox(request.Instance)
		if err != nil {
			zap.L().Error("failed to ensure chatwoot inbox", 
				zap.Error(err),
				zap.String("instance", instanceID),
				zap.String("chatwootUrl", request.Instance.ChatwootUrl),
			)
			return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to ensure chatwoot inbox")
		}
		
		request.Instance.ChatwootInboxId = inboxID
		
		zap.L().Info("chatwoot inbox created/verified",
			zap.String("instance", instanceID),
			zap.String("inboxId", inboxID),
			zap.String("chatwootUrl", request.Instance.ChatwootUrl),
		)
	}

	// Salvar instância no banco
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

	// Buscar instância existente usando List (padrão do projeto)
	instances, err := s.repo.List(ctx.Request().Context(), request.ID)
	if err != nil {
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to get instance")
	}
	if len(instances) == 0 {
		return utils.HTTPFail(ctx, http.StatusNotFound, nil, "instance not found")
	}

	instance := instances[0]

	// ==============================
	// Atualizar WEBHOOK
	// ==============================
	if request.Webhook != nil {
		if instance.Webhook == nil {
			instance.Webhook = &models.InstanceWebhook{}
		}
		if request.Webhook.Url != "" {
			instance.Webhook.Url = request.Webhook.Url
		}
		if request.Webhook.Events != nil {
			instance.Webhook.Events = request.Webhook.Events
		}
		if request.Webhook.ByEvents != nil {
			instance.Webhook.ByEvents = request.Webhook.ByEvents
		}
		if request.Webhook.Base64 != nil {
			instance.Webhook.Base64 = request.Webhook.Base64
		}
		if request.Webhook.Headers != nil {
			instance.Webhook.Headers = request.Webhook.Headers
		}
	}

	// ==============================
	// Atualizar RABBITMQ
	// ==============================
	if request.RabbitMQ != nil {
		if instance.RabbitMQ == nil {
			instance.RabbitMQ = &models.InstanceBroker{}
		}
		instance.RabbitMQ.Enabled = request.RabbitMQ.Enabled
		if request.RabbitMQ.Events != nil {
			instance.RabbitMQ.Events = request.RabbitMQ.Events
		}
	}

	// ==============================
	// Atualizar SQS
	// ==============================
	if request.SQS != nil {
		if instance.SQS == nil {
			instance.SQS = &models.InstanceBroker{}
		}
		instance.SQS.Enabled = request.SQS.Enabled
		if request.SQS.Events != nil {
			instance.SQS.Events = request.SQS.Events
		}
	}

	// ==============================
	// Atualizar CHATWOOT - Usando ponteiros para detectar mudanças
	// ==============================
	chatwootUpdated := false

	if request.ChatwootEnabled != nil {
		instance.ChatwootEnabled = *request.ChatwootEnabled
		chatwootUpdated = true
	}
	if request.ChatwootUrl != nil {
		instance.ChatwootUrl = *request.ChatwootUrl
		chatwootUpdated = true
	}
	if request.ChatwootToken != nil {
		instance.ChatwootToken = *request.ChatwootToken
		chatwootUpdated = true
	}
	if request.ChatwootAccountId != nil {
		instance.ChatwootAccountId = *request.ChatwootAccountId
		chatwootUpdated = true
	}
	if request.ChatwootNameInbox != nil {
		instance.ChatwootNameInbox = *request.ChatwootNameInbox
		chatwootUpdated = true
	}
	if request.ChatwootSignMsg != nil {
		instance.ChatwootSignMsg = *request.ChatwootSignMsg
	}
	if request.ChatwootReopenConversation != nil {
		instance.ChatwootReopenConversation = *request.ChatwootReopenConversation
	}
	if request.ChatwootConversationPending != nil {
		instance.ChatwootConversationPending = *request.ChatwootConversationPending
	}
	if request.ChatwootImportContacts != nil {
		instance.ChatwootImportContacts = *request.ChatwootImportContacts
	}
	if request.ChatwootMergeBrazilContacts != nil {
		instance.ChatwootMergeBrazilContacts = *request.ChatwootMergeBrazilContacts
	}
	if request.ChatwootImportMessages != nil {
		instance.ChatwootImportMessages = *request.ChatwootImportMessages
	}
	if request.ChatwootDaysLimitImportMessages != nil {
		instance.ChatwootDaysLimitImportMessages = *request.ChatwootDaysLimitImportMessages
	}
	if request.ChatwootOrganization != nil {
		instance.ChatwootOrganization = *request.ChatwootOrganization
	}
	if request.ChatwootLogo != nil {
		instance.ChatwootLogo = *request.ChatwootLogo
	}

	// Se Chatwoot foi atualizado E está habilitado, recriar/verificar inbox
	if chatwootUpdated && instance.ChatwootEnabled {
		// Validação
		if instance.ChatwootUrl == "" {
			return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "chatwootUrl is required when chatwoot is enabled")
		}
		if instance.ChatwootToken == "" {
			return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "chatwootToken is required when chatwoot is enabled")
		}
		if instance.ChatwootAccountId == 0 {
			return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "chatwootAccountId is required when chatwoot is enabled")
		}

		inboxID, err := s.whatsmiau.ChatwootService.EnsureInbox(&instance)
		if err != nil {
			zap.L().Error("failed to ensure chatwoot inbox on update",
				zap.Error(err),
				zap.String("instance", request.ID),
			)
			return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to ensure chatwoot inbox")
		}
		instance.ChatwootInboxId = inboxID

		zap.L().Info("chatwoot inbox updated",
			zap.String("instance", request.ID),
			zap.String("inboxId", inboxID),
		)
	}

	// Salvar instância atualizada usando Update do Redis (padrão do projeto)
	updatedInstance, err := s.repo.Update(ctx.Request().Context(), request.ID, &instance)
	if err != nil {
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to update instance")
	}

	return ctx.JSON(http.StatusOK, dto.UpdateInstanceResponse{
		Instance: updatedInstance,
	})
}
