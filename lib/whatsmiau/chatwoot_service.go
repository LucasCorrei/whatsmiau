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
	"strings"
	"time"
	"regexp"
	_ "github.com/lib/pq"
	"github.com/verbeux-ai/whatsmiau/env"
	"go.uber.org/zap"
)
type Contact struct {
	ID int `json:"id"`
	Name string `json:"name"`
	PhoneNumber string `json:"phone_number"`
	Identifier string `json:"identifier"`
}

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

	contactCache map[string]int
}

func NewChatwootService(config ChatwootConfig) *ChatwootService {
	service := &ChatwootService{
		config: config,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		contactCache: make(map[string]int),
	}

	// Se o Chatwoot não está habilitado, retorna sem inicializar nada
	if !c.config.Enabled {
		zap.L().Info("chatwoot: serviço desabilitado via CHATWOOT_ENABLED=false")
		return service
	}

	zap.L().Info("chatwoot: serviço HABILITADO")

	// Conecta ao PostgreSQL se a URI estiver configurada
	if env.Env.ChatwootImportDatabaseConnectionURI != "" {
		zap.L().Info("chatwoot: tentando conectar ao PostgreSQL",
			zap.String("uri", maskPassword(env.Env.ChatwootImportDatabaseConnectionURI)))

		db, err := sql.Open("postgres", env.Env.ChatwootImportDatabaseConnectionURI)
		if err != nil {
			zap.L().Error("chatwoot: erro ao conectar no PostgreSQL", zap.Error(err))
		} else {
			// Configura pool de conexões
			db.SetMaxOpenConns(10)
			db.SetMaxIdleConns(5)
			db.SetConnMaxLifetime(time.Hour)

			// Testa a conexão
			if err := db.Ping(); err != nil {
				zap.L().Error("chatwoot: erro ao pingar PostgreSQL", zap.Error(err))
				db.Close()
			} else {
				service.db = db
				zap.L().Info("chatwoot: ✅ CONECTADO ao PostgreSQL para verificação de duplicatas")
			}
		}
	} else {
		zap.L().Warn("chatwoot: ⚠️ CHATWOOT_IMPORT_DATABASE_CONNECTION_URI não configurado, verificação de duplicatas DESABILITADA")
	}

	return service
}

// maskPassword mascara a senha na URI do PostgreSQL para logs
func maskPassword(uri string) string {
	// postgres://user:password@host:port/db -> postgres://user:***@host:port/db
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

// Close fecha a conexão com o banco de dados
func (c *ChatwootService) Close() error {
	if c.db != nil {
		zap.L().Info("chatwoot: fechando conexão com PostgreSQL")
		return c.db.Close()
	}
	return nil
}

// IsEnabled verifica se o Chatwoot está habilitado
func (c *ChatwootService) IsEnabled() bool {
	return env.Env.ChatwootEnabled
}

// ── Tipos de resposta da API ──────────────────────────────────────────────────

type chatwootContact struct {
	ID int `json:"id"`
}

type chatwootContactFilterResponse struct {
	Payload []chatwootContact `json:"payload"`
}

// CORRIGIDO: A API retorna o contato diretamente em payload, não em payload.contact
type chatwootContactCreateResponse struct {
	Payload chatwootContact `json:"payload"`
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

// ── Handler principal ─────────────────────────────────────────────────────────

func (c *ChatwootService) HandleMessage(messageData *WookMessageData) {
	// Verifica se está habilitado
	if !c.config.Enabled {
		return
	}

	if messageData == nil || messageData.Key == nil {
		return
	}

	// CORRIGIDO: Aceita tanto mensagens de entrada quanto de saída
	// (para sincronizar mensagens enviadas diretamente do WhatsApp)
	isFromMe := messageData.Key.FromMe

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	remoteJid := messageData.Key.RemoteJid

	phone := normalizePhone(extractPhone(remoteJid))
	if phone == "" {
		zap.L().Warn("chatwoot: número inválido", zap.String("remoteJid", remoteJid))
		return
	}

	pushName := messageData.PushName
	if pushName == "" {
		pushName = phone
	}

	messageID := messageData.Key.Id

	messageType := "incoming"
	if isFromMe {
		messageType = "outgoing"
	}

	zap.L().Info("chatwoot: 📞 processando mensagem",
		zap.String("phone", phone),
		zap.String("messageId", messageID),
		zap.String("pushName", pushName),
		zap.String("type", messageType))

	contactID, err := c.findOrCreateContact(ctx, phone, pushName, remoteJid)
	if err != nil {
		zap.L().Error("chatwoot: erro ao buscar/criar contato", zap.Error(err))
		return
	}

	// CORRIGIDO: Verifica se o contactID é válido
	if contactID <= 0 {
		zap.L().Error("chatwoot: contactID inválido retornado",
			zap.Int("contactId", contactID),
			zap.String("phone", phone))
		return
	}

	zap.L().Info("chatwoot: ✅ contato obtido", zap.Int("contactId", contactID))

	conversationID, err := c.findOrCreateConversation(ctx, contactID)
	if err != nil {
		zap.L().Error("chatwoot: erro ao buscar/criar conversa", zap.Error(err))
		return
	}

	// CORRIGIDO: Verifica se o conversationID é válido
	if conversationID <= 0 {
		zap.L().Error("chatwoot: conversationID inválido retornado",
			zap.Int("conversationId", conversationID),
			zap.Int("contactId", contactID))
		return
	}

	zap.L().Info("chatwoot: ✅ conversa obtida", zap.Int("conversationId", conversationID))

	msg := messageData.Message
	if msg == nil {
		return
	}

	// Mídia: usa Base64
	if msg.Base64 != "" {
		filename, caption, mimetype := extractMediaMeta(messageData)

		mediaBytes, err := base64.StdEncoding.DecodeString(msg.Base64)
		if err != nil {
			zap.L().Error("chatwoot: erro ao decodificar base64", zap.Error(err))
			return
		}

		if err := c.sendMediaMessage(ctx, conversationID, mediaBytes, filename, mimetype, caption, messageID, isFromMe); err != nil {
			zap.L().Error("chatwoot: erro ao enviar mídia", zap.Error(err))
			return
		}

		zap.L().Info("chatwoot: mídia enviada",
			zap.String("phone", phone),
			zap.String("type", messageData.MessageType),
			zap.String("filename", filename),
			zap.String("mimetype", mimetype),
			zap.Int("conversationId", conversationID),
			zap.String("messageType", messageType),
		)
		return
	}

	// Texto
	messageText := extractMessageText(messageData)
	if messageText == "" {
		zap.L().Warn("chatwoot: mensagem sem conteúdo, ignorando",
			zap.String("type", messageData.MessageType))
		return
	}

	if err := c.sendMessage(ctx, conversationID, messageText, messageID, isFromMe); err != nil {
		zap.L().Error("chatwoot: erro ao enviar mensagem", zap.Error(err))
		return
	}

	zap.L().Info("chatwoot: mensagem de texto enviada",
		zap.String("phone", phone),
		zap.Int("conversationId", conversationID),
		zap.String("messageType", messageType),
	)
}

// ── Verificação de duplicatas ─────────────────────────────────────────────────

func (c *ChatwootService) checkMessageExists(ctx context.Context, conversationID int, sourceID string) (bool, error) {
	zap.L().Info("chatwoot: 🔍 iniciando verificação de duplicata",
		zap.Int("conversationId", conversationID),
		zap.String("sourceId", sourceID),
		zap.Bool("dbConnected", c.db != nil))

	if c.db == nil {
		zap.L().Warn("chatwoot: ⚠️ verificação de duplicata PULADA - banco de dados NÃO CONECTADO")
		return false, nil
	}

	// Proteção adicional: se conversationID for 0 ou inválido, não verifica
	if conversationID <= 0 {
		zap.L().Warn("chatwoot: ⚠️ conversationID inválido, pulando verificação",
			zap.Int("conversationId", conversationID))
		return false, nil
	}

	query := `
		SELECT COUNT(*) 
		FROM messages 
		WHERE conversation_id = $1 
		AND source_id = $2
		LIMIT 1
	`

	zap.L().Info("chatwoot: 📊 executando query de verificação",
		zap.Int("conversationId", conversationID),
		zap.String("sourceId", sourceID))

	var count int
	err := c.db.QueryRowContext(ctx, query, conversationID, sourceID).Scan(&count)
	if err != nil {
		zap.L().Error("chatwoot: ❌ erro ao executar query de verificação",
			zap.Error(err),
			zap.Int("conversationId", conversationID),
			zap.String("sourceId", sourceID))
		return false, fmt.Errorf("erro ao verificar mensagem existente: %w", err)
	}

	exists := count > 0
	if exists {
		zap.L().Info("chatwoot: ✅ DUPLICATA DETECTADA - mensagem JÁ EXISTE",
			zap.Int("count", count),
			zap.String("sourceId", sourceID),
			zap.Int("conversationId", conversationID))
	} else {
		zap.L().Info("chatwoot: ✅ mensagem NÃO EXISTE no banco, pode enviar",
			zap.String("sourceId", sourceID),
			zap.Int("conversationId", conversationID))
	}

	return exists, nil
}

// ── Contatos ──────────────────────────────────────────────────────────────────
func (c *ChatwootService) findOrCreateContact(ctx context.Context, phone, name, identifier string) (int, error) {

	if id, ok := c.contactCache[phone]; ok {
		zap.L().Info("chatwoot: contato vindo do cache",
			zap.Int("contactId", id))
		return id, nil
	}

	id, err := c.searchContact(ctx, phone)
	if err != nil {
		return 0, err
	}

	if id > 0 {
		c.contactCache[phone] = id
		return id, nil
	}

	id, err = c.createContact(ctx, phone, name, identifier)
	if err != nil {
		return 0, err
	}

	c.contactCache[phone] = id

	return id, nil
}

func (c *ChatwootService) searchContact(ctx context.Context, phone string) (int, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts/filter", c.config.URL, c.config.AccountID)
	body := map[string]interface{}{
		"payload": []map[string]interface{}{
			{
				"attribute_key":   "phone_number",
				"filter_operator": "equal_to",
				"values": []string{fmt.Sprintf("+%s", phone), phone,},
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

	// CORRIGIDO: Log da resposta bruta para debug
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("erro ao ler resposta: %w", err)
	}

	zap.L().Info("chatwoot: resposta bruta da criação de contato",
		zap.String("response", string(bodyBytes)))

	var result chatwootContactCreateResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		zap.L().Error("chatwoot: erro ao decodificar resposta de criar contato",
			zap.Error(err),
			zap.String("response", string(bodyBytes)))
		return 0, fmt.Errorf("erro ao decodificar resposta: %w", err)
	}

	contactID := result.Payload.ID
	
	// CORRIGIDO: Verifica se o ID é válido
	if contactID <= 0 {
		zap.L().Error("chatwoot: ID de contato inválido retornado pela API",
			zap.Int("contactId", contactID),
			zap.String("response", string(bodyBytes)))
		return 0, fmt.Errorf("API retornou contactId inválido: %d", contactID)
	}

	zap.L().Info("chatwoot: ✅ contato CRIADO", zap.Int("contactId", contactID))

	return contactID, nil
}

// ── Conversas ─────────────────────────────────────────────────────────────────

func (c *ChatwootService) findOrCreateConversation(ctx context.Context, contactID int) (int, error) {
	if contactID <= 0 {
		return 0, fmt.Errorf("contactID inválido: %d", contactID)
	}

	zap.L().Info("chatwoot: 🔍 buscando conversa existente",
		zap.Int("contactId", contactID))

	id, err := c.findOrReopenConversation(ctx, contactID)
	if err != nil {
		return 0, err
	}

	if id > 0 {
		return id, nil
	}

	zap.L().Info("chatwoot: 📝 criando nova conversa",
		zap.Int("contactId", contactID))

	return c.createConversation(ctx, contactID)
}

func (c *ChatwootService) findOrReopenConversation(ctx context.Context, contactID int) (int, error) {
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

	if len(result.Payload) == 0 {
		return 0, nil
	}

	// Vamos pegar a conversa mais recente da inbox
	for _, conv := range result.Payload {
		if conv.InboxID != c.config.InboxID {
			continue
		}

		zap.L().Info("chatwoot: 📋 conversa encontrada",
			zap.Int("conversationId", conv.ID),
			zap.String("status", conv.Status))

		// Se estiver resolvida → reabre
		if conv.Status == "resolved" {
			zap.L().Info("chatwoot: ♻️ reabrindo conversa resolvida",
				zap.Int("conversationId", conv.ID))

			if err := c.reopenConversation(ctx, conv.ID); err != nil {
				zap.L().Warn("chatwoot: erro ao reabrir conversa",
					zap.Error(err))
			}
		}

		return conv.ID, nil
	}

	return 0, nil
}

func (c *ChatwootService) reopenConversation(ctx context.Context, conversationID int) error {
	url := fmt.Sprintf(
		"%s/api/v1/accounts/%s/conversations/%d/toggle_status",
		c.config.URL,
		c.config.AccountID,
		conversationID,
	)

	body := map[string]interface{}{
		"status": "open",
	}

	resp, err := c.doRequest(ctx, "POST", url, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("erro ao reabrir conversa: %d - %s",
			resp.StatusCode,
			string(raw))
	}

	zap.L().Info("chatwoot: ✅ conversa reaberta",
		zap.Int("conversationId", conversationID))

	return nil
}

func (c *ChatwootService) createConversation(ctx context.Context, contactID int) (int, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations",
		c.config.URL,
		c.config.AccountID)

	body := map[string]interface{}{
		"contact_id": contactID,
		"inbox_id":   c.config.InboxID,
	}

	resp, err := c.doRequest(ctx, "POST", url, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("erro ao criar conversa: %d - %s",
			resp.StatusCode,
			string(raw))
	}

	var result chatwootConversationCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	zap.L().Info("chatwoot: ✅ nova conversa criada",
		zap.Int("conversationId", result.ID))

	return result.ID, nil
}

// ── Envio de mensagens ────────────────────────────────────────────────────────

// CORRIGIDO: Adiciona parâmetro isFromMe para determinar o tipo de mensagem
func (c *ChatwootService) sendMessage(ctx context.Context, conversationID int, content, messageID string, isFromMe bool) error {
	sourceID := fmt.Sprintf("WAID:%s", messageID)

	// CORRIGIDO: Define o tipo correto baseado na origem
	messageType := "incoming"
	if isFromMe {
		messageType = "outgoing"
	}

	zap.L().Info("chatwoot: 📨 preparando para enviar mensagem de TEXTO",
		zap.String("messageId", messageID),
		zap.String("sourceId", sourceID),
		zap.Int("conversationId", conversationID),
		zap.Int("contentLength", len(content)),
		zap.String("messageType", messageType))

	// Verifica se a mensagem já existe
	exists, err := c.checkMessageExists(ctx, conversationID, sourceID)
	if err != nil {
		zap.L().Warn("chatwoot: erro ao verificar duplicata, continuando envio", zap.Error(err))
	} else if exists {
		zap.L().Info("chatwoot: 🚫 mensagem já existe, IGNORANDO envio",
			zap.String("sourceId", sourceID),
			zap.Int("conversationId", conversationID))
		return nil
	}

	zap.L().Info("chatwoot: ➡️ enviando mensagem de texto para API",
		zap.String("sourceId", sourceID),
		zap.Int("conversationId", conversationID),
		zap.String("messageType", messageType))

	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages",
		c.config.URL, c.config.AccountID, conversationID)
	body := map[string]interface{}{
		"content":      content,
		"message_type": messageType,
		"private":      false,
		"source_id":    sourceID,
	}

	resp, err := c.doRequest(ctx, "POST", url, body)
	if err != nil {
		zap.L().Error("chatwoot: ❌ erro na requisição HTTP", zap.Error(err))
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		zap.L().Error("chatwoot: ❌ erro do servidor Chatwoot",
			zap.Int("statusCode", resp.StatusCode),
			zap.String("response", string(raw)))
		return fmt.Errorf("chatwoot erro %d: %s", resp.StatusCode, string(raw))
	}

	zap.L().Info("chatwoot: ✅ mensagem de texto enviada com SUCESSO",
		zap.String("sourceId", sourceID),
		zap.Int("statusCode", resp.StatusCode),
		zap.String("messageType", messageType))

	return nil
}

// CORRIGIDO: Adiciona parâmetro isFromMe
func (c *ChatwootService) sendMediaMessage(
	ctx context.Context,
	conversationID int,
	mediaBytes []byte,
	filename, mimetype, caption, messageID string,
	isFromMe bool,
) error {
	sourceID := fmt.Sprintf("WAID:%s", messageID)

	// CORRIGIDO: Define o tipo correto baseado na origem
	messageType := "incoming"
	if isFromMe {
		messageType = "outgoing"
	}

	zap.L().Info("chatwoot: 📨 preparando para enviar MÍDIA",
		zap.String("messageId", messageID),
		zap.String("sourceId", sourceID),
		zap.Int("conversationId", conversationID),
		zap.String("filename", filename),
		zap.String("mimetype", mimetype),
		zap.Int("mediaSize", len(mediaBytes)),
		zap.String("messageType", messageType))

	// Verifica se a mensagem já existe
	exists, err := c.checkMessageExists(ctx, conversationID, sourceID)
	if err != nil {
		zap.L().Warn("chatwoot: erro ao verificar duplicata, continuando envio", zap.Error(err))
	} else if exists {
		zap.L().Info("chatwoot: 🚫 mídia já existe, IGNORANDO envio",
			zap.String("sourceId", sourceID),
			zap.Int("conversationId", conversationID))
		return nil
	}

	zap.L().Info("chatwoot: ➡️ enviando mídia para API",
		zap.String("sourceId", sourceID),
		zap.Int("conversationId", conversationID),
		zap.String("messageType", messageType))

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	_ = writer.WriteField("message_type", messageType)
	_ = writer.WriteField("private", "false")
	_ = writer.WriteField("source_id", sourceID)

	if caption != "" {
		_ = writer.WriteField("content", caption)
	}

	// Content-Type explícito para o Chatwoot renderizar corretamente
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
		zap.L().Error("chatwoot: ❌ erro na requisição HTTP de mídia", zap.Error(err))
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		zap.L().Error("chatwoot: ❌ erro do servidor Chatwoot ao enviar mídia",
			zap.Int("statusCode", resp.StatusCode),
			zap.String("response", string(raw)))
		return fmt.Errorf("chatwoot erro %d: %s", resp.StatusCode, string(raw))
	}

	zap.L().Info("chatwoot: ✅ mídia enviada com SUCESSO",
		zap.String("sourceId", sourceID),
		zap.Int("statusCode", resp.StatusCode),
		zap.String("messageType", messageType))

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

// extractMediaMeta retorna filename, caption e mimetype limpos baseado no tipo da mensagem
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
		// PTT do WhatsApp vem como "audio/ogg; codecs=opus" — limpa para o Chatwoot
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
		caption = ""

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
			ext := mimetypeToExt(mimetype, "bin")
			filename = fmt.Sprintf("%s.%s", id, ext)
		}
		caption = msg.DocumentMessage.Caption
	case msg.StickerMessage != nil:
		mimetype = cleanMimetype(msg.StickerMessage.Mimetype, "image/webp")

	// Sticker SEMPRE é webp no WhatsApp
		if mimetype == "" || mimetype == "application/octet-stream" {
			mimetype = "image/webp"
		}

		filename = fmt.Sprintf("%s.webp", id)
		caption = ""
	default:
		filename = fmt.Sprintf("%s.bin", id)
		mimetype = "application/octet-stream"
	}

		return filename, caption, mimetype
}

// cleanMimetype remove parâmetros extras como "; codecs=opus" e retorna fallback se vazio
func cleanMimetype(raw, fallback string) string {
	if raw == "" {
		return fallback
	}
	// Remove parâmetros: "image/jpeg; something" → "image/jpeg"
	parts := strings.SplitN(raw, ";", 2)
	clean := strings.TrimSpace(parts[0])
	if clean == "" {
		return fallback
	}
	return clean
}

// mimetypeToExt retorna extensão simples baseada no mimetype
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
		nome := msg.ContactMessage.DisplayName
		vcard := msg.ContactMessage.VCard

		// Regex para pegar o waid (telefone puro)
		re := regexp.MustCompile(`waid=(\d+)`)
		match := re.FindStringSubmatch(vcard)

		telefone := ""
		if len(match) > 1 {
			telefone = match[1]
		}

		return fmt.Sprintf("Contato:\nNome: %s\nTelefone: %s", nome, telefone)
	}
	if msg.LocationMessage != nil {
	nome := msg.LocationMessage.Name
	endereco := msg.LocationMessage.Address
	lat := msg.LocationMessage.DegreesLatitude
	lng := msg.LocationMessage.DegreesLongitude

	link := fmt.Sprintf("https://www.google.com/maps?q=%f,%f", lat, lng)

	return fmt.Sprintf(
		"📍 *Localização*\n\n*Nome:* %s\n*Endereço:* %s\n*Latitude:* %.6f\n*Longitude:* %.6f\n\n🌎 *Mapa:* %s",
		nome,
		endereco,
		lat,
		lng,
		link,
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

func (c *ChatwootService) FindContactByPhone(phone string) (*Contact, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts/search?q=%s",
		c.config.BaseURL,
		c.config.AccountID,
		phone,
	)

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("api_access_token", c.config.Token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	var result ContactSearchResponse
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Payload) > 0 {
		return &result.Payload[0], nil
	}

	return nil, nil
}

func (c *ChatwootService) CreateContact(name, phone string) (*Contact, error) {

	body := map[string]interface{}{
		"name":         name,
		"phone_number": phone,
		"inbox_id":     c.InboxID,
	}

	jsonBody, _ := json.Marshal(body)

	url := fmt.Sprintf("%s/api/v1/accounts/%s/contacts", c.config.BaseURL, c.config.AccountID)

	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))

	req.Header.Set("api_access_token", c.config.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	var result ContactResponse
	json.NewDecoder(resp.Body).Decode(&result)

	return &result.Payload, nil
}
func (c *ChatwootService) GetOrCreateContact(name, phone string) (*Contact, error) {

	contact, err := c.FindContactByPhone(phone)
	if err != nil {
		return nil, err
	}

	if contact != nil {
		return contact, nil
	}

	return c.CreateContact(name, phone)
}

func normalizePhone(phone string) string {

	re := regexp.MustCompile(`\D`)
	phone = re.ReplaceAllString(phone, "")

	if !strings.HasPrefix(phone, "55") {
		phone = "55" + phone
	}

	// corrige celular sem 9
	if len(phone) == 12 {
		ddd := phone[2:4]
		num := phone[4:]
		phone = "55" + ddd + "9" + num
	}

	return phone
}
