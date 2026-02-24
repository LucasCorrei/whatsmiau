package controllers

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"go.mau.fi/whatsmeow/types"
	"go.uber.org/zap"
)

type ChatwootWebhook struct {
	whatsmiau *whatsmiau.Whatsmiau
}

func NewChatwootWebhook(w *whatsmiau.Whatsmiau) *ChatwootWebhook {
	return &ChatwootWebhook{
		whatsmiau: w,
	}
}

func (h *ChatwootWebhook) Handle(ctx echo.Context) error {
	var payload map[string]interface{}

	if err := ctx.Bind(&payload); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid payload",
		})
	}

	event, _ := payload["event"].(string)

	// Você deve extrair:
	// - number (do conversation.contact)
	// - instanceID (da sua lógica interna)
	// - message content

	jid := "5511999999999@s.whatsapp.net" // <- extrair corretamente
	instanceID := "default"              // <- sua lógica

	switch event {

	case "conversation_typing_on":
		return h.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
			InstanceID: instanceID,
			RemoteJID:  jid,
			Presence:   types.ChatPresenceComposing,
		})

	case "conversation_typing_off":
		return h.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
			InstanceID: instanceID,
			RemoteJID:  jid,
			Presence:   types.ChatPresencePaused,
		})

	case "message_created":
		message := extractMessage(payload)

		_, err := h.whatsmiau.SendText(ctx.Request().Context(), &whatsmiau.SendText{
			Text:       message,
			InstanceID: instanceID,
			RemoteJID:  jid,
		})
		if err != nil {
			zap.L().Error("SendText failed", zap.Error(err))
			return ctx.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to send message",
			})
		}
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "ok",
	})
}
