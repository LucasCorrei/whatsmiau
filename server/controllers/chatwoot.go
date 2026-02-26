package controllers

import (
	"net/http"
	"regexp"
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

type ChatwootWebhook struct {
	Event       string `json:"event"`
	MessageType string `json:"message_type"`
	Content     string `json:"content"`
	Private     bool   `json:"private"`
	SourceID    string `json:"source_id"`

	ContentAttributes struct {
		InReplyTo           int    `json:"in_reply_to"`
		InReplyToExternalID string `json:"in_reply_to_external_id"`
		ExternalError       any    `json:"external_error"`
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

// =======================================================
// UTIL: verifica se o texto é somente emoji
// =======================================================
func isOnlyEmoji(text string) bool {
	if text == "" {
		return false
	}
	emojiRegex := regexp.MustCompile(`^([\p{So}\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}\x{FE0F}\x{200D}]+)$`)
	return emojiRegex.MatchString(strings.TrimSpace(text))
}

// =======================================================
// WEBHOOK
// =======================================================
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

	// =======================================================
	// EVENT SWITCH
	// =======================================================
	switch payload.Event {

	// -------------------------------
	// TYPING ON
	// -------------------------------
	case "conversation_typing_on":
		_ = c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
			InstanceID: instanceID,
			RemoteJID:  &jid,
			Presence:   types.ChatPresenceComposing,
		})
		return ctx.JSON(http.StatusOK, map[string]string{"status": "typing_on"})

	// -------------------------------
	// TYPING OFF
	// -------------------------------
	case "conversation_typing_off":
		_ = c.whatsmiau.ChatPresence(&whatsmiau.ChatPresenceRequest{
			InstanceID: instanceID,
			RemoteJID:  &jid,
			Presence:   types.ChatPresencePaused,
		})
		return ctx.JSON(http.StatusOK, map[string]string{"status": "typing_off"})

	// -------------------------------
	// MESSAGE CREATED
	// -------------------------------
	case "message_created":

		if payload.MessageType != "outgoing" {
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored_not_outgoing"})
		}

		if payload.Private {
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored_private"})
		}

		if payload.SourceID != "" {
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored_already_has_source_id"})
		}

		// -----------------------------------------
		// Verifica se é reply
		// -----------------------------------------
		var quotedMessageID string
		if payload.ContentAttributes.InReplyToExternalID != "" {
			quotedMessageID = strings.TrimPrefix(
				payload.ContentAttributes.InReplyToExternalID,
				"WAID:",
			)
		}

		// -----------------------------------------
		// TEXTO
		// -----------------------------------------
		if payload.Content != "" {

			// Se for reply + emoji → reaction
			if quotedMessageID != "" && isOnlyEmoji(payload.Content) {

				_, err := c.whatsmiau.SendReaction(
					ctx.Request().Context(),
					&whatsmiau.SendReactionRequest{
						InstanceID: instanceID,
						Reaction:   payload.Content,
						RemoteJID:  &jid,
						MessageID:  quotedMessageID,
						FromMe:     false,
					},
				)

				if err != nil {
					return ctx.JSON(http.StatusInternalServerError, map[string]string{
						"error": "failed to send reaction",
					})
				}

				return ctx.JSON(http.StatusOK, map[string]string{
					"status": "reaction_sent",
				})
			}

			// Texto normal (com ou sem quoted)
			_, err := c.whatsmiau.SendText(ctx.Request().Context(), &whatsmiau.SendText{
				InstanceID:     instanceID,
				RemoteJID:      &jid,
				Text:           payload.Content,
				QuoteMessageID: quotedMessageID,
				QuoteMessage: payload.Content,
			})

			if err != nil {
				return ctx.JSON(http.StatusInternalServerError, map[string]string{
					"error": "failed to send text",
				})
			}
		}

		// -----------------------------------------
		// ATTACHMENTS
		// -----------------------------------------
		for _, att := range payload.Attachments {

			switch att.FileType {

			case "audio":
				_, err := c.whatsmiau.SendAudio(ctx.Request().Context(), &whatsmiau.SendAudioRequest{
					InstanceID:     instanceID,
					RemoteJID:      &jid,
					AudioURL:       att.DataURL,
					QuoteMessageID: quotedMessageID,
				})
				if err != nil {
					return ctx.JSON(http.StatusInternalServerError, map[string]string{
						"error": "failed to send audio",
					})
				}

			case "image":
				_, err := c.whatsmiau.SendImage(ctx.Request().Context(), &whatsmiau.SendImageRequest{
					InstanceID:     instanceID,
					RemoteJID:      &jid,
					MediaURL:       att.DataURL,
					Mimetype:       "image/jpeg",
				})
				if err != nil {
					return ctx.JSON(http.StatusInternalServerError, map[string]string{
						"error": "failed to send image",
					})
				}

			case "file":
				_, err := c.whatsmiau.SendDocument(ctx.Request().Context(), &whatsmiau.SendDocumentRequest{
					InstanceID:     instanceID,
					RemoteJID:      &jid,
					MediaURL:       att.DataURL,
					FileName:       "document",
					Mimetype:       "application/octet-stream",
				})
				if err != nil {
					return ctx.JSON(http.StatusInternalServerError, map[string]string{
						"error": "failed to send document",
					})
				}
			}
		}

		return ctx.JSON(http.StatusOK, map[string]string{
			"status": "sent_to_whatsapp",
		})
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "ignored_event",
	})
}
