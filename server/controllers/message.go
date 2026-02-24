package controllers

import (
	"net/http"
	"regexp"
	"time"

	"github.com/go-playground/validator/v10"
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

// =============================
// Estrutura do webhook Chatwoot
// =============================
type ChatwootWebhook struct {
	Event       string `json:"event"`
	MessageType string `json:"message_type"`
	Content     string `json:"content"`

	Attachments []struct {
		FileType string `json:"file_type"`
		DataURL  string `json:"data_url"`
	} `json:"attachments"`

	Conversation struct {
		Meta struct {
			Sender struct {
				Identifier string `json:"identifier"`
			} `json:"sender"`
		} `json:"meta"`
	} `json:"conversation"`
}

// =============================
// ReceiveWebhook
// =============================
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
	// EVENTOS DO CHATWOOT
	// =============================
	switch payload.Event {

	// Digitando ON
	case "conversation_typing_on":
		_ = c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
			InstanceID: instanceID,
			RemoteJID:  &jid,
			Presence:   types.ChatPresenceComposing,
		})

	// Digitando OFF
	case "conversation_typing_off":
		_ = c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
			InstanceID: instanceID,
			RemoteJID:  &jid,
			Presence:   types.ChatPresencePaused,
		})

	// Nova mensagem
	case "message_created":

		// Só envia se for mensagem do agente
		if payload.MessageType != "outgoing" {
			return ctx.JSON(http.StatusOK, map[string]string{
				"status": "ignored",
			})
		}

		cCtx := ctx.Request().Context()

		// Enviar texto
		if payload.Content != "" {
			_, err := c.whatsmiau.SendText(cCtx, &whatsmiau.SendText{
				InstanceID: instanceID,
				RemoteJID:  &jid,
				Text:       payload.Content,
			})
			if err != nil {
				return ctx.JSON(http.StatusInternalServerError, map[string]string{
					"error": "failed to send text",
				})
			}
		}

		// Enviar attachments
		for _, att := range payload.Attachments {
			switch att.FileType {
			case "audio":
				_, err := c.whatsmiau.SendAudio(cCtx, &whatsmiau.SendAudioRequest{
					InstanceID: instanceID,
					RemoteJID:  &jid,
					AudioURL:   att.DataURL,
				})
				if err != nil {
					return ctx.JSON(http.StatusInternalServerError, map[string]string{
						"error": "failed to send audio",
					})
				}
			case "image":
				_, err := c.whatsmiau.SendImage(cCtx, &whatsmiau.SendImageRequest{
					InstanceID: instanceID,
					RemoteJID:  &jid,
					MediaURL:   att.DataURL,
					Mimetype:   "image/jpeg",
				})
				if err != nil {
					return ctx.JSON(http.StatusInternalServerError, map[string]string{
						"error": "failed to send image",
					})
				}
			case "file":
				_, err := c.whatsmiau.SendDocument(cCtx, &whatsmiau.SendDocumentRequest{
					InstanceID: instanceID,
					RemoteJID:  &jid,
					MediaURL:   att.DataURL,
					FileName:   "document.pdf",
					Mimetype:   "application/pdf",
				})
				if err != nil {
					return ctx.JSON(http.StatusInternalServerError, map[string]string{
						"error": "failed to send document",
					})
				}
			}
		}

		// Enviar reaction (se houver)
		emojiRegex := regexp.MustCompile(`[\x{1F600}-\x{1F64F}]|[\x{1F300}-\x{1F5FF}]|[\x{1F680}-\x{1F6FF}]|[\x{2600}-\x{26FF}]|[\x{2700}-\x{27BF}]`)
		if emojiRegex.MatchString(payload.Content) {
			_, _ = c.whatsmiau.SendReaction(cCtx, &whatsmiau.SendReactionRequest{
				InstanceID: instanceID,
				RemoteJID:  &jid,
				Reaction:   payload.Content,
				MessageID:  "", // aqui você pode preencher se tiver in_reply_to_external_id
				FromMe:     true,
			})
		}
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "ok",
	})
}
