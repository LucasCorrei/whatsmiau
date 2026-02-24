package controllers

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"github.com/verbeux-ai/whatsmiau/repositories/instances"
	"go.mau.fi/whatsmeow/types"
)

type Chatwoot struct {
	repo      instances.Repository
	whatsmiau *whatsmiau.Whatsmiau
}

func NewChatwoot(repo instances.Repository, w *whatsmiau.Whatsmiau) *Chatwoot {
	return &Chatwoot{
		repo:      repo,
		whatsmiau: w,
	}
}

type ChatwootWebhook struct {
	Event       string `json:"event"`
	MessageType string `json:"message_type"`
	Content     string `json:"content"`
	ContentType string `json:"content_type"`
	Private     bool   `json:"private"`

	Conversation struct {
		Meta struct {
			Sender struct {
				Identifier string `json:"identifier"`
			} `json:"sender"`
		} `json:"meta"`
	} `json:"conversation"`

	Attachments []struct {
		URL      string `json:"url"`
		FileName string `json:"filename"`
		FileType string `json:"file_type"`
	} `json:"attachments"`
}

func (c *Chatwoot) ReceiveWebhook(ctx echo.Context) error {
	instanceName := ctx.Param("instance")

	var payload ChatwootWebhook
	if err := ctx.Bind(&payload); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid payload",
		})
	}

	// Só processa mensagens enviadas pelo agente
	if payload.Event != "message_created" {
		return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored"})
	}

	if payload.MessageType != "outgoing" || payload.Private {
		return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored"})
	}

	if payload.Content == "" && len(payload.Attachments) == 0 {
		return ctx.JSON(http.StatusOK, map[string]string{"status": "empty"})
	}

	// Buscar instância
	instanceList, err := c.repo.List(ctx.Request().Context(), instanceName)
	if err != nil || len(instanceList) == 0 {
		return ctx.JSON(http.StatusNotFound, map[string]string{
			"error": "instance not found",
		})
	}

	instanceID := instanceList[0].ID

	// Montar JID
	jidString := payload.Conversation.Meta.Sender.Identifier

	if !strings.Contains(jidString, "@") {
		jidString = jidString + "@s.whatsapp.net"
	}

	jid, err := types.ParseJID(jidString)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid jid",
		})
	}

	// =============================
	// ENVIO DE IMAGEM
	// =============================
	if payload.ContentType == "image" && len(payload.Attachments) > 0 {
		data, err := downloadFile(payload.Attachments[0].URL)
		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{
				"error": "error downloading image",
			})
		}

		_, err = c.whatsmiau.SendImage(context.Background(), &whatsmiau.SendImage{
			InstanceID: instanceID,
			RemoteJID:  &jid,
			File:       data,
			FileName:   payload.Attachments[0].FileName,
			Caption:    payload.Content,
		})

		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{
				"error": err.Error(),
			})
		}

		return ctx.JSON(http.StatusOK, map[string]string{"status": "image sent"})
	}

	// =============================
	// ENVIO DE DOCUMENTO
	// =============================
	if payload.ContentType == "file" && len(payload.Attachments) > 0 {
		data, err := downloadFile(payload.Attachments[0].URL)
		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{
				"error": "error downloading file",
			})
		}

		_, err = c.whatsmiau.SendDocument(context.Background(), &whatsmiau.SendDocument{
			InstanceID: instanceID,
			RemoteJID:  &jid,
			File:       data,
			FileName:   payload.Attachments[0].FileName,
		})

		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{
				"error": err.Error(),
			})
		}

		return ctx.JSON(http.StatusOK, map[string]string{"status": "document sent"})
	}

	// =============================
	// ENVIO DE ÁUDIO
	// =============================
	if payload.ContentType == "audio" && len(payload.Attachments) > 0 {
		data, err := downloadFile(payload.Attachments[0].URL)
		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{
				"error": "error downloading audio",
			})
		}

		_, err = c.whatsmiau.SendAudio(context.Background(), &whatsmiau.SendAudio{
			InstanceID: instanceID,
			RemoteJID:  &jid,
			File:       data,
			FileName:   payload.Attachments[0].FileName,
			PTT:        true,
		})

		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{
				"error": err.Error(),
			})
		}

		return ctx.JSON(http.StatusOK, map[string]string{"status": "audio sent"})
	}

	// =============================
	// ENVIO DE TEXTO (default)
	// =============================
	_, err = c.whatsmiau.SendText(context.Background(), &whatsmiau.SendText{
		InstanceID: instanceID,
		RemoteJID:  &jid,
		Text:       payload.Content,
	})

	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	return ctx.JSON(http.StatusOK, map[string]string{"status": "text sent"})
}

// =============================
// DOWNLOAD COM TOKEN CHATWOOT
// =============================
func downloadFile(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	token := os.Getenv("CHATWOOT_TOKEN")
	if token != "" {
		req.Header.Set("api_access_token", token)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}
