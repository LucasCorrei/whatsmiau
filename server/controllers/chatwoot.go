package controllers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
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

func (c *Chatwoot) ReceiveWebhook(ctx echo.Context) error {
	instanceName := ctx.Param("instance")
	if instanceName == "" {
		return utils.HTTPFail(ctx, http.StatusBadRequest, nil, "instance name is required")
	}

	var webhook dto.ChatwootWebhookRequest
	if err := ctx.Bind(&webhook); err != nil {
		zap.L().Error("failed to bind chatwoot webhook", zap.Error(err))
		return utils.HTTPFail(ctx, http.StatusUnprocessableEntity, err, "failed to bind request body")
	}

	zap.L().Info("chatwoot webhook received",
		zap.String("instance", instanceName),
		zap.String("event", webhook.Event),
		zap.Int64("conversation_id", webhook.Conversation.ID),
	)

	// Ignora mensagens privadas
	if webhook.Private || webhook.IsPrivate {
		return ctx.JSON(http.StatusOK, map[string]string{"message": "private message ignored"})
	}

	// Processa apenas mensagens outgoing (do agente para o cliente)
	switch webhook.Event {
	case "conversation_typing_on":
		return c.handleTypingOn(ctx, instanceName, &webhook)
	
	case "message_created":
		if webhook.MessageType == "outgoing" {
			return c.handleOutgoingMessage(ctx, instanceName, &webhook)
		}
	
	case "conversation_typing_off":
		return c.handleTypingOff(ctx, instanceName, &webhook)
	
	case "message_updated":
		zap.L().Debug("message_updated event received", zap.Int64("message_id", webhook.ID))
	
	default:
		zap.L().Debug("unhandled chatwoot event", zap.String("event", webhook.Event))
	}

	return ctx.JSON(http.StatusOK, map[string]string{"message": "ok"})
}

func (c *Chatwoot) handleTypingOn(ctx echo.Context, instanceName string, webhook *dto.ChatwootWebhookRequest) error {
	jid, err := c.getJIDFromWebhook(webhook)
	if err != nil {
		zap.L().Error("failed to get JID from webhook", zap.Error(err))
		return ctx.JSON(http.StatusOK, map[string]string{"message": "invalid jid"})
	}

	instanceID, err := c.getInstanceID(ctx.Request().Context(), instanceName)
	if err != nil {
		zap.L().Error("instance not found", zap.Error(err), zap.String("instance", instanceName))
		return ctx.JSON(http.StatusOK, map[string]string{"message": "instance not found"})
	}

	if err := c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
		InstanceID: instanceID,
		RemoteJID:  &jid,
		Presence:   types.ChatPresenceComposing,
	}); err != nil {
		zap.L().Error("failed to send typing presence", zap.Error(err))
	}

	return ctx.JSON(http.StatusOK, map[string]string{"message": "typing on"})
}

func (c *Chatwoot) handleTypingOff(ctx echo.Context, instanceName string, webhook *dto.ChatwootWebhookRequest) error {
	jid, err := c.getJIDFromWebhook(webhook)
	if err != nil {
		zap.L().Error("failed to get JID from webhook", zap.Error(err))
		return ctx.JSON(http.StatusOK, map[string]string{"message": "invalid jid"})
	}

	instanceID, err := c.getInstanceID(ctx.Request().Context(), instanceName)
	if err != nil {
		zap.L().Error("instance not found", zap.Error(err))
		return ctx.JSON(http.StatusOK, map[string]string{"message": "instance not found"})
	}

	if err := c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
		InstanceID: instanceID,
		RemoteJID:  &jid,
		Presence:   types.ChatPresencePaused,
	}); err != nil {
		zap.L().Error("failed to send paused presence", zap.Error(err))
	}

	return ctx.JSON(http.StatusOK, map[string]string{"message": "typing off"})
}

func (c *Chatwoot) handleOutgoingMessage(ctx echo.Context, instanceName string, webhook *dto.ChatwootWebhookRequest) error {
	jid, err := c.getJIDFromWebhook(webhook)
	if err != nil {
		zap.L().Error("failed to get JID from webhook", zap.Error(err))
		return ctx.JSON(http.StatusOK, map[string]string{"error": "invalid jid"})
	}

	instanceID, err := c.getInstanceID(ctx.Request().Context(), instanceName)
	if err != nil {
		zap.L().Error("instance not found", zap.Error(err))
		return ctx.JSON(http.StatusOK, map[string]string{"error": "instance not found"})
	}

	// 1. Marca presença como "composing"
	if err := c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
		InstanceID: instanceID,
		RemoteJID:  &jid,
		Presence:   types.ChatPresenceComposing,
	}); err != nil {
		zap.L().Error("failed to send composing presence", zap.Error(err))
	}

	// Delay simulando digitação (500-2000ms)
	delayMs := 500 + len(webhook.Content)
	if delayMs > 2000 {
		delayMs = 2000
	}
	time.Sleep(time.Millisecond * time.Duration(delayMs))

	requestContext := ctx.Request().Context()

	// 2. Envia a mensagem
	var sendErr error
	
	// Verifica se tem anexos
	if len(webhook.Conversation.Messages) > 0 && len(webhook.Conversation.Messages[0].Attachments) > 0 {
		sendErr = c.sendAttachments(requestContext, instanceID, &jid, webhook)
	} else if webhook.Content != "" {
		// Envia texto
		sendErr = c.sendTextMessage(requestContext, instanceID, &jid, webhook.Content)
	}

	// 3. Remove presença (paused)
	if err := c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
		InstanceID: instanceID,
		RemoteJID:  &jid,
		Presence:   types.ChatPresencePaused,
	}); err != nil {
		zap.L().Error("failed to send paused presence", zap.Error(err))
	}

	if sendErr != nil {
		zap.L().Error("failed to send message", zap.Error(sendErr))
		return ctx.JSON(http.StatusOK, map[string]string{"error": sendErr.Error()})
	}

	return ctx.JSON(http.StatusOK, map[string]string{"message": "sent"})
}

// getInstanceID busca o ID da instância usando o método List do repositório
func (c *Chatwoot) getInstanceID(ctx context.Context, instanceName string) (string, error) {
	instances, err := c.repo.List(ctx, instanceName)
	if err != nil {
		return "", fmt.Errorf("failed to list instances: %w", err)
	}

	if len(instances) == 0 {
		return "", fmt.Errorf("instance %s not found", instanceName)
	}

	// Retorna o ID da primeira instância encontrada
	return instances[0].ID, nil
}

func (c *Chatwoot) sendTextMessage(ctx context.Context, instanceID string, jid *types.JID, content string) error {
	formattedContent := c.convertChatwootToWhatsAppFormatting(content)

	_, err := c.whatsmiau.SendText(ctx, &whatsmiau.SendText{
		InstanceID: instanceID,
		RemoteJID:  *jid,
		Text:       formattedContent,
	})

	return err
}

func (c *Chatwoot) sendAttachments(ctx context.Context, instanceID string, jid *types.JID, webhook *dto.ChatwootWebhookRequest) error {
	for _, message := range webhook.Conversation.Messages {
		for _, attachment := range message.Attachments {
			var err error
			
			switch {
			case strings.HasPrefix(attachment.FileType, "image/"):
				err = c.sendImage(ctx, instanceID, *jid, attachment.DataURL, webhook.Content)
			
			case strings.HasPrefix(attachment.FileType, "audio/"):
				err = c.sendAudio(ctx, instanceID, *jid, attachment.DataURL)
			
			default:
				err = c.sendDocument(ctx, instanceID, *jid, attachment.DataURL, webhook.Content, attachment.FileType)
			}

			if err != nil {
				return err
			}
		}
	}
	
	return nil
}

func (c *Chatwoot) sendImage(ctx context.Context, instanceID string, jid types.JID, imageURL, caption string) error {
	_, err := c.whatsmiau.SendImage(ctx, &whatsmiau.SendImageRequest{
		InstanceID: instanceID,
		RemoteJID:  jid,
		MediaURL:   imageURL,
		Caption:    caption,
	})
	return err
}

func (c *Chatwoot) sendAudio(ctx context.Context, instanceID string, jid types.JID, audioURL string) error {
	_, err := c.whatsmiau.SendAudio(ctx, &whatsmiau.SendAudioRequest{
		InstanceID: instanceID,
		RemoteJID:  jid,
		AudioURL:   audioURL,
	})
	return err
}

func (c *Chatwoot) sendDocument(ctx context.Context, instanceID string, jid types.JID, documentURL, caption, mimetype string) error {
	_, err := c.whatsmiau.SendDocument(ctx, &whatsmiau.SendDocumentRequest{
		InstanceID: instanceID,
		RemoteJID:  jid,
		MediaURL:   documentURL,
		Caption:    caption,
		Mimetype:   mimetype,
	})
	return err
}

func (c *Chatwoot) getJIDFromWebhook(webhook *dto.ChatwootWebhookRequest) (types.JID, error) {
	var identifier string

	// Tenta pegar do meta.sender.identifier
	if webhook.Conversation.Meta.Sender.Identifier != "" {
		identifier = webhook.Conversation.Meta.Sender.Identifier
	} else if webhook.Conversation.ContactInbox.SourceID != "" {
		identifier = webhook.Conversation.ContactInbox.SourceID
	} else if webhook.Conversation.Meta.Sender.PhoneNumber != "" {
		phoneNumber := strings.TrimPrefix(webhook.Conversation.Meta.Sender.PhoneNumber, "+")
		identifier = fmt.Sprintf("%s@s.whatsapp.net", phoneNumber)
	}

	if identifier == "" {
		return types.JID{}, fmt.Errorf("no identifier found in webhook")
	}

	jid, err := types.ParseJID(identifier)
	if err != nil {
		return types.JID{}, fmt.Errorf("failed to parse JID: %w", err)
	}

	return jid, nil
}

func (c *Chatwoot) convertChatwootToWhatsAppFormatting(content string) string {
	// Converte **bold** do Chatwoot para *bold* do WhatsApp
	content = strings.ReplaceAll(content, "**", "*")
	
	// Converte `code` para ```code```
	// TODO: Implementar conversão mais robusta de markdown
	
	return content
}
