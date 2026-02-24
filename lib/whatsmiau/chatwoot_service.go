package whatsmiau

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"go.uber.org/zap"
)

type ChatwootConfig struct {
	URL       string
	AccountID string
	Token     string
	InboxID   int
	InboxName string
}

type ChatwootService struct {
	config     ChatwootConfig
	httpClient *http.Client
}

func NewChatwootService(config ChatwootConfig) *ChatwootService {
	return &ChatwootService{
		config:     config,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

//
// ─────────────────────────────────────────────────────────────────────────────
// RESPONSE TYPES
// ─────────────────────────────────────────────────────────────────────────────
//

type chatwootContact struct {
	ID int `json:"id"`
}

type chatwootContactFilterResponse struct {
	Payload []chatwootContact `json:"payload"`
}

type chatwootContactCreateResponse struct {
	Payload struct {
		Contact chatwootContact `json:"contact"`
	} `json:"payload"`
}

type chatwootConversation struct {
	ID      int    `json:"id"`
	Status  string `json:"status"`
	InboxID int    `json:"inbox_id"`
}

type chatwootConversationsResponse struct {
	Payload []chatwootConversation `json:"payload"`
}

type chatwootConversationCreateResponse struct {
	ID int `json:"id"`
}

type chatwootInbox struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type chatwootInboxListResponse struct {
	Payload []chatwootInbox `json:"payload"`
}

//
// ─────────────────────────────────────────────────────────────────────────────
// HANDLER PRINCIPAL
// ─────────────────────────────────────────────────────────────────────────────
//

func (c *ChatwootService) HandleMessage(messageData *WookMessageData) {

	if messageData == nil || (messageData.Key != nil && messageData.Key.FromMe) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 🔥 resolve inbox dinamicamente se necessário
	if c.config.InboxID == 0 && c.config.InboxName != "" {
		inboxID, err := c.ResolveInboxIDByName(ctx, c.config.InboxName)
		if err != nil {
			zap.L().Error("chatwoot: erro ao resolver inbox", zap.Error(err))
			return
		}
		c.config.InboxID = inboxID
	}

	remoteJid := ""
	if messageData.Key != nil {
		remoteJid = messageData.Key.RemoteJid
	}

	phone := extractPhone(remoteJid)
	if phone == "" {
		return
	}

	pushName := messageData.PushName
	if pushName == "" {
		pushName = phone
	}

	messageID := ""
	if messageData.Key != nil {
		messageID = messageData.Key.Id
	}

	contactID, err := c.findOrCreateContact(ctx, phone, pushName, remoteJid)
	if err != nil {
		zap.L().Error("erro contato", zap.Error(err))
		return
	}

	conversationID, err := c.findOrCreateConversation(ctx, contactID)
	if err != nil {
		zap.L().Error("erro conversa", zap.Error(err))
		return
	}

	msg := messageData.Message
	if msg == nil {
		return
	}

	// MEDIA
	if msg.Base64 != "" {

		filename, caption, mimetype := extractMediaMeta(messageData)

		mediaBytes, err := base64.StdEncoding.DecodeString(msg.Base64)
		if err != nil {
			zap.L().Error("erro base64", zap.Error(err))
			return
		}

		err = c.sendMediaMessage(ctx, conversationID, mediaBytes, filename, mimetype, caption, messageID)
		if err != nil {
			zap.L().Error("erro enviar mídia", zap.Error(err))
		}
		return
	}

	// TEXTO
	text := extractMessageText(messageData)
	if text == "" {
		return
	}

	err = c.sendMessage(ctx, conversationID, text, messageID)
	if err != nil {
		zap.L().Error("erro enviar texto", zap.Error(err))
	}
}

//
// ─────────────────────────────────────────────────────────────────────────────
// INBOX RESOLVER
// ─────────────────────────────────────────────────────────────────────────────
//

func (c *ChatwootService) ResolveInboxIDByName(ctx context.Context, inboxName string) (int, error) {

	url := fmt.Sprintf("%s/api/v1/accounts/%s/inboxes",
		c.config.URL,
		c.config.AccountID,
	)

	resp, err := c.doRequest(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result chatwootInboxListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	for _, inbox := range result.Payload {
		if strings.EqualFold(inbox.Name, inboxName) {
			return inbox.ID, nil
		}
	}

	return 0, fmt.Errorf("inbox '%s' não encontrada", inboxName)
}

//
// ─────────────────────────────────────────────────────────────────────────────
// CONTATOS
// ─────────────────────────────────────────────────────────────────────────────
//

func (c *ChatwootService) findOrCreateContact(ctx context.Context, phone, name, identifier string) (int, error) {

	id, err := c.searchContact(ctx, phone)
	if err != nil {
		return 0, err
	}
	if id > 0 {
		return id, nil
	}
	return c.createContact(ctx, phone, name, identifier)
}

func (c *ChatwootService) searchContact(ctx context.Context, phone string) (int, error) {

	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts/filter",
		c.config.URL,
		c.config.AccountID,
	)

	body := map[string]interface{}{
		"payload": []map[string]interface{}{
			{
				"attribute_key":   "phone_number",
				"filter_operator": "equal_to",
				"values":          []string{fmt.Sprintf("+%s", phone)},
			},
		},
	}

	resp, err := c.doRequest(ctx, "POST", url, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result chatwootContactFilterResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	if len(result.Payload) > 0 {
		return result.Payload[0].ID, nil
	}

	return 0, nil
}

func (c *ChatwootService) createContact(ctx context.Context, phone, name, identifier string) (int, error) {

	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts",
		c.config.URL,
		c.config.AccountID,
	)

	body := map[string]interface{}{
		"name":         name,
		"phone_number": fmt.Sprintf("+%s", phone),
		"identifier":   identifier,
	}

	resp, err := c.doRequest(ctx, "POST", url, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result chatwootContactCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	return result.Payload.Contact.ID, nil
}

//
// ─────────────────────────────────────────────────────────────────────────────
// CONVERSAS
// ─────────────────────────────────────────────────────────────────────────────
//

func (c *ChatwootService) findOrCreateConversation(ctx context.Context, contactID int) (int, error) {

	id, err := c.findOpenConversation(ctx, contactID)
	if err != nil {
		return 0, err
	}
	if id > 0 {
		return id, nil
	}

	return c.createConversation(ctx, contactID)
}

func (c *ChatwootService) findOpenConversation(ctx context.Context, contactID int) (int, error) {

	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts/%d/conversations",
		c.config.URL,
		c.config.AccountID,
		contactID,
	)

	resp, err := c.doRequest(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result chatwootConversationsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	for _, conv := range result.Payload {
		if conv.InboxID == c.config.InboxID && conv.Status != "resolved" {
			return conv.ID, nil
		}
	}

	return 0, nil
}

func (c *ChatwootService) createConversation(ctx context.Context, contactID int) (int, error) {

	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations",
		c.config.URL,
		c.config.AccountID,
	)

	body := map[string]interface{}{
		"contact_id": contactID,
		"inbox_id":   c.config.InboxID,
	}

	resp, err := c.doRequest(ctx, "POST", url, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result chatwootConversationCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	return result.ID, nil
}

//
// ─────────────────────────────────────────────────────────────────────────────
// ENVIO TEXTO
// ─────────────────────────────────────────────────────────────────────────────
//

func (c *ChatwootService) sendMessage(ctx context.Context, conversationID int, content, messageID string) error {

	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages",
		c.config.URL,
		c.config.AccountID,
		conversationID,
	)

	body := map[string]interface{}{
		"content":      content,
		"message_type": "incoming",
		"private":      false,
		"source_id":    fmt.Sprintf("WAID:%s", messageID),
	}

	resp, err := c.doRequest(ctx, "POST", url, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

//
// ─────────────────────────────────────────────────────────────────────────────
// ENVIO MIDIA
// ─────────────────────────────────────────────────────────────────────────────
//

func (c *ChatwootService) sendMediaMessage(
	ctx context.Context,
	conversationID int,
	mediaBytes []byte,
	filename, mimetype, caption, messageID string,
) error {

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	_ = writer.WriteField("message_type", "incoming")
	_ = writer.WriteField("private", "false")
	_ = writer.WriteField("source_id", fmt.Sprintf("WAID:%s", messageID))

	if caption != "" {
		_ = writer.WriteField("content", caption)
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="attachments[]"; filename="%s"`, filename))
	h.Set("Content-Type", mimetype)

	part, err := writer.CreatePart(h)
	if err != nil {
		return err
	}

	_, err = part.Write(mediaBytes)
	if err != nil {
		return err
	}

	writer.Close()

	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages",
		c.config.URL,
		c.config.AccountID,
		conversationID,
	)

	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return err
	}

	req.Header.Set("api_access_token", c.config.Token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

//
// ─────────────────────────────────────────────────────────────────────────────
// HTTP
// ─────────────────────────────────────────────────────────────────────────────
//

func (c *ChatwootService) doRequest(ctx context.Context, method, url string, body interface{}) (*http.Response, error) {

	var bodyReader io.Reader

	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("api_access_token", c.config.Token)
	req.Header.Set("Content-Type", "application/json")

	return c.httpClient.Do(req)
}

//
// ─────────────────────────────────────────────────────────────────────────────
// HELPERS
// ─────────────────────────────────────────────────────────────────────────────
//

func extractPhone(remoteJid string) string {
	parts := strings.Split(remoteJid, "@")
	if len(parts) == 0 {
		return ""
	}
	return strings.Split(parts[0], ":")[0]
}
