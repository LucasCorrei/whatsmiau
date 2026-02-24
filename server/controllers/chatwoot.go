package controllers

import (
	"net/http"
	"fmt"
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
// Estrutura do webhook Chatwoot
// =============================
type ChatwootWebhook struct {
	Event             string `json:"event"`
	MessageType       string `json:"message_type"`
	Content           string `json:"content"`
	ContentAttributes struct {
		InReplyTo int `json:"in_reply_to"`
	} `json:"content_attributes"`

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

		// Só envia se for mensagem do agente
		if payload.MessageType != "outgoing" {
			return ctx.JSON(http.StatusOK, map[string]string{
				"status": "ignored",
			})
		}

		// =============================
		// REACTION
		// =============================
		if payload.ContentAttributes.InReplyTo > 0 && payload.Content != "" {
			_, err := c.whatsmiau.SendReaction(ctx.Request().Context(), &whatsmiau.SendReactionRequest{
				InstanceID: instanceID,
				RemoteJID:  &jid,
				MessageID:  fmt.Sprintf("%d", payload.ContentAttributes.InReplyTo),
				Reaction:   payload.Content,
				FromMe:     true,
			})
			if err != nil {
				return ctx.JSON(http.StatusInternalServerError, map[string]string{
					"error": "failed to send reaction",
				})
			}
			return ctx.JSON(http.StatusOK, map[string]string{"status": "reaction_sent"})
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
		// ATTACHMENTS
		// =============================
		for _, att := range payload.Attachments {

			switch att.FileType {

			case "audio":
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

			case "image":
				_, err := c.whatsmiau.SendImage(ctx.Request().Context(), &whatsmiau.SendImageRequest{
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
				_, err := c.whatsmiau.SendDocument(ctx.Request().Context(), &whatsmiau.SendDocumentRequest{
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
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "ok",
	})
}
