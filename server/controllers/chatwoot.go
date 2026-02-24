package whatsmiau

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.mau.fi/whatsmeow/types"
)

type Chatwoot struct {
	whatsmiau *Whatsmiau
}

func NewChatwoot(w *Whatsmiau) *Chatwoot {
	return &Chatwoot{
		whatsmiau: w,
	}
}

//
// ==============================
// TYPING ON
// ==============================
//
func (c *Chatwoot) handleTypingOn(ctx context.Context, instanceID string, jid types.JID) error {
	return c.whatsmiau.ChatPresence(&ChatPresenceRequest{
		InstanceID: instanceID,
		RemoteJID:  &jid, // ✅ CORRIGIDO
		Presence:   types.ChatPresenceComposing,
	})
}

//
// ==============================
// TYPING OFF
// ==============================
//
func (c *Chatwoot) handleTypingOff(ctx context.Context, instanceID string, jid types.JID) error {
	return c.whatsmiau.ChatPresence(&ChatPresenceRequest{
		InstanceID: instanceID,
		RemoteJID:  &jid, // ✅ CORRIGIDO
		Presence:   types.ChatPresencePaused,
	})
}

//
// ==============================
// ENVIO TEXTO
// ==============================
//
func (c *Chatwoot) sendTextMessage(ctx context.Context, instanceID string, jid types.JID, content string) error {
	return c.whatsmiau.SendText(&SendTextRequest{
		InstanceID: instanceID,
		RemoteJID:  &jid, // ✅ CORRIGIDO
		Text:       content,
	})
}

//
// ==============================
// ENVIO IMAGEM
// ==============================
//
func (c *Chatwoot) sendImage(ctx context.Context, instanceID string, jid types.JID, imageURL string, caption string) error {
	return c.whatsmiau.SendImage(&SendImageRequest{
		InstanceID: instanceID,
		RemoteJID:  &jid, // ✅ CORRIGIDO
		URL:        imageURL,
		Caption:    caption,
	})
}

//
// ==============================
// ENVIO AUDIO
// ==============================
//
func (c *Chatwoot) sendAudio(ctx context.Context, instanceID string, jid types.JID, audioURL string) error {
	return c.whatsmiau.SendAudio(&SendAudioRequest{
		InstanceID: instanceID,
		RemoteJID:  &jid, // ✅ CORRIGIDO
		URL:        audioURL,
	})
}

//
// ==============================
// ENVIO DOCUMENTO
// ==============================
//
func (c *Chatwoot) sendDocument(ctx context.Context, instanceID string, jid types.JID, documentURL string, fileName string) error {
	return c.whatsmiau.SendDocument(&SendDocumentRequest{
		InstanceID: instanceID,
		RemoteJID:  &jid, // ✅ CORRIGIDO
		URL:        documentURL,
		FileName:   fileName,
	})
}

//
// ==============================
// OUTGOING MESSAGE HANDLER
// ==============================
//
func (c *Chatwoot) handleOutgoingMessage(
	ctx context.Context,
	instanceID string,
	jid types.JID,
	messageType string,
	content string,
	mediaURL string,
	fileName string,
) error {

	// typing ON
	if err := c.handleTypingOn(ctx, instanceID, jid); err != nil {
		return err
	}

	time.Sleep(1 * time.Second)

	var err error

	switch messageType {

	case "text":
		err = c.sendTextMessage(ctx, instanceID, jid, content)

	case "image":
		err = c.sendImage(ctx, instanceID, jid, mediaURL, content)

	case "audio":
		err = c.sendAudio(ctx, instanceID, jid, mediaURL)

	case "document":
		err = c.sendDocument(ctx, instanceID, jid, mediaURL, fileName)

	default:
		err = fmt.Errorf("tipo de mensagem não suportado: %s", messageType)
	}

	// typing OFF
	_ = c.handleTypingOff(ctx, instanceID, jid)

	return err
}

//
// ==============================
// CHATWOOT → WHATSAPPIAU WEBHOOK
// ==============================
//
func (c *Chatwoot) HandleChatwootWebhook(w http.ResponseWriter, r *http.Request) {

	var payload map[string]interface{}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// aqui você pode extrair:
	// conversation
	// message_type
	// content
	// attachments
	// etc.

	w.WriteHeader(http.StatusOK)
}

//
// ==============================
// WHATSAPPIAU → CHATWOOT
// ==============================
//
func (c *Chatwoot) sendToChatwoot(webhookURL string, data interface{}) error {

	body, err := json.Marshal(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("chatwoot respondeu status %d", resp.StatusCode)
	}

	return nil
}
