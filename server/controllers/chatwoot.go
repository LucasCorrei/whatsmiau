package controllers

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/verbeux-ai/whatsmiau/interfaces"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"github.com/verbeux-ai/whatsmiau/utils"
	"go.mau.fi/whatsmeow/types"
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

type ChatwootWebhook struct {
	Event       string `json:"event"`
	MessageType string `json:"message_type"`
	Content     string `json:"content"`

	Conversation struct {
		Meta struct {
			Sender struct {
				Identifier string `json:"identifier"`
			} `json:"sender"`
		} `json:"meta"`
	} `json:"conversation"`
}

func (c *Chatwoot) ReceiveWebhook(ctx echo.Context) error {
	instanceName := ctx.Param("instance")
	if instanceName == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "instance required",
		})
	}

	var payload ChatwootWebhook
	if err := ctx.Bind(&payload); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid payload",
		})
	}

	// Buscar instância
	instances, err := c.repo.List(ctx.Request().Context(), instanceName)
	if err != nil || len(instances) == 0 {
		return ctx.JSON(http.StatusNotFound, map[string]string{
			"error": "instance not found",
		})
	}

	instanceID := instances[0].ID
	jidString := payload.Conversation.Meta.Sender.Identifier

	if jidString == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "identifier not found",
		})
	}

	jid, err := types.ParseJID(jidString)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid jid",
		})
	}

	// =============================
	// EVENTOS
	// =============================

	switch payload.Event {

	case "conversation_typing_on":
		_ = c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
			InstanceID: instanceID,
			RemoteJID:  jid,
			Presence:   types.ChatPresenceComposing,
		})

	case "conversation_typing_off":
		_ = c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
			InstanceID: instanceID,
			RemoteJID:  jid,
			Presence:   types.ChatPresencePaused,
		})

	case "message_created":

		// Só enviar se for mensagem enviada pelo agente
		if payload.MessageType != "outgoing" {
			return ctx.JSON(http.StatusOK, map[string]string{
				"status": "ignored",
			})
		}

		if payload.Content == "" {
			return ctx.JSON(http.StatusOK, map[string]string{
				"status": "empty message",
			})
		}

		_, err = c.whatsmiau.SendText(ctx.Request().Context(), &whatsmiau.SendText{
			InstanceID: instanceID,
			RemoteJID:  jid,
			Text:       payload.Content,
		})

		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to send message",
			})
		}
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "ok",
	})
}
