package controllers

import (
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

	if webhook.Private || webhook.IsPrivate {
		return ctx.JSON(http.StatusOK, map[string]string{"message": "private ignored"})
	}

	switch webhook.Event {

	case "conversation_typing_on":
		return c.handleTyping(ctx, instanceName, &webhook, types.ChatPresenceComposing)

	case "conversation_typing_off":
		return c.handleTyping(ctx, instanceName, &webhook, types.ChatPresencePaused)

	case "message_created":
		if webhook.MessageType == "outgoing" {
			return c.handleOutgoing(ctx, instanceName, &webhook)
		}
	}

	return ctx.JSON(http.StatusOK, map[string]string{"message": "ok"})
}

func (c *Chatwoot) handleTyping(ctx echo.Context, instanceName string, webhook *dto.ChatwootWebhookRequest, presence types.ChatPresence) error {
	instanceID, jid, err := c.resolveInstanceAndJID(ctx, instanceName, webhook)
	if err != nil {
		return ctx.JSON(http.StatusOK, map[string]string{"error": err.Error()})
	}

	_ = c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
		InstanceID: instanceID,
		RemoteJID:  jid,
		Presence:   presence,
	})

	return ctx.JSON(http.StatusOK, map[string]string{"message": "presence updated"})
}

func (c *Chatwoot) handleOutgoing(ctx echo.Context, instanceName string, webhook *dto.ChatwootWebhookRequest) error {
	instanceID, jid, err := c.resolveInstanceAndJID(ctx, instanceName, webhook)
	if err != nil {
		return ctx.JSON(http.StatusOK, map[string]string{"error": err.Error()})
	}

	cctx := ctx.Request().Context()

	// presença digitando
	_ = c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
		InstanceID: instanceID,
		RemoteJID:  jid,
		Presence:   types.ChatPresenceComposing,
	})

	delay := 500 + len(webhook.Content)
	if delay > 2000 {
		delay = 2000
	}
	time.Sleep(time.Millisecond * time.Duration(delay))

	var sendErr error

	if len(webhook.Attachments) > 0 {
		sendErr = c.sendAttachments(cctx, instanceID, jid, webhook)
	} else if webhook.Content != "" {
		sendErr = c.sendText(cctx, instanceID, jid, webhook.Content)
	}

	// presença pausado
	_ = c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
		InstanceID: instanceID,
		RemoteJID:  jid,
		Presence:   types.ChatPresencePaused,
	})

	if sendErr != nil {
		return ctx.JSON(http.StatusOK, map[string]string{"error": sendErr.Error()})
	}

	return ctx.JSON(http.StatusOK, map[string]string{"message": "sent"})
}

func (c *Chatwoot) sendText(ctx context.Context, instanceID string, jid *types.JID, text string) error {
	formatted := strings.ReplaceAll(text, "**", "*")

	_, err := c.whatsmiau.SendText(ctx, &whatsmiau.SendText{
		InstanceID: instanceID,
		RemoteJID:  jid,
		Text:       formatted,
	})

	return err
}

func (c *Chatwoot) sendAttachments(ctx context.Context, instanceID string, jid *types.JID, webhook *dto.ChatwootWebhookRequest) error {
	for _, attachment := range webhook.Attachments {

		switch {
		case strings.HasPrefix(attachment.FileType, "image/"):
			_, err := c.whatsmiau.SendImage(ctx, &whatsmiau.SendImageRequest{
				InstanceID: instanceID,
				RemoteJID:  jid,
				MediaURL:   attachment.DataURL,
				Caption:    webhook.Content,
			})
			if err != nil {
				return err
			}

		case strings.HasPrefix(attachment.FileType, "audio/"):
			_, err := c.whatsmiau.SendAudio(ctx, &whatsmiau.SendAudioRequest{
				InstanceID: instanceID,
				RemoteJID:  jid,
				AudioURL:   attachment.DataURL,
			})
			if err != nil {
				return err
			}

		default:
			_, err := c.whatsmiau.SendDocument(ctx, &whatsmiau.SendDocumentRequest{
				InstanceID: instanceID,
				RemoteJID:  jid,
				MediaURL:   attachment.DataURL,
				Caption:    webhook.Content,
				Mimetype:   attachment.FileType,
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Chatwoot) resolveInstanceAndJID(ctx echo.Context, instanceName string, webhook *dto.ChatwootWebhookRequest) (string, *types.JID, error) {

	instances, err := c.repo.List(ctx.Request().Context(), instanceName)
	if err != nil || len(instances) == 0 {
		return "", nil, fmt.Errorf("instance not found")
	}

	number := c.extractNumber(webhook)
	if number == "" {
		return "", nil, fmt.Errorf("invalid number")
	}

	jid, err := numberToJid(number)
	if err != nil {
		return "", nil, err
	}

	return instances[0].ID, jid, nil
}

func (c *Chatwoot) extractNumber(webhook *dto.ChatwootWebhookRequest) string {

	if webhook.Conversation.Meta.Sender.PhoneNumber != "" {
		return strings.TrimPrefix(webhook.Conversation.Meta.Sender.PhoneNumber, "+")
	}

	if webhook.Conversation.ContactInbox.SourceID != "" {
		return strings.Split(webhook.Conversation.ContactInbox.SourceID, "@")[0]
	}

	if webhook.Conversation.Meta.Sender.Identifier != "" {
		return strings.Split(webhook.Conversation.Meta.Sender.Identifier, "@")[0]
	}

	return ""
}
