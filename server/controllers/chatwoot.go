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

type ChatwootWebhook struct {
	Event       string `json:"event"`
	MessageType string `json:"message_type"`
	Content     string `json:"content"`
	Private     bool   `json:"private"`
	SourceID    string `json:"source_id"`

	ContentAttributes struct {
		InReplyTo             int    `json:"in_reply_to"`
		InReplyToExternalID   string `json:"in_reply_to_external_id"`
		ExternalError         any    `json:"external_error"`
	} `json:"content_attributes"`

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
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "instance required"})
	}

	var payload ChatwootWebhook
	if err := ctx.Bind(&payload); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "invalid payload"})
	}

	instances, err := c.repo.List(ctx.Request().Context(), instanceName)
	if err != nil || len(instances) == 0 {
		return ctx.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
	}

	instanceID := instances[0].ID

	jidString := payload.Conversation.Meta.Sender.Identifier
	jid, err := types.ParseJID(jidString)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "invalid jid"})
	}

	switch payload.Event {

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

		// ==========================================
		// 🔥 REACTION SE FOR REPLY
		// ==========================================
		if payload.ContentAttributes.InReplyToExternalID != "" {

			externalID := payload.ContentAttributes.InReplyToExternalID

			// Remove WAID:
			messageID := strings.TrimPrefix(externalID, "WAID:")

			_, err := c.whatsmiau.SendReaction(
				ctx.Request().Context(),
				&whatsmiau.SendReactionRequest{
					InstanceID: instanceID,
					Reaction:   "👍", // você pode alterar aqui
					RemoteJID:  &jid,
					MessageID:  messageID,
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

		// ==========================================
		// TEXTO NORMAL
		// ==========================================
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

		return ctx.JSON(http.StatusOK, map[string]string{
			"status": "sent",
		})
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "ignored_event",
	})
}
