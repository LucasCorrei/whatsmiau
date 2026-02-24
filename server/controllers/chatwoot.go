package controllers

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/verbeux-ai/whatsmiau/interfaces"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"go.mau.fi/whatsmeow/types"
)

// Struct para receber o webhook do Chatwoot
type ChatwootWebhook struct {
	Event            string `json:"event"`
	MessageType      string `json:"message_type"`
	Content          string `json:"content"`
	ContentAttributes struct {
		InReplyToExternalID string `json:"in_reply_to_external_id"`
	} `json:"content_attributes"`

	Attachments []struct {
		FileType string `json:"file_type"`
		DataURL  string `json:"data_url"`
		Name     string `json:"name"`
	} `json:"attachments"`

	Conversation struct {
		Meta struct {
			Sender struct {
				Identifier string `json:"identifier"`
			} `json:"sender"`
		} `json:"meta"`
	} `json:"conversation"`
}

// Função principal que recebe o webhook
func ReceiveWebhook(c echo.Context) error {
	var payload ChatwootWebhook

	if err := c.Bind(&payload); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid payload"})
	}

	// Só tratamos eventos de criação de mensagem
	if payload.Event != "message_created" {
		return c.JSON(http.StatusOK, map[string]string{"status": "ignored"})
	}

	// Pega o identificador do contato no WhatsApp
	jid := payload.Conversation.Meta.Sender.Identifier
	if jid == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing sender identifier"})
	}

	// Se houver reaction (em reply)
	if payload.ContentAttributes.InReplyToExternalID != "" && len(payload.Content) > 0 {
		// Extrai somente o ID após "WAID:"
		messageID := strings.TrimPrefix(payload.ContentAttributes.InReplyToExternalID, "WAID:")

		reaction := &whatsmiau.SendReactionRequest{
			RemoteJID: jid,
			MessageID: messageID,
			Reaction:  payload.Content,
			FromMe:    true,
		}

		if _, err := whatsMiauClient.SendReaction(c.Request().Context(), reaction); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to send reaction"})
		}

		return c.JSON(http.StatusOK, map[string]string{"status": "reaction_sent"})
	}

	// Se houver attachments
	if len(payload.Attachments) > 0 {
		for _, att := range payload.Attachments {
			media := &whatsmiau.SendMediaRequest{
				RemoteJID: jid,
				URL:       att.DataURL,
				FileName:  att.Name,
				MimeType:  att.FileType,
			}

			if _, err := whatsMiauClient.SendMedia(c.Request().Context(), media); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to send media"})
			}
		}

		return c.JSON(http.StatusOK, map[string]string{"status": "media_sent"})
	}

	// Caso seja só mensagem de texto
	if len(payload.Content) > 0 {
		text := &whatsmiau.SendTextRequest{
			RemoteJID: jid,
			Text:      payload.Content,
		}

		if _, err := whatsMiauClient.SendText(c.Request().Context(), text); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to send text"})
		}

		return c.JSON(http.StatusOK, map[string]string{"status": "text_sent"})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "ignored"})
}
