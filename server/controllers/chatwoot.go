package controllers

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/verbeux-ai/whatsmiau/interfaces"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"github.com/verbeux-ai/whatsmiau/server/dto"
	"github.com/verbeux-ai/whatsmiau/utils"
	"go.mau.fi/whatsmeow/types"
	"go.uber.org/zap"
)

type Chatwoot struct {
	repo      interfaces.InstanceRepository
	whatsmiau *whatsmiau.Whatsmiau
}

func NewChatwoot(repository interfaces.InstanceRepository, whatsmiau *whatsmiau.Whatsmiau) *Chatwoot {
	return &Chatwoot{
		repo:      repository,
		whatsmiau: whatsmiau,
	}
}

// ChatwootWebhookBody representa o payload recebido do Chatwoot
type ChatwootWebhookBody struct {
	Event        string `json:"event"`
	MessageType  string `json:"message_type"`
	Content      string `json:"content"`
	ContentType  string `json:"content_type"`
	Private      bool   `json:"private"`
	Conversation struct {
		Meta struct {
			Sender struct {
				PhoneNumber string `json:"phone_number"`
			} `json:"sender"`
		} `json:"meta"`
		ContactInbox struct {
			SourceID string `json:"source_id"`
		} `json:"contact_inbox"`
	} `json:"conversation"`
	Inbox struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"inbox"`
}

func (s *Chatwoot) ReceiveWebhook(ctx echo.Context) error {
	instanceID := ctx.Param("instance")
	if instanceID == "" {
		return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "instance parameter is required")
	}

	var webhook ChatwootWebhookBody
	if err := ctx.Bind(&webhook); err != nil {
		zap.L().Error("failed to bind chatwoot webhook", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}

	// Log do evento recebido
	zap.L().Info("chatwoot webhook received",
		zap.String("instance", instanceID),
		zap.String("event", webhook.Event),
		zap.String("message_type", webhook.MessageType),
		zap.String("content_type", webhook.ContentType),
	)

	// Processar apenas eventos de mensagem criada do tipo outgoing (enviada pelo agente)
	if webhook.Event != "message_created" || webhook.MessageType != "outgoing" {
		return ctx.JSON(http.StatusOK, map[string]string{
			"status": "ignored",
			"reason": "not an outgoing message",
		})
	}

	// Ignorar mensagens privadas (notas internas)
	if webhook.Private {
		return ctx.JSON(http.StatusOK, map[string]string{
			"status": "ignored",
			"reason": "private message",
		})
	}

	// Processar baseado no tipo de conteúdo
	switch webhook.ContentType {
	case "text":
		return s.handleTextMessage(ctx, instanceID, webhook)
	default:
		zap.L().Warn("unsupported content type",
			zap.String("content_type", webhook.ContentType),
		)
		return ctx.JSON(http.StatusOK, map[string]string{
			"status": "ignored",
			"reason": "unsupported content type",
		})
	}
}

func (s *Chatwoot) handleTextMessage(ctx echo.Context, instanceID string, webhook ChatwootWebhookBody) error {
	// Extrair número do telefone
	phoneNumber := webhook.Conversation.Meta.Sender.PhoneNumber
	if phoneNumber == "" {
		return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "phone number not found in webhook")
	}

	// Converter número para JID
	jid, err := numberToJid(phoneNumber)
	if err != nil {
		zap.L().Error("error converting number to jid", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusBadRequest, err, "invalid number format")
	}

	// Enviar indicador de "digitando"
	if err := s.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
		InstanceID: instanceID,
		RemoteJID:  jid,
		Presence:   types.ChatPresenceComposing,
	}); err != nil {
		zap.L().Error("Whatsmiau.ChatPresence", zap.Error(err))
	} else {
		// Delay simulando digitação (você pode ajustar ou remover)
		time.Sleep(time.Millisecond * 500)
	}

	// Preparar e enviar mensagem
	sendText := &whatsmiau.SendText{
		Text:       webhook.Content,
		InstanceID: instanceID,
		RemoteJID:  jid,
	}

	c := ctx.Request().Context()
	res, err := s.whatsmiau.SendText(c, sendText)
	if err != nil {
		zap.L().Error("Whatsmiau.SendText failed", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusInternalServerError, err, "failed to send text")
	}

	// Enviar indicador de "parou de digitar"
	if err := s.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
		InstanceID: instanceID,
		RemoteJID:  jid,
		Presence:   types.ChatPresencePaused,
	}); err != nil {
		zap.L().Error("Whatsmiau.ChatPresence pause", zap.Error(err))
	}

	return ctx.JSON(http.StatusOK, dto.SendTextResponse{
		Key: dto.MessageResponseKey{
			RemoteJid: phoneNumber,
			FromMe:    true,
			Id:        res.ID,
		},
		Status: "sent",
		Message: dto.SendTextResponseMessage{
			Conversation: webhook.Content,
		},
		MessageType:      "conversation",
		MessageTimestamp: int(res.CreatedAt.Unix() / 1000),
		InstanceId:       instanceID,
	})
}
