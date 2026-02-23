package whatsmiau

// =============================================================================
// chatwoot_service.go
// Integração nativa WhatsMiau → Chatwoot
//
// Coloque este arquivo em: lib/whatsmiau/chatwoot_service.go
//
// Como usar:
// 1. Adicione as variáveis de ambiente no .env (veja abaixo)
// 2. Instancie o ChatwootService no seu main.go ou onde inicializar o Whatsmiau
// 3. Chame chatwootService.HandleMessage(id, instance, e) dentro do
//    handleMessageEvent em event_emitter.go
// =============================================================================

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// =============================================================================
// Variáveis de ambiente necessárias no .env:
//
// CHATWOOT_URL=https://seu-chatwoot.com
// CHATWOOT_ACCOUNT_ID=1
// CHATWOOT_TOKEN=seu_token_aqui
// CHATWOOT_INBOX_ID=1
// =============================================================================

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
		config: config,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
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
// Chame isso dentro do handleMessageEvent em event_emitter.go:
//
//   if s.chatwootService != nil {
//       s.chatwootService.HandleMessage(id, instance, e)
//   }
// =============================================================================

func (c *ChatwootService) HandleMessage(messageData *WookMessageData) {
	if messageData == nil {
		return
	}

	// Ignorar mensagens enviadas por mim
	if messageData.Key != nil && messageData.Key.FromMe {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Extrair número limpo
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

	// Texto da mensagem
	messageText := extractMessageText(messageData)
	if messageText == "" {
		messageText = "[Mensagem não suportada]"
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

	// 3. Enviar mensagem
	if err := c.sendMessage(ctx, conversationID, messageText, messageID); err != nil {
		zap.L().Error("chatwoot: erro ao enviar mensagem", zap.Error(err))
		return
	}

	zap.L().Info("chatwoot: mensagem enviada com sucesso",
		zap.String("phone", phone),
		zap.Int("conversationId", conversationID),
	)
}

// =============================================================================
// findOrCreateContact
// =============================================================================

func (c *ChatwootService) findOrCreateContact(ctx context.Context, phone, name, identifier string) (int, error) {
	// Buscar pelo telefone
	contactID, err := c.searchContact(ctx, phone)
	if err != nil {
		return 0, err
	}
	if contactID > 0 {
		return contactID, nil
	}

	// Criar novo contato
	return c.createContact(ctx, phone, name, identifier)
}

func (c *ChatwootService) searchContact(ctx context.Context, phone string) (int, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts/filter",
		c.config.URL, c.config.AccountID)

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
	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts",
		c.config.URL, c.config.AccountID)

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
	// Buscar conversa aberta
	convID, err := c.findOpenConversation(ctx, contactID)
	if err != nil {
		return 0, err
	}
	if convID > 0 {
		return convID, nil
	}

	// Criar nova conversa
	return c.createConversation(ctx, contactID)
}

func (c *ChatwootService) findOpenConversation(ctx context.Context, contactID int) (int, error) {
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
		if conv.InboxID == c.config.InboxID && conv.Status != "resolved" {
			return conv.ID, nil
		}
	}

	return 0, nil
}

func (c *ChatwootService) createConversation(ctx context.Context, contactID int) (int, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations",
		c.config.URL, c.config.AccountID)

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
// sendMessage
// =============================================================================

func (c *ChatwootService) sendMessage(ctx context.Context, conversationID int, content, messageID string) error {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages",
		c.config.URL, c.config.AccountID, conversationID)

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
		return fmt.Errorf("chatwoot API erro %d: %s", resp.StatusCode, string(raw))
	}

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
// Ex: "5511999999999@s.whatsapp.net" → "5511999999999"
func extractPhone(remoteJid string) string {
	parts := strings.Split(remoteJid, "@")
	if len(parts) == 0 {
		return ""
	}
	// Remove o sufixo :XX (dispositivo)
	phone := strings.Split(parts[0], ":")[0]
	return phone
}

// extractMessageText extrai o texto de qualquer tipo de mensagem
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
		if msg.DocumentMessage.Caption != "" {
			return msg.DocumentMessage.Caption
		}
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
