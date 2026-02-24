package controllers

import (
	"net/http"
	"strings"

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

	// 🔥 Multi-instância pela URL
	instanceID := ctx.Param("instance")
	if instanceID == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "instance not provided",
		})
	}

	var payload map[string]interface{}
	if err := ctx.Bind(&payload); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid payload",
		})
	}

	event, _ := payload["event"].(string)

	// Extrair telefone
	conversation, _ := payload["conversation"].(map[string]interface{})
	contact, _ := conversation["contact"].(map[string]interface{})
	phoneRaw, _ := contact["phone_number"].(string)

	if phoneRaw == "" {
		return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored"})
	}

	phone := normalizePhone(phoneRaw)
	jid := phone + "@s.whatsapp.net"

	switch event {

	// =============================
	// DIGITANDO
	// =============================
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

	// =============================
	// ENVIO DE MENSAGEM
	// =============================
	case "message_created":

		message, _ := payload["message"].(map[string]interface{})

		// Ignorar mensagens recebidas do WhatsApp
		messageType, _ := message["message_type"].(string)
		private, _ := message["private"].(bool)

		if messageType != "outgoing" || private {
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored"})
		}

		content, _ := message["content"].(string)

		// =============================
		// TEXTO
		// =============================
		if content != "" {
			_, err := h.whatsmiau.SendText(ctx.Request().Context(), &whatsmiau.SendText{
				Text:       content,
				InstanceID: instanceID,
				RemoteJID:  jid,
			})

			if err != nil {
				zap.L().Error("SendText failed", zap.Error(err))
				return ctx.JSON(http.StatusInternalServerError, map[string]string{
					"error": "failed to send text",
				})
			}
		}

		// =============================
		// ATTACHMENTS (MÍDIA)
		// =============================
		attachments, _ := message["attachments"].([]interface{})
		for _, att := range attachments {

			attachment, _ := att.(map[string]interface{})
			fileURL, _ := attachment["data_url"].(string)
			fileType, _ := attachment["file_type"].(string)

			if fileURL == "" {
				continue
			}

			switch {

			// 🎵 ÁUDIO
			case strings.HasPrefix(fileType, "audio"):
				_, err := h.whatsmiau.SendAudio(ctx.Request().Context(), &whatsmiau.SendAudioRequest{
					AudioURL:   fileURL,
					InstanceID: instanceID,
					RemoteJID:  jid,
				})
				if err != nil {
					zap.L().Error("SendAudio failed", zap.Error(err))
				}

			// 🖼 IMAGEM
			case strings.HasPrefix(fileType, "image"):
				_, err := h.whatsmiau.SendImage(ctx.Request().Context(), &whatsmiau.SendImageRequest{
					MediaURL:   fileURL,
					InstanceID: instanceID,
					RemoteJID:  jid,
				})
				if err != nil {
					zap.L().Error("SendImage failed", zap.Error(err))
				}

			// 📎 DOCUMENTO / VÍDEO
			default:
				_, err := h.whatsmiau.SendDocument(ctx.Request().Context(), &whatsmiau.SendDocumentRequest{
					MediaURL:   fileURL,
					InstanceID: instanceID,
					RemoteJID:  jid,
					Mimetype:   fileType,
				})
				if err != nil {
					zap.L().Error("SendDocument failed", zap.Error(err))
				}
			}
		}
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "ok",
	})
}

// =============================
// UTIL
// =============================

func normalizePhone(phone string) string {
	replacer := strings.NewReplacer(
		"+", "",
		" ", "",
		"-", "",
		"(", "",
		")", "",
	)
	return replacer.Replace(phone)
}
