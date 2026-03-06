package whatsmiau

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/verbeux-ai/whatsmiau/env"
	"go.uber.org/zap"
)

type ChatwootConfig struct {
	URL       string
	AccountID string
	Token     string
	InboxID   int
}

type ChatwootService struct {
	config     ChatwootConfig
	httpClient *http.Client
	db         *sql.DB

	inboxCache   map[string]int
	inboxCacheMu sync.RWMutex
}

func NewChatwootService(config ChatwootConfig) *ChatwootService {
	service := &ChatwootService{
		config:     config,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		inboxCache: make(map[string]int),
	}

	if config.URL == "" || config.Token == "" || config.AccountID == "" {
		zap.L().Info("chatwoot: desabilitado para esta instância (config incompleta)")
		return service
	}

	zap.L().Info("chatwoot: serviço HABILITADO")

	if env.Env.ChatwootImportDatabaseConnectionURI != "" {
		db, err := sql.Open("postgres", env.Env.ChatwootImportDatabaseConnectionURI)
		if err != nil {
			zap.L().Error("chatwoot: erro ao conectar no PostgreSQL", zap.Error(err))
		} else {
			db.SetMaxOpenConns(10)
			db.SetMaxIdleConns(5)
			db.SetConnMaxLifetime(time.Hour)
			if err := db.Ping(); err != nil {
				zap.L().Error("chatwoot: erro ao pingar PostgreSQL", zap.Error(err))
				db.Close()
			} else {
				service.db = db
				zap.L().Info("chatwoot: ✅ CONECTADO ao PostgreSQL")
			}
		}
	}

	return service
}

func maskPassword(uri string) string {
	if strings.Contains(uri, "@") && strings.Contains(uri, "://") {
		parts := strings.Split(uri, "://")
		if len(parts) == 2 {
			afterScheme := parts[1]
			if strings.Contains(afterScheme, "@") {
				userHostParts := strings.SplitN(afterScheme, "@", 2)
				if strings.Contains(userHostParts[0], ":") {
					userParts := strings.SplitN(userHostParts[0], ":", 2)
					return parts[0] + "://" + userParts[0] + ":***@" + userHostParts[1]
				}
			}
		}
	}
	return uri
}

func (c *ChatwootService) Close() error {
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}

func (c *ChatwootService) IsEnabled() bool {
	return c.config.URL != "" && c.config.Token != "" && c.config.AccountID != ""
}

func (c *ChatwootService) getInboxIDForInstance(ctx context.Context, instanceID string) (int, error) {
	c.inboxCacheMu.RLock()
	if id, ok := c.inboxCache[instanceID]; ok {
		c.inboxCacheMu.RUnlock()
		return id, nil
	}
	c.inboxCacheMu.RUnlock()

	id, err := c.findInboxByName(ctx, instanceID)
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, fmt.Errorf("inbox não encontrada para instância '%s'", instanceID)
	}

	c.inboxCacheMu.Lock()
	c.inboxCache[instanceID] = id
	c.inboxCacheMu.Unlock()

	return id, nil
}

func (c *ChatwootService) findInboxByName(ctx context.Context, name string) (int, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/inboxes", c.config.URL, c.config.AccountID)
	resp, err := c.doRequest(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var listResp chatwootInboxListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return 0, err
	}
	for _, inbox := range listResp.Payload {
		if inbox.Name == name {
			return inbox.ID, nil
		}
	}
	return 0, nil
}

// ── Tipos ─────────────────────────────────────────────────────────────────────

type chatwootContact struct{ ID int `json:"id"` }
type chatwootContactFilterResponse struct{ Payload []chatwootContact `json:"payload"` }
type chatwootContactCreateResponse struct{ Payload chatwootContact `json:"payload"` }
type chatwootConversation struct {
	ID      int    `json:"id"`
	Status  string `json:"status"`
	InboxID int    `json:"inbox_id"`
}
type chatwootConversationsResponse struct{ Payload []chatwootConversation `json:"payload"` }
type chatwootConversationCreateResponse struct{ ID int `json:"id"` }
type chatwootInbox struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
type chatwootInboxListResponse struct{ Payload []chatwootInbox `json:"payload"` }
type chatwootInboxCreateResponse struct{ ID int `json:"id"` }

// ── Init Instance ─────────────────────────────────────────────────────────────

func (c *ChatwootService) InitInstance(ctx context.Context, inboxName, webhookURL, organization, logo string) (int, error) {
	if !c.IsEnabled() {
		return 0, fmt.Errorf("chatwoot não está habilitado")
	}

	inboxID, err := c.findOrCreateInbox(ctx, inboxName, webhookURL)
	if err != nil {
		return 0, fmt.Errorf("erro ao buscar/criar inbox: %w", err)
	}

	c.inboxCacheMu.Lock()
	c.inboxCache[inboxName] = inboxID
	c.inboxCacheMu.Unlock()

	orgName := organization
	if orgName == "" {
		orgName = "WhatsMiau"
	}
	logoURL := logo
	if logoURL == "" {
		logoURL = "https://evolution-api.com/files/evolution-api-favicon.png"
	}

	botContactID, err := c.findOrCreateBotContact(ctx, inboxID, orgName, logoURL)
	if err != nil {
		zap.L().Warn("chatwoot: erro ao criar contato bot (não crítico)", zap.Error(err))
	} else if botContactID > 0 {
		if err := c.createBotConversation(ctx, botContactID, inboxID); err != nil {
			zap.L().Warn("chatwoot: erro ao criar conversa bot (não crítico)", zap.Error(err))
		}
	}

	return inboxID, nil
}

func (c *ChatwootService) findOrCreateInbox(ctx context.Context, inboxName, webhookURL string) (int, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/inboxes", c.config.URL, c.config.AccountID)
	resp, err := c.doRequest(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var listResp chatwootInboxListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return 0, err
	}
	for _, inbox := range listResp.Payload {
		if inbox.Name == inboxName {
			return inbox.ID, nil
		}
	}
	return c.createInbox(ctx, inboxName, webhookURL)
}

func (c *ChatwootService) createInbox(ctx context.Context, name, webhookURL string) (int, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/inboxes", c.config.URL, c.config.AccountID)
	body := map[string]interface{}{
		"name": name,
		"channel": map[string]interface{}{
			"type":        "api",
			"webhook_url": webhookURL,
		},
	}
	resp, err := c.doRequest(ctx, "POST", url, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("erro ao criar inbox: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var result chatwootInboxCreateResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return 0, err
	}
	if result.ID <= 0 {
		return 0, fmt.Errorf("inbox criada com ID inválido: %s", string(bodyBytes))
	}
	return result.ID, nil
}

func (c *ChatwootService) findOrCreateBotContact(ctx context.Context, inboxID int, orgName, logoURL string) (int, error) {
	if id, err := c.searchContact(ctx, "123456"); err == nil && id > 0 {
		return id, nil
	}

	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts", c.config.URL, c.config.AccountID)
	body := map[string]interface{}{
		"name":         orgName,
		"phone_number": "+123456",
		"avatar_url":   logoURL,
		"inbox_id":     inboxID,
	}
	resp, err := c.doRequest(ctx, "POST", url, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("erro ao criar contato bot: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var result chatwootContactCreateResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return 0, err
	}
	if result.Payload.ID <= 0 {
		return 0, fmt.Errorf("contato bot criado com ID inválido")
	}
	return result.Payload.ID, nil
}

func (c *ChatwootService) createBotConversation(ctx context.Context, contactID, inboxID int) error {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations", c.config.URL, c.config.AccountID)
	body := map[string]interface{}{
		"contact_id": fmt.Sprintf("%d", contactID),
		"inbox_id":   fmt.Sprintf("%d", inboxID),
	}
	resp, err := c.doRequest(ctx, "POST", url, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("erro ao criar conversa bot: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var conv chatwootConversationCreateResponse
	if err := json.Unmarshal(bodyBytes, &conv); err != nil {
		return err
	}
	if conv.ID <= 0 {
		return fmt.Errorf("conversa bot criada com ID inválido")
	}

	msgURL := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages",
		c.config.URL, c.config.AccountID, conv.ID)
	msgResp, err := c.doRequest(ctx, "POST", msgURL, map[string]interface{}{
		"content":      "init",
		"message_type": "outgoing",
	})
	if err != nil {
		return err
	}
	msgResp.Body.Close()
	return nil
}

// ── Handler principal ─────────────────────────────────────────────────────────

func (c *ChatwootService) HandleMessage(instanceID string, messageData *WookMessageData) {
	if !c.IsEnabled() || messageData == nil || messageData.Key == nil {
		return
	}

	isFromMe := messageData.Key.FromMe

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	inboxID, err := c.getInboxIDForInstance(ctx, instanceID)
	if err != nil {
		zap.L().Error("chatwoot: inbox não encontrada", zap.String("instanceID", instanceID), zap.Error(err))
		return
	}

	remoteJid := messageData.Key.RemoteJid
	phone := extractPhone(remoteJid)
	if phone == "" {
		return
	}

	pushName := messageData.PushName
	if pushName == "" {
		pushName = phone
	}

	messageID := messageData.Key.Id
	isFromMe2 := messageData.Key.FromMe

	// ── Quoted message: extrai o StanzaId do contextInfo ──────────────────────
	// O StanzaId é o ID da mensagem original no WhatsApp.
	// Enviamos como "WAID:<stanzaId>" para o Chatwoot via in_reply_to_external_id,
	// que faz o link automático com a mensagem que já está na conversa.
	var quotedMessageID string
	if messageData.ContextInfo != nil && messageData.ContextInfo.StanzaId != "" {
		quotedMessageID = fmt.Sprintf("WAID:%s", messageData.ContextInfo.StanzaId)
		zap.L().Debug("chatwoot: mensagem é reply",
			zap.String("quotedMessageID", quotedMessageID),
			zap.String("messageID", messageID))
	}

	contactID, err := c.findOrCreateContact(ctx, phone, pushName, remoteJid)
	if err != nil || contactID <= 0 {
		zap.L().Error("chatwoot: erro ao buscar/criar contato", zap.Error(err))
		return
	}

	conversationID, err := c.findOrCreateConversation(ctx, contactID, inboxID)
	if err != nil || conversationID <= 0 {
		zap.L().Error("chatwoot: erro ao buscar/criar conversa", zap.Error(err))
		return
	}

	msg := messageData.Message
	if msg == nil {
		return
	}

	if msg.Base64 != "" {
		filename, caption, mimetype := extractMediaMeta(messageData)
		mediaBytes, err := base64.StdEncoding.DecodeString(msg.Base64)
		if err != nil {
			zap.L().Error("chatwoot: erro ao decodificar base64", zap.Error(err))
			return
		}
		if err := c.sendMediaMessage(ctx, conversationID, mediaBytes, filename, mimetype, caption, messageID, isFromMe2, quotedMessageID); err != nil {
			zap.L().Error("chatwoot: erro ao enviar mídia", zap.Error(err))
		}
		return
	}

	messageText := extractMessageText(messageData)
	if messageText == "" {
		return
	}

	if err := c.sendMessage(ctx, conversationID, messageText, messageID, isFromMe, quotedMessageID); err != nil {
		zap.L().Error("chatwoot: erro ao enviar mensagem", zap.Error(err))
	}
}

// ── Duplicatas ────────────────────────────────────────────────────────────────

func (c *ChatwootService) checkMessageExists(ctx context.Context, conversationID int, sourceID string) (bool, error) {
	if c.db == nil || conversationID <= 0 {
		return false, nil
	}
	var count int
	err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE conversation_id = $1 AND source_id = $2 LIMIT 1`,
		conversationID, sourceID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ── Contatos ──────────────────────────────────────────────────────────────────

func (c *ChatwootService) findOrCreateContact(ctx context.Context, phone, name, identifier string) (int, error) {
	if id, err := c.searchContact(ctx, phone); err == nil && id > 0 {
		return id, nil
	}
	return c.createContact(ctx, phone, name, identifier)
}

func (c *ChatwootService) searchContact(ctx context.Context, phone string) (int, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts/filter", c.config.URL, c.config.AccountID)
	body := map[string]interface{}{
		"payload": []map[string]interface{}{
			{
				"attribute_key":   "phone_number",
				"filter_operator": "equal_to",
				"values":          []string{fmt.Sprintf("+%s", phone)},
				"query_operator":  nil,
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
	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts", c.config.URL, c.config.AccountID)
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

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var result chatwootContactCreateResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return 0, err
	}
	if result.Payload.ID <= 0 {
		return 0, fmt.Errorf("API retornou contactId inválido")
	}
	return result.Payload.ID, nil
}

// ── Conversas ─────────────────────────────────────────────────────────────────

func (c *ChatwootService) findOrCreateConversation(ctx context.Context, contactID, inboxID int) (int, error) {
	if contactID <= 0 {
		return 0, fmt.Errorf("contactID inválido: %d", contactID)
	}
	if id, err := c.findOrReopenConversation(ctx, contactID, inboxID); err != nil {
		return 0, err
	} else if id > 0 {
		return id, nil
	}
	return c.createConversation(ctx, contactID, inboxID)
}

func (c *ChatwootService) findOrReopenConversation(ctx context.Context, contactID, inboxID int) (int, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts/%d/conversations",
		c.config.URL, c.config.AccountID, contactID)
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
		if conv.InboxID != inboxID {
			continue
		}
		if conv.Status == "resolved" {
			if err := c.reopenConversation(ctx, conv.ID); err != nil {
				zap.L().Warn("chatwoot: erro ao reabrir conversa", zap.Error(err))
			}
		}
		return conv.ID, nil
	}
	return 0, nil
}

func (c *ChatwootService) reopenConversation(ctx context.Context, conversationID int) error {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/toggle_status",
		c.config.URL, c.config.AccountID, conversationID)
	resp, err := c.doRequest(ctx, "POST", url, map[string]interface{}{"status": "open"})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("erro ao reabrir conversa: %d - %s", resp.StatusCode, string(raw))
	}
	return nil
}

func (c *ChatwootService) createConversation(ctx context.Context, contactID, inboxID int) (int, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations", c.config.URL, c.config.AccountID)
	resp, err := c.doRequest(ctx, "POST", url, map[string]interface{}{
		"contact_id": contactID,
		"inbox_id":   inboxID,
	})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("erro ao criar conversa: %d - %s", resp.StatusCode, string(raw))
	}

	var result chatwootConversationCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result.ID, nil
}

// ── Envio de mensagens ────────────────────────────────────────────────────────

// sendMessage envia texto ao Chatwoot.
// quotedMessageID = "WAID:<stanzaId>" para replies, "" para mensagens normais.
func (c *ChatwootService) sendMessage(ctx context.Context, conversationID int, content, messageID string, isFromMe bool, quotedMessageID string) error {
	sourceID := fmt.Sprintf("WAID:%s", messageID)

	messageType := "incoming"
	if isFromMe {
		messageType = "outgoing"
	}

	if exists, err := c.checkMessageExists(ctx, conversationID, sourceID); err == nil && exists {
		return nil
	}

	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages",
		c.config.URL, c.config.AccountID, conversationID)

	body := map[string]interface{}{
		"content":      content,
		"message_type": messageType,
		"private":      false,
		"source_id":    sourceID,
	}

	// Quoted message: o Chatwoot usa in_reply_to_external_id para buscar
	// internamente qual mensagem tem esse source_id e fazer o link visual.
	if quotedMessageID != "" {
		body["content_attributes"] = map[string]interface{}{
			"in_reply_to_external_id": quotedMessageID,
		}
	}

	resp, err := c.doRequest(ctx, "POST", url, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chatwoot erro %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// sendMediaMessage envia mídia (multipart) ao Chatwoot.
// quotedMessageID = "WAID:<stanzaId>" para replies, "" para mensagens normais.
func (c *ChatwootService) sendMediaMessage(
	ctx context.Context,
	conversationID int,
	mediaBytes []byte,
	filename, mimetype, caption, messageID string,
	isFromMe bool,
	quotedMessageID string,
) error {
	sourceID := fmt.Sprintf("WAID:%s", messageID)

	messageType := "incoming"
	if isFromMe {
		messageType = "outgoing"
	}

	if exists, err := c.checkMessageExists(ctx, conversationID, sourceID); err == nil && exists {
		return nil
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	_ = writer.WriteField("message_type", messageType)
	_ = writer.WriteField("private", "false")
	_ = writer.WriteField("source_id", sourceID)

	if caption != "" {
		_ = writer.WriteField("content", caption)
	}

	// Quoted message via multipart: JSON serializado no campo content_attributes
	if quotedMessageID != "" {
		contentAttrs, _ := json.Marshal(map[string]interface{}{
			"in_reply_to_external_id": quotedMessageID,
		})
		_ = writer.WriteField("content_attributes", string(contentAttrs))
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="attachments[]"; filename="%s"`, filename))
	h.Set("Content-Type", mimetype)

	part, err := writer.CreatePart(h)
	if err != nil {
		return fmt.Errorf("erro ao criar form part: %w", err)
	}
	if _, err := part.Write(mediaBytes); err != nil {
		return fmt.Errorf("erro ao escrever bytes: %w", err)
	}
	writer.Close()

	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages",
		c.config.URL, c.config.AccountID, conversationID)

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

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chatwoot erro %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// ── HTTP helper ───────────────────────────────────────────────────────────────

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

// ── Helpers ───────────────────────────────────────────────────────────────────

func extractPhone(remoteJid string) string {
	parts := strings.Split(remoteJid, "@")
	if len(parts) == 0 {
		return ""
	}
	return strings.Split(parts[0], ":")[0]
}

func extractMediaMeta(data *WookMessageData) (filename, caption, mimetype string) {
	if data.Message == nil {
		return "file.bin", "", "application/octet-stream"
	}

	msg := data.Message
	id := ""
	if data.Key != nil {
		id = data.Key.Id
	}

	switch {
	case msg.AudioMessage != nil:
		raw := msg.AudioMessage.Mimetype
		if strings.Contains(raw, "ogg") {
			mimetype = "audio/ogg"
			filename = fmt.Sprintf("%s.ogg", id)
		} else if strings.Contains(raw, "mp4") {
			mimetype = "audio/mp4"
			filename = fmt.Sprintf("%s.m4a", id)
		} else {
			mimetype = "audio/ogg"
			filename = fmt.Sprintf("%s.ogg", id)
		}
	case msg.VideoMessage != nil:
		mimetype = cleanMimetype(msg.VideoMessage.Mimetype, "video/mp4")
		filename = fmt.Sprintf("%s.mp4", id)
		caption = msg.VideoMessage.Caption
	case msg.ImageMessage != nil:
		mimetype = cleanMimetype(msg.ImageMessage.Mimetype, "image/jpeg")
		ext := mimetypeToExt(mimetype, "jpg")
		filename = fmt.Sprintf("%s.%s", id, ext)
		caption = msg.ImageMessage.Caption
	case msg.DocumentMessage != nil:
		mimetype = cleanMimetype(msg.DocumentMessage.Mimetype, "application/octet-stream")
		filename = msg.DocumentMessage.FileName
		if filename == "" {
			filename = fmt.Sprintf("%s.%s", id, mimetypeToExt(mimetype, "bin"))
		}
		caption = msg.DocumentMessage.Caption
	case msg.StickerMessage != nil:
		mimetype = cleanMimetype(msg.StickerMessage.Mimetype, "image/webp")
		if mimetype == "" || mimetype == "application/octet-stream" {
			mimetype = "image/webp"
		}
		filename = fmt.Sprintf("%s.webp", id)
	default:
		filename = fmt.Sprintf("%s.bin", id)
		mimetype = "application/octet-stream"
	}
	return
}

func cleanMimetype(raw, fallback string) string {
	if raw == "" {
		return fallback
	}
	clean := strings.TrimSpace(strings.SplitN(raw, ";", 2)[0])
	if clean == "" {
		return fallback
	}
	return clean
}

func mimetypeToExt(mimetype, fallback string) string {
	switch mimetype {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "video/mp4":
		return "mp4"
	case "video/3gpp":
		return "3gp"
	case "audio/ogg":
		return "ogg"
	case "audio/mp4":
		return "m4a"
	case "application/pdf":
		return "pdf"
	default:
		return fallback
	}
}

func extractMessageText(data *WookMessageData) string {
	if data.Message == nil {
		return ""
	}
	msg := data.Message
	if msg.Conversation != "" {
		return msg.Conversation
	}
	if msg.ImageMessage != nil && msg.ImageMessage.Caption != "" {
		return msg.ImageMessage.Caption
	}
	if msg.VideoMessage != nil && msg.VideoMessage.Caption != "" {
		return msg.VideoMessage.Caption
	}
	if msg.DocumentMessage != nil {
		if msg.DocumentMessage.Caption != "" {
			return msg.DocumentMessage.Caption
		}
		if msg.DocumentMessage.FileName != "" {
			return fmt.Sprintf("[Documento: %s]", msg.DocumentMessage.FileName)
		}
	}
	if msg.ContactMessage != nil {
		re := regexp.MustCompile(`waid=(\d+)`)
		match := re.FindStringSubmatch(msg.ContactMessage.VCard)
		telefone := ""
		if len(match) > 1 {
			telefone = match[1]
		}
		return fmt.Sprintf("Contato:\nNome: %s\nTelefone: %s", msg.ContactMessage.DisplayName, telefone)
	}
	if msg.LocationMessage != nil {
		lat := msg.LocationMessage.DegreesLatitude
		lng := msg.LocationMessage.DegreesLongitude
		return fmt.Sprintf(
			"📍 *Localização*\n\n*Nome:* %s\n*Endereço:* %s\n*Latitude:* %.6f\n*Longitude:* %.6f\n\n🌎 *Mapa:* https://www.google.com/maps?q=%f,%f",
			msg.LocationMessage.Name, msg.LocationMessage.Address, lat, lng, lat, lng,
		)
	}
	if msg.StickerMessage != nil {
		if msg.StickerMessage.IsAnimated {
			return "🖼️ *Sticker animado*"
		}
		return "🖼️ *Sticker*"
	}
	if msg.ReactionMessage != nil {
		return fmt.Sprintf("[Reação: %s]", msg.ReactionMessage.Text)
	}
	return ""
}
