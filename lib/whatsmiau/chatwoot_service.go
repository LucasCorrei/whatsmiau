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
	"go.uber.org/zap"
	"github.com/verbeux-ai/whatsmiau/env"
	_ "github.com/lib/pq"
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
}

func NewChatwootService(config ChatwootConfig) *ChatwootService {
	service := &ChatwootService{
		config:     config,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}

	// Se o Chatwoot não está habilitado, retorna sem inicializar nada
	if !env.Env.ChatwootEnabled {
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

// ── Handler principal ─────────────────────────────────────────────────────────

func (c *ChatwootService) HandleMessage(messageData *WookMessageData) {
	// Verifica se está habilitado
	if !env.Env.ChatwootEnabled {
		return
	}

	if messageData == nil || (messageData.Key != nil && messageData.Key.FromMe) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	contactID, err := c.findOrCreateContact(ctx, phone, pushName, remoteJid)
	if err != nil {
		zap.L().Error("chatwoot: erro ao buscar/criar contato", zap.Error(err))
		return
	}

	conversationID, err := c.findOrCreateConversation(ctx, contactID)
	if err != nil {
		zap.L().Error("chatwoot: erro ao buscar/criar conversa", zap.Error(err))
		return
	}

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

		if err := c.sendMediaMessage(ctx, conversationID, mediaBytes, filename, mimetype, caption, messageID); err != nil {
			zap.L().Error("chatwoot: erro ao enviar mídia", zap.Error(err))
			return
		}

		zap.L().Info("chatwoot: mídia enviada",
			zap.String("phone", phone),
			zap.String("type", messageData.MessageType),
			zap.String("filename", filename),
			zap.String("mimetype", mimetype),
			zap.Int("conversationId", conversationID),
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

	if err := c.sendMessage(ctx, conversationID, messageText, messageID); err != nil {
		zap.L().Error("chatwoot: erro ao enviar mensagem", zap.Error(err))
		return
	}

	zap.L().Info("chatwoot: mensagem de texto enviada",
		zap.String("phone", phone),
		zap.Int("conversationId", conversationID),
	)
}

// ── Verificação de duplicatas ─────────────────────────────────────────────────

// checkMessageExists verifica se a mensagem já existe no Chatwoot
// retorna true se a mensagem já foi salva
func (c *ChatwootService) checkMessageExists(ctx context.Context, conversationID int, sourceID string) (bool, error) {
	zap.L().Debug("chatwoot: verificando duplicata",
		zap.Int("conversationId", conversationID),
		zap.String("sourceId", sourceID),
		zap.Bool("dbConnected", c.db != nil))

	if c.db == nil {
		zap.L().Warn("chatwoot: ⚠️ verificação de duplicata PULADA - banco de dados NÃO CONECTADO")
		return false, nil
	}

	query := `
		SELECT COUNT(*) 
		FROM messages 
		WHERE conversation_id = $1 
		AND source_id = $2
		LIMIT 1
	`

	zap.L().Debug("chatwoot: executando query de verificação",
		zap.String("query", query),
		zap.Int("conversationId", conversationID),
		zap.String("sourceId", sourceID))

	var count int
	err := c.db.QueryRowContext(ctx, query, conversationID, sourceID).Scan(&count)
	if err != nil {
		zap.L().Error("chatwoot: ❌ erro ao verificar mensagem existente",
			zap.Error(err),
			zap.Int("conversationId", conversationID),
			zap.String("sourceId", sourceID))
		return false, fmt.Errorf("erro ao verificar mensagem existente: %w", err)
	}

	exists := count > 0
	if exists {
		zap.L().Info("chatwoot: ✅ mensagem JÁ EXISTE no banco",
			zap.Int("count", count),
			zap.String("sourceId", sourceID),
			zap.Int("conversationId", conversationID))
	} else {
		zap.L().Debug("chatwoot: ✅ mensagem NÃO EXISTE no banco, pode enviar",
			zap.String("sourceId", sourceID),
			zap.Int("conversationId", conversationID))
	}

	return exists, nil
}

// ── Contatos ──────────────────────────────────────────────────────────────────

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

// ── Conversas ─────────────────────────────────────────────────────────────────

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

// ── Envio de mensagens ────────────────────────────────────────────────────────

func (c *ChatwootService) sendMessage(ctx context.Context, conversationID int, content, messageID string) error {
	sourceID := fmt.Sprintf("WAID:%s", messageID)

	zap.L().Info("chatwoot: 📨 preparando para enviar mensagem de TEXTO",
		zap.String("messageId", messageID),
		zap.String("sourceId", sourceID),
		zap.Int("conversationId", conversationID),
		zap.Int("contentLength", len(content)))

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
		zap.Int("conversationId", conversationID))

	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages",
		c.config.URL, c.config.AccountID, conversationID)
	body := map[string]interface{}{
		"content":      content,
		"message_type": "incoming",
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
		zap.Int("statusCode", resp.StatusCode))

	return nil
}

func (c *ChatwootService) sendMediaMessage(
	ctx context.Context,
	conversationID int,
	mediaBytes []byte,
	filename, mimetype, caption, messageID string,
) error {
	sourceID := fmt.Sprintf("WAID:%s", messageID)

	zap.L().Info("chatwoot: 📨 preparando para enviar MÍDIA",
		zap.String("messageId", messageID),
		zap.String("sourceId", sourceID),
		zap.Int("conversationId", conversationID),
		zap.String("filename", filename),
		zap.String("mimetype", mimetype),
		zap.Int("mediaSize", len(mediaBytes)))

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
		zap.Int("conversationId", conversationID))

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	_ = writer.WriteField("message_type", "incoming")
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
		zap.Int("statusCode", resp.StatusCode))

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
		return fmt.Sprintf("[Contato: %s]", msg.ContactMessage.DisplayName)
	}
	if msg.ReactionMessage != nil {
		return fmt.Sprintf("[Reação: %s]", msg.ReactionMessage.Text)
	}

	return ""
}
