package controllers

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/verbeux-ai/whatsmiau/interfaces"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
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

// =============================
// Estrutura webhook Chatwoot
// =============================
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

	Attachments []struct {
		DataURL  string `json:"data_url"`
		FileType string `json:"file_type"`
		FileName string `json:"file_name"`
	} `json:"attachments"`
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

	switch payload.Event {

	case "conversation_typing_on":
		_ = c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
			InstanceID: instanceID,
			RemoteJID:  &jid,
			Presence:   types.ChatPresenceComposing,
		})

	case "conversation_typing_off":
		_ = c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
			InstanceID: instanceID,
			RemoteJID:  &jid,
			Presence:   types.ChatPresencePaused,
		})

	case "message_created":

		// só envia mensagens do agente
		if payload.MessageType != "outgoing" {
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored"})
		}

		// =============================
		// TEXTO
		// =============================
		if payload.Content != "" {
			_, err := c.whatsmiau.SendText(ctx.Request().Context(), &whatsmiau.SendText{
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

		// =============================
		// ANEXOS
		// =============================
		for _, att := range payload.Attachments {

			if att.DataURL == "" {
				continue
			}

			fileType := strings.ToLower(att.FileType)

			// IMAGEM
			if strings.HasPrefix(fileType, "image/") {

				_, err := c.whatsmiau.SendImage(ctx.Request().Context(), &whatsmiau.SendImageRequest{
					InstanceID: instanceID,
					RemoteJID:  &jid,
					MediaURL:   att.DataURL,
					Caption:    payload.Content,
					Mimetype:   fileType,
				})

				if err != nil {
					return ctx.JSON(http.StatusInternalServerError, map[string]string{
						"error": "failed to send image",
					})
				}

				continue
			}

			// AUDIO
			if strings.HasPrefix(fileType, "audio/") {

				_, err := c.whatsmiau.SendAudio(ctx.Request().Context(), &whatsmiau.SendAudioRequest{
					InstanceID: instanceID,
					RemoteJID:  &jid,
					AudioURL:   att.DataURL,
				})

				if err != nil {
					return ctx.JSON(http.StatusInternalServerError, map[string]string{
						"error": "failed to send audio",
					})
				}

				continue
			}

			// DOCUMENTO
			_, err := c.whatsmiau.SendDocument(ctx.Request().Context(), &whatsmiau.SendDocumentRequest{
				InstanceID: instanceID,
				RemoteJID:  &jid,
				MediaURL:   att.DataURL,
				Caption:    payload.Content,
				FileName:   att.FileName,
				Mimetype:   fileType,
			})

			if err != nil {
				return ctx.JSON(http.StatusInternalServerError, map[string]string{
					"error": "failed to send document",
				})
			}
		}
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "ok",
	})
}
