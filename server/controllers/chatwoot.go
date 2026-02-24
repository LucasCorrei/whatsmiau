package controllers

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/verbeux-ai/whatsmiau/interfaces"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"github.com/verbeux-ai/whatsmiau/server/dto"
	"github.com/verbeux-ai/whatsmiau/utils"
	"go.uber.org/zap"
)

type Chatwoot struct {
	repo      interfaces.InstanceRepository
	whatsmiau *whatsmiau.Whatsmiau
}

func NewChatwoot(repository interfaces.InstanceRepository, w *whatsmiau.Whatsmiau) *Chatwoot {
	return &Chatwoot{
		repo:      repository,
		whatsmiau: w,
	}
}

type ChatwootWebhook struct {
	Content     string `json:"content"`
	MessageType string `json:"message_type"`
	Conversation struct {
		Contact struct {
			PhoneNumber string `json:"phone_number"`
		} `json:"contact"`
	} `json:"conversation"`
	Attachments []struct {
		DataURL string `json:"data_url"`
		FileName string `json:"file_name"`
	} `json:"attachments"`
}

func (c *Chatwoot) HandleWebhook(ctx echo.Context) error {

	var payload ChatwootWebhook
	if err := ctx.Bind(&payload); err != nil {
		return utils.HTTPFail(ctx, http.StatusBadRequest, err, "invalid payload")
	}

	// Ignora mensagens recebidas (só envia outgoing)
	if payload.MessageType != "outgoing" {
		return ctx.NoContent(http.StatusOK)
	}

	number := payload.Conversation.Contact.PhoneNumber
	instanceID := ctx.Param("instance") // exemplo: /chatwoot/:instance

	if len(payload.Attachments) == 0 {

		// TEXTO
		req := dto.SendTextRequest{
			InstanceID: instanceID,
			Number:     number,
			Text:       payload.Content,
		}

		// Reutiliza controller existente
		messageController := NewMessages(c.repo, c.whatsmiau)
		ctx.SetRequest(ctx.Request().WithContext(ctx.Request().Context()))
		ctx.Set("body", req)

		return messageController.SendText(ctx)
	}

	// TEM ANEXO
	attachment := payload.Attachments[0]

	req := dto.SendDocumentRequest{
		InstanceID: instanceID,
		Number:     number,
		Media:      attachment.DataURL,
		FileName:   attachment.FileName,
		Caption:    payload.Content,
		Mimetype:   "",
	}

	messageController := NewMessages(c.repo, c.whatsmiau)
	ctx.Set("body", req)

	return messageController.SendDocument(ctx)
}
