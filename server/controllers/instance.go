package controllers

import (
	"encoding/base64"
	"net/http"
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"github.com/verbeux-ai/whatsmiau/models"
	"github.com/verbeux-ai/whatsmiau/env"
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
	var request dto.CreateInstanceRequest

	if err := ctx.Bind(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}

	if err := validator.New().Struct(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusBadRequest, err, "invalid request body")
	}

	instance := models.Instance{
		ID:          request.InstanceName,
		Name:        request.Name,
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
		SGPEnabled:      request.SGPEnabled,
		SGPAllowedIPs:   request.SGPAllowedIPs,
		SGPSyncChatwoot: request.SGPSyncChatwoot,

		Webhook: request.Webhook,

		// Chatwoot — deref ponteiros → value types do model
		ChatwootAccountID:               derefString(request.ChatwootAccountID),
		ChatwootToken:                   derefString(request.ChatwootToken),
		ChatwootURL:                     derefString(request.ChatwootURL),
		ChatwootSignMsg:                 derefBool(request.ChatwootSignMsg),
		ChatwootReopenConversation:      derefBool(request.ChatwootReopenConversation),
		ChatwootConversationPending:     derefBool(request.ChatwootConversationPending),
		ChatwootImportContacts:          derefBool(request.ChatwootImportContacts),
		ChatwootNameInbox:               derefString(request.ChatwootNameInbox),
		ChatwootMergeBrazilContacts:     derefBool(request.ChatwootMergeBrazilContacts),
		ChatwootImportMessages:          derefBool(request.ChatwootImportMessages),
		ChatwootDaysLimitImportMessages: derefInt(request.ChatwootDaysLimitImportMessages),
		ChatwootOrganization:            derefString(request.ChatwootOrganization),
		ChatwootLogo:                    derefString(request.ChatwootLogo),

		// Proxy
		InstanceProxy: models.InstanceProxy{
			ProxyHost:     request.ProxyHost,
			ProxyPort:     request.ProxyPort,
			ProxyProtocol: request.ProxyProtocol,
			ProxyUsername: request.ProxyUsername,
			ProxyPassword: request.ProxyPassword,
		},
	}

	// Auto-generate SGPToken if SGP enabled and no token provided
	sgpToken := request.SGPToken
	if instance.SGPEnabled && sgpToken == "" {
		sgpToken = uuid.New().String()
	}
	instance.SGPToken = sgpToken

	c := ctx.Request().Context()

	if instance.ChatwootURL != "" && instance.ChatwootToken != "" && instance.ChatwootAccountID != "" {
   		if instance.Webhook == nil {
     	   instance.Webhook = &models.InstanceWebhook{}
    	}
    	if instance.Webhook.Base64 == nil {
     	   t := true
     	   instance.Webhook.Base64 = &t
    	}
	}

	if err := s.repo.Create(c, &instance); err != nil {
    zap.L().Error("failed to create instance", zap.Error(err))
    return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to create instance")
}

// ADICIONAR AQUI ↓
if instance.ChatwootURL != "" && instance.ChatwootToken != "" && instance.ChatwootAccountID != "" {
    go func() {
        svc := whatsmiau.NewChatwootService(whatsmiau.ChatwootConfig{
            URL:       instance.ChatwootURL,
            AccountID: instance.ChatwootAccountID,
            Token:     instance.ChatwootToken,
        })

        inboxName := instance.ChatwootNameInbox
        if inboxName == "" {
            inboxName = instance.ID
        }

        webhookURL := fmt.Sprintf("%s/webhook/chatwoot/%s", env.Env.GetServerURL(), instance.ID)

        inboxID, err := svc.InitInstance(
            context.Background(),
            inboxName,
            webhookURL,
            instance.ChatwootOrganization,
            instance.ChatwootLogo,
        )
        if err != nil {
            zap.L().Error("chatwoot: falha ao criar inbox", zap.Error(err))
            return
        }
        zap.L().Info("chatwoot: ✅ inbox criada", zap.Int("inboxId", inboxID))
    }()
}
// FIM DA ADIÇÃO ↑

return ctx.JSON(http.StatusCreated, dto.CreateInstanceResponse{
    Instance: instance,
})
}

func (s *Instance) Update(ctx echo.Context) error {
	// ================================
	// Bind em duas etapas — evita bug do Echo
	// onde param:"id" + json body no mesmo struct
	// faz o body ser ignorado
	// ================================
	id := ctx.Param("id")
	if id == "" {
		return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "missing instance id in url")
	}

	var request dto.UpdateInstanceRequest
	if err := ctx.Bind(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}

	// Força ID da URL (nunca do body)
	request.ID = id

	if err := validator.New().Struct(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusBadRequest, err, "invalid request body")
	}

	c := ctx.Request().Context()

	// Buscar instância atual
	currentList, err := s.repo.List(c, request.ID)
	if err != nil {
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to list instances")
	}
	if len(currentList) == 0 {
		return utils.HTTPFail(ctx, http.StatusNotFound, nil, "instance not found")
	}

	current := &currentList[0]

	// ================================
	// Atualizações parciais (PATCH)
	// ================================

	// ---------- Webhook ----------
	if request.Webhook != nil {
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

	// ---------- Name ----------
	if request.Name != nil {
		current.Name = *request.Name
	}

	// ---------- Behavioral ----------
	if request.RejectCall != nil {
		current.RejectCall = *request.RejectCall
	}
	if request.MsgCall != nil {
		current.MsgCall = *request.MsgCall
	}
	if request.GroupsIgnore != nil {
		current.GroupsIgnore = *request.GroupsIgnore
	}
	if request.AlwaysOnline != nil {
		current.AlwaysOnline = *request.AlwaysOnline
	}
	if request.ReadMessages != nil {
		current.ReadMessages = *request.ReadMessages
	}
	if request.ReadStatus != nil {
		current.ReadStatus = *request.ReadStatus
	}
	if request.SyncFullHistory != nil {
		current.SyncFullHistory = *request.SyncFullHistory
	}
	if request.SGPEnabled != nil {
		current.SGPEnabled = *request.SGPEnabled
	}
	if request.SGPToken != nil {
		if *request.SGPToken == "" && current.SGPEnabled {
			// regenerate
			current.SGPToken = uuid.New().String()
		} else if *request.SGPToken != "" {
			current.SGPToken = *request.SGPToken
		}
	}
	// auto-generate token if SGP just enabled and no token yet
	if current.SGPEnabled && current.SGPToken == "" {
		current.SGPToken = uuid.New().String()
	}
	if request.SGPAllowedIPs != nil {
		current.SGPAllowedIPs = *request.SGPAllowedIPs
	}
	if request.SGPSyncChatwoot != nil {
		current.SGPSyncChatwoot = *request.SGPSyncChatwoot
	}

	// ---------- Chatwoot ----------
	if request.ChatwootAccountID != nil {
		current.ChatwootAccountID = *request.ChatwootAccountID
	}
	if request.ChatwootToken != nil {
		current.ChatwootToken = *request.ChatwootToken
	}
	if request.ChatwootURL != nil {
		current.ChatwootURL = *request.ChatwootURL
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

	_, err = s.repo.Update(c, request.ID, current)
	if err != nil {
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to update instance")
	}

	updatedList, err := s.repo.List(c, request.ID)
	if err != nil {
		zap.L().Error("failed to fetch updated instance", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to fetch updated instance")
	}
	if len(updatedList) == 0 {
		return utils.HTTPFail(ctx, http.StatusNotFound, nil, "instance not found after update")
	}

	return ctx.JSON(http.StatusOK, dto.UpdateInstanceResponse{
		Instance: &updatedList[0],
	})
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

		connStatus, _ := s.whatsmiau.Status(instance.ID)

		var chatwoot *dto.ChatwootInstanceInfo
		if instance.ChatwootURL != "" || instance.ChatwootToken != "" {
			chatwoot = &dto.ChatwootInstanceInfo{
				Enabled:                 instance.ChatwootURL != "" && instance.ChatwootToken != "",
				AccountID:               instance.ChatwootAccountID,
				Token:                   instance.ChatwootToken,
				URL:                     instance.ChatwootURL,
				NameInbox:               instance.ChatwootNameInbox,
				SignMsg:                 instance.ChatwootSignMsg,
				ReopenConversation:      instance.ChatwootReopenConversation,
				ConversationPending:     instance.ChatwootConversationPending,
				MergeBrazilContacts:     instance.ChatwootMergeBrazilContacts,
				ImportContacts:          instance.ChatwootImportContacts,
				ImportMessages:          instance.ChatwootImportMessages,
				DaysLimitImportMessages: instance.ChatwootDaysLimitImportMessages,
				Organization:            instance.ChatwootOrganization,
				Logo:                    instance.ChatwootLogo,
				IgnoreJids:              []string{},
			}
		}

		sgpInfo := &dto.SGPInstanceInfo{
			Enabled:      instance.SGPEnabled,
			Token:        instance.SGPToken,
			AllowedIPs:   instance.SGPAllowedIPs,
			SyncChatwoot: instance.SGPSyncChatwoot,
		}

		response = append(response, dto.ListInstancesResponse{
			InstanceBaseInfo: dto.InstanceBaseInfo{
				ID:              instance.ID,
				Name:            instance.Name,
				Integration:     instance.Integration,
				Token:           instance.Token,
				QRCode:          instance.QRCode,
				Number:          instance.Number,
				RejectCall:      instance.RejectCall,
				MsgCall:         instance.MsgCall,
				GroupsIgnore:    instance.GroupsIgnore,
				AlwaysOnline:    instance.AlwaysOnline,
				ReadMessages:    instance.ReadMessages,
				ReadStatus:      instance.ReadStatus,
				SyncFullHistory: instance.SyncFullHistory,
				RemoteJID:       instance.RemoteJID,
				Webhook:         instance.Webhook,
				ProxyHost:       instance.ProxyHost,
				ProxyPort:       instance.ProxyPort,
				ProxyProtocol:   instance.ProxyProtocol,
				ProxyUsername:   instance.ProxyUsername,
				ProxyPassword:   instance.ProxyPassword,
			},
			OwnerJID:         jid.ToNonAD().String(),
			InstanceName:     instance.ID,
			Name:             instance.ID,
			ConnectionStatus: string(connStatus),
			Chatwoot:         chatwoot,
			SGP:              sgpInfo,
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

func (s *Instance) Restart(ctx echo.Context) error {
	c := ctx.Request().Context()
	id := ctx.Param("id")
	if id == "" {
		return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "missing instance id")
	}

	result, err := s.repo.List(c, id)
	if err != nil || len(result) == 0 {
		return utils.HTTPFail(ctx, http.StatusNotFound, nil, "instance not found")
	}

	if err := s.whatsmiau.Restart(id); err != nil {
		zap.L().Error("failed to restart instance", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to restart instance")
	}

	return ctx.JSON(http.StatusOK, map[string]string{"message": "instance restarted successfully"})
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

// ================================
// Helpers para deref de ponteiros
// ================================

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}
