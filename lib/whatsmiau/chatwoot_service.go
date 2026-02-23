package whatsmiau

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ChatwootConfig configuração da integração
type ChatwootConfig struct {
	URL       string
	AccountID string
	Token     string
	InboxID   int
}

// ChatwootService gerencia a integração com o Chatwoot
type ChatwootService struct {
	config     ChatwootConfig
	httpClient *http.Client
}

// NewChatwootService cria uma nova instância do serviço
func NewChatwootService(config ChatwootConfig) *ChatwootService {
	return &ChatwootService{
		config:     config,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// =============================================================================
// Structs de resposta do Chatwoot
// =============================================================================

type chatwootContact struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	PhoneNumber string `json:"phone_number"`
	Identifier  string `json:"identifier"`
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

// =============================================================================
// HandleMessage — ponto de entrada principal
// =============================================================================

func (c *ChatwootService) HandleMessage(messageData *WookMessageData) {
	if messageData == nil {
		return
	}

	// Ignorar mensagens enviadas por mim
	if messageData.Key != nil && messageData.Key.FromMe {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	remoteJid := ""
	if messageData.Key != nil {
		remoteJid = messageData.Key.RemoteJid
	}

	phone := extractPhone(remoteJid)
	if phone == "" {
		zap.L().Warn("chatwoot: número inválido", zap.String("remoteJid", remoteJid))
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

	// 1. Buscar ou criar contato
	contactID, err := c.findOrCreateContact(ctx, phone, pushName, remoteJid)
	if err != nil {
		zap.L().Error("chatwoot: erro ao buscar/criar contato", zap.Error(err))
		return
	}

	// 2. Buscar ou criar conversa
	conversationID, err := c.findOrCreateConversation(ctx, contactID)
	if err != nil {
		zap.L().Error("chatwoot: erro ao buscar/criar conversa", zap.Error(err))
		return
	}

	// 3. Verificar se tem mídia
	mediaURL, fileName, mimeType := extractMedia(messageData)
	caption := extractCaption(messageData)
	messageText := extractMessageText(messageData)

	if mediaURL != "" {
		// Enviar mídia como attachment
		if err := c.sendAttachment(ctx, conversationID, mediaURL, fileName, mimeType, caption, messageID); err != nil {
			zap.L().Error("chatwoot: erro ao enviar mídia", zap.Error(err))
			// fallback: enviar como texto
			_ = c.sendMessage(ctx, conversationID, messageText, messageID)
		}
	} else {
		// Enviar mensagem de texto
		if messageText == "" {
			messageText = "[Mensagem não suportada]"
		}
		if err := c.sendMessage(ctx, conversationID, messageText, messageID); err != nil {
			zap.L().Error("chatwoot: erro ao enviar mensagem", zap.Error(err))
			return
		}
	}

	zap.L().Info("chatwoot: mensagem enviada com sucesso",
		zap.String("phone", phone),
		zap.Int("conversationId", conversationID),
		zap.String("tipo", messageData.MessageType),
	)
}

// =============================================================================
// findOrCreateContact
// =============================================================================

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

	var result chatwootContactCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result.Payload.Contact.ID, nil
}

// =============================================================================
// findOrCreateConversation
// =============================================================================

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
	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts/%d/conversations", c.config.URL, c.config.AccountID, contactID)

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
	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations", c.config.URL, c.config.AccountID)

	body := map[string]interface{}{
		"contact_id": fmt.Sprintf("%d", contactID),
		"inbox_id":   fmt.Sprintf("%d", c.config.InboxID),
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

// =============================================================================
// sendMessage — texto simples
// =============================================================================

func (c *ChatwootService) sendMessage(ctx context.Context, conversationID int, content, messageID string) error {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages", c.config.URL, c.config.AccountID, conversationID)

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

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chatwoot erro %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// =============================================================================
// sendAttachment — mídia via multipart (foto, vídeo, áudio, documento)
// =============================================================================

func (c *ChatwootService) sendAttachment(ctx context.Context, conversationID int, mediaURL, fileName, mimeType, caption, messageID string) error {
	// 1. Baixar o arquivo da URL
	req, err := http.NewRequestWithContext(ctx, "GET", mediaURL, nil)
	if err != nil {
		return fmt.Errorf("erro ao criar request de download: %w", err)
	}

	dlResp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("erro ao baixar mídia: %w", err)
	}
	defer dlResp.Body.Close()

	fileData, err := io.ReadAll(dlResp.Body)
	if err != nil {
		return fmt.Errorf("erro ao ler mídia: %w", err)
	}

	// 2. Montar multipart
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	_ = writer.WriteField("message_type", "incoming")
	_ = writer.WriteField("source_id", fmt.Sprintf("WAID:%s", messageID))

	if caption != "" {
		_ = writer.WriteField("content", caption)
	}

	part, err := writer.CreateFormFile("attachments[]", fileName)
	if err != nil {
		return fmt.Errorf("erro ao criar form file: %w", err)
	}
	if _, err := part.Write(fileData); err != nil {
		return fmt.Errorf("erro ao escrever arquivo no form: %w", err)
	}

	writer.Close()

	// 3. Enviar para o Chatwoot
	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages",
		c.config.URL, c.config.AccountID, conversationID)

	postReq, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return err
	}

	postReq.Header.Set("api_access_token", c.config.Token)
	postReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(postReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chatwoot attachment erro %d: %s", resp.StatusCode, string(raw))
	}

	_ = mimeType
	return nil
}

// =============================================================================
// Helpers
// =============================================================================

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

// extractPhone extrai o número limpo do remoteJid
func extractPhone(remoteJid string) string {
	parts := strings.Split(remoteJid, "@")
	if len(parts) == 0 {
		return ""
	}
	return strings.Split(parts[0], ":")[0]
}

// extractMedia retorna (mediaURL, fileName, mimeType) se a mensagem tiver mídia
func extractMedia(data *WookMessageData) (string, string, string) {
	if data.Message == nil {
		return "", "", ""
	}
	msg := data.Message

	if msg.MediaURL != "" {
		fileName := guessFileName(data.MessageType, msg)
		mimeType := guessMimeType(data.MessageType)
		return msg.MediaURL, fileName, mimeType
	}

	return "", "", ""
}

// extractCaption retorna a legenda da mídia (se houver)
func extractCaption(data *WookMessageData) string {
	if data.Message == nil {
		return ""
	}
	msg := data.Message

	if msg.ImageMessage != nil && msg.ImageMessage.Caption != "" {
		return msg.ImageMessage.Caption
	}
	if msg.VideoMessage != nil && msg.VideoMessage.Caption != "" {
		return msg.VideoMessage.Caption
	}
	if msg.DocumentMessage != nil && msg.DocumentMessage.Caption != "" {
		return msg.DocumentMessage.Caption
	}
	return ""
}

// extractMessageText extrai o texto para mensagens sem mídia
func extractMessageText(data *WookMessageData) string {
	if data.Message == nil {
		return ""
	}
	msg := data.Message

	if msg.Conversation != "" {
		return msg.Conversation
	}
	if msg.ImageMessage != nil {
		if msg.ImageMessage.Caption != "" {
			return msg.ImageMessage.Caption
		}
		return "[Imagem]"
	}
	if msg.AudioMessage != nil {
		return "[Áudio]"
	}
	if msg.VideoMessage != nil {
		if msg.VideoMessage.Caption != "" {
			return msg.VideoMessage.Caption
		}
		return "[Vídeo]"
	}
	if msg.DocumentMessage != nil {
		if msg.DocumentMessage.FileName != "" {
			return fmt.Sprintf("[Documento: %s]", msg.DocumentMessage.FileName)
		}
		return "[Documento]"
	}
	if msg.ContactMessage != nil {
		return fmt.Sprintf("[Contato: %s]", msg.ContactMessage.DisplayName)
	}
	if msg.ReactionMessage != nil {
		return fmt.Sprintf("[Reação: %s]", msg.ReactionMessage.Text)
	}
	return ""
}

func guessFileName(messageType string, msg *WookMessageRaw) string {
	if msg.DocumentMessage != nil && msg.DocumentMessage.FileName != "" {
		return msg.DocumentMessage.FileName
	}

	ext := map[string]string{
		"imageMessage":    ".jpg",
		"videoMessage":    ".mp4",
		"audioMessage":    ".ogg",
		"documentMessage": ".bin",
	}

	e, ok := ext[messageType]
	if !ok {
		e = filepath.Ext(msg.MediaURL)
		if e == "" {
			e = ".bin"
		}
	}

	return fmt.Sprintf("arquivo%s", e)
}

func guessMimeType(messageType string) string {
	types := map[string]string{
		"imageMessage":    "image/jpeg",
		"videoMessage":    "video/mp4",
		"audioMessage":    "audio/ogg",
		"documentMessage": "application/octet-stream",
	}
	if t, ok := types[messageType]; ok {
		return t
	}
	return "application/octet-stream"
}
