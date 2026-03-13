package controllers

import (
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/verbeux-ai/whatsmiau/interfaces"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
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

type ChatwootWebhook struct {
	ID          int    `json:"id"`
	Event       string `json:"event"`
	MessageType string `json:"message_type"`
	Content     string `json:"content"`
	Private     bool   `json:"private"`
	SourceID    string `json:"source_id"`

	ContentAttributes struct {
		InReplyTo           int    `json:"in_reply_to"`
		InReplyToExternalID string `json:"in_reply_to_external_id"`
		ExternalError       any    `json:"external_error"`
		Deleted             bool   `json:"deleted"`
	} `json:"content_attributes"`

	Attachments []struct {
		FileType  string `json:"file_type"`
		DataURL   string `json:"data_url"`
		Name      string `json:"name"`       // nome original do arquivo (ex: "relatorio.pdf")
		Extension string `json:"extension"`  // extensão sem ponto (ex: "pdf")
	} `json:"attachments"`

	Conversation struct {
		ID   int `json:"id"`
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

// resolveOutgoingWAID resolve o sourceID e o waMessageID de uma mensagem outgoing.
// Tenta primeiro o sourceID do payload, depois o cache em memória.
func resolveOutgoingWAID(c *Chatwoot, payloadSourceID string, chatwootMsgID int) (sourceID, waMessageID string, ok bool) {
	sourceID = payloadSourceID
	if sourceID == "" {
		waid, found := c.whatsmiau.GetChatwootOutgoingWAID(chatwootMsgID)
		if !found {
			return "", "", false
		}
		sourceID = waid
	}
	waMessageID = strings.TrimPrefix(sourceID, "WAID:")
	if waMessageID == sourceID {
		return "", "", false
	}
	return sourceID, waMessageID, true
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
		zap.L().Info("chatwoot webhook: message_created recebido",
			zap.Int("payloadID", payload.ID),
			zap.String("messageType", payload.MessageType),
			zap.String("sourceID", payload.SourceID))

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
			textResp, err := c.whatsmiau.SendText(ctx.Request().Context(), &whatsmiau.SendText{
				InstanceID:     instanceID,
				RemoteJID:      &jid,
				Text:           payload.Content,
				QuoteMessageID: quotedMessageID,
			})

			if err != nil {
				return ctx.JSON(http.StatusInternalServerError, map[string]string{
					"error": "failed to send text",
				})
			}

			if textResp != nil && payload.ID > 0 {
				go c.whatsmiau.UpdateChatwootMessageSourceID(&instances[0], payload.ID, textResp.ID)
			}
		}

		// -----------------------------------------
		// ATTACHMENTS
		// -----------------------------------------
		for _, att := range payload.Attachments {

			switch att.FileType {

			case "audio":
				audioResp, err := c.whatsmiau.SendAudio(ctx.Request().Context(), &whatsmiau.SendAudioRequest{
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
				if audioResp != nil && payload.ID > 0 {
					go c.whatsmiau.UpdateChatwootMessageSourceID(&instances[0], payload.ID, audioResp.ID)
				}

			case "image":
				imgResp, err := c.whatsmiau.SendImage(ctx.Request().Context(), &whatsmiau.SendImageRequest{
					InstanceID:     instanceID,
					RemoteJID:      &jid,
					MediaURL:       att.DataURL,
					Mimetype:       "image/jpeg",
					QuoteMessageID: quotedMessageID,
				})
				if err != nil {
					return ctx.JSON(http.StatusInternalServerError, map[string]string{
						"error": "failed to send image",
					})
				}
				if imgResp != nil && payload.ID > 0 {
					go c.whatsmiau.UpdateChatwootMessageSourceID(&instances[0], payload.ID, imgResp.ID)
				}

			case "file":
				// Resolve filename: prioridade → att.Name → basename da URL → "document"
				docFileName := att.Name
				if docFileName == "" {
					docFileName = path.Base(strings.Split(att.DataURL, "?")[0])
				}
				if docFileName == "" || docFileName == "." {
					docFileName = "document"
				}
				// Resolve mimetype a partir da extensão do arquivo
				docMimetype := "application/octet-stream"
				if att.Extension != "" {
					if mt := mime.TypeByExtension("." + att.Extension); mt != "" {
						docMimetype = mt
					}
				} else if ext := path.Ext(docFileName); ext != "" {
					if mt := mime.TypeByExtension(ext); mt != "" {
						docMimetype = mt
					}
				}
				docResp, err := c.whatsmiau.SendDocument(ctx.Request().Context(), &whatsmiau.SendDocumentRequest{
					InstanceID:     instanceID,
					RemoteJID:      &jid,
					MediaURL:       att.DataURL,
					FileName:       docFileName,
					Mimetype:       docMimetype,
					QuoteMessageID: quotedMessageID,
				})
				if err != nil {
					return ctx.JSON(http.StatusInternalServerError, map[string]string{
						"error": "failed to send document",
					})
				}
				if docResp != nil && payload.ID > 0 {
					go c.whatsmiau.UpdateChatwootMessageSourceID(&instances[0], payload.ID, docResp.ID)
				}
			}
		}

		return ctx.JSON(http.StatusOK, map[string]string{
			"status": "sent_to_whatsapp",
		})

	// -------------------------------
	// MESSAGE UPDATED (edição pelo operador no Chatwoot → WhatsApp)
	// Chatwoot também dispara message_updated (com content_attributes.deleted=true)
	// quando o operador apaga uma mensagem — nesse caso revogamos no WhatsApp.
	// -------------------------------
	case "message_updated":
		if payload.MessageType != "outgoing" || payload.Private {
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored"})
		}

		_, waMessageID, ok := resolveOutgoingWAID(c, payload.SourceID, payload.ID)
		if !ok {
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored_no_source_id"})
		}

		// Deleção: Chatwoot seta content_attributes.deleted = true
		if payload.ContentAttributes.Deleted {
			// Verifica se a deleção foi originada pelo WhatsApp (não pelo operador no Chatwoot).
			// Se sim, ignora — a mensagem já foi revogada no WhatsApp e nós deletamos no Chatwoot,
			// e o webhook é apenas o eco dessa deleção. Evita o loop e a nota de erro falsa.
			if c.whatsmiau.IsWARevokedMessage(waMessageID) {
				zap.L().Info("chatwoot webhook: message_updated deleted ignorado — revogado pelo WhatsApp",
					zap.String("waMessageID", waMessageID))
				return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored_wa_revoked"})
			}

			// Se a chave não existe no Redis, o TTL de 60h expirou (ou processo reiniciou sem DB).
			if _, withinTTL := c.whatsmiau.GetChatwootOutgoingWAID(payload.ID); !withinTTL {
				zap.L().Warn("chatwoot webhook: prazo de 60h expirado, não é possível apagar no WhatsApp",
					zap.Int("chatwootMsgID", payload.ID))
				go c.whatsmiau.PostChatwootPrivateNote(
					&instances[0],
					payload.Conversation.ID,
					"⚠️ Não foi possível apagar esta mensagem no WhatsApp: o prazo de 60 horas para exclusão já expirou.",
				)
				return ctx.JSON(http.StatusOK, map[string]string{"status": "revoke_ttl_expired"})
			}

			_, err := c.whatsmiau.RevokeMessage(ctx.Request().Context(), &whatsmiau.RevokeMessageRequest{
				InstanceID: instanceID,
				RemoteJID:  &jid,
				MessageID:  waMessageID,
			})
			if err != nil {
				zap.L().Error("chatwoot webhook: erro ao apagar mensagem no WhatsApp", zap.Error(err))
				return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to revoke message"})
			}
			return ctx.JSON(http.StatusOK, map[string]string{"status": "revoked"})
		}

		// Edição: requer conteúdo
		if payload.Content == "" {
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored_empty_content"})
		}

		_, err := c.whatsmiau.EditMessage(ctx.Request().Context(), &whatsmiau.EditMessageRequest{
			InstanceID: instanceID,
			RemoteJID:  &jid,
			MessageID:  waMessageID,
			NewText:    payload.Content,
		})
		if err != nil {
			zap.L().Error("chatwoot webhook: erro ao editar mensagem no WhatsApp", zap.Error(err))
			return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to edit message"})
		}

		return ctx.JSON(http.StatusOK, map[string]string{"status": "edited"})

	// -------------------------------
	// MESSAGE DELETED (deleção pelo operador no Chatwoot → WhatsApp)
	// -------------------------------
	case "message_deleted":
		if payload.MessageType != "outgoing" || payload.Private {
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored"})
		}

		_, waMessageID, ok := resolveOutgoingWAID(c, payload.SourceID, payload.ID)
		if !ok {
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored_no_source_id"})
		}

		// Verifica se a deleção foi originada pelo WhatsApp (não pelo operador no Chatwoot).
		if c.whatsmiau.IsWARevokedMessage(waMessageID) {
			zap.L().Info("chatwoot webhook: message_deleted ignorado — revogado pelo WhatsApp",
				zap.String("waMessageID", waMessageID))
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored_wa_revoked"})
		}

		if _, withinTTL := c.whatsmiau.GetChatwootOutgoingWAID(payload.ID); !withinTTL {
			zap.L().Warn("chatwoot webhook: prazo de 60h expirado, não é possível apagar no WhatsApp",
				zap.Int("chatwootMsgID", payload.ID))
			go c.whatsmiau.PostChatwootPrivateNote(
				&instances[0],
				payload.Conversation.ID,
				"⚠️ Não foi possível apagar esta mensagem no WhatsApp: o prazo de 60 horas para exclusão já expirou.",
			)
			return ctx.JSON(http.StatusOK, map[string]string{"status": "revoke_ttl_expired"})
		}

		_, err := c.whatsmiau.RevokeMessage(ctx.Request().Context(), &whatsmiau.RevokeMessageRequest{
			InstanceID: instanceID,
			RemoteJID:  &jid,
			MessageID:  waMessageID,
		})
		if err != nil {
			zap.L().Error("chatwoot webhook: erro ao apagar mensagem no WhatsApp", zap.Error(err))
			return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to revoke message"})
		}

		return ctx.JSON(http.StatusOK, map[string]string{"status": "revoked"})
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "ignored_event",
	})
}
