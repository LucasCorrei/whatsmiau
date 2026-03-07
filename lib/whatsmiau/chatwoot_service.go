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

var conversationLocks sync.Map

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

	// label aplicada aos contatos (nome da inbox / instanceID)
	labelName string

	// cache de inboxID por nome de instância
	inboxCache   map[string]int
	inboxCacheMu sync.RWMutex
}

func getConversationLock(key string) *sync.Mutex {
    lock, _ := conversationLocks.LoadOrStore(key, &sync.Mutex{})
    return lock.(*sync.Mutex)
}
func NewChatwootService(config ChatwootConfig) *ChatwootService {
	service := &ChatwootService{
		config:     config,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		inboxCache: make(map[string]int),
	}

	// Se o Chatwoot não está habilitado, retorna sem inicializar nada
	if config.URL == "" || config.Token == "" || config.AccountID == "" {
		zap.L().Info("chatwoot: desabilitado para esta instância (config incompleta)")
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
			db.SetMaxOpenConns(10)
			db.SetMaxIdleConns(5)
			db.SetConnMaxLifetime(time.Hour)

			if err := db.Ping(); err != nil {
				zap.L().Error("chatwoot: erro ao pingar PostgreSQL", zap.Error(err))
				db.Close()
			} else {
				service.db = db
				zap.L().Info("chatwoot: ✅ CONECTADO ao PostgreSQL para verificação de duplicatas e labels")
			}
		}
	} else {
		zap.L().Warn("chatwoot: ⚠️ CHATWOOT_IMPORT_DATABASE_CONNECTION_URI não configurado, verificação de duplicatas e labels DESABILITADA")
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
		zap.L().Info("chatwoot: fechando conexão com PostgreSQL")
		return c.db.Close()
	}
	return nil
}

func (c *ChatwootService) IsEnabled() bool {
	return c.config.URL != "" &&
		c.config.Token != "" &&
		c.config.AccountID != ""
}

// getInboxIDForInstance retorna o inboxID para uma instância usando cache em memória.
// Se não estiver em cache, busca na API pelo nome da instância (que é o instanceID).
func (c *ChatwootService) getInboxIDForInstance(ctx context.Context, instanceID string) (int, error) {
	c.inboxCacheMu.RLock()
	if id, ok := c.inboxCache[instanceID]; ok {
		c.inboxCacheMu.RUnlock()
		return id, nil
	}
	c.inboxCacheMu.RUnlock()

	zap.L().Info("chatwoot: 🔍 buscando inboxID para instância na API", zap.String("instanceID", instanceID))

	id, err := c.findInboxByName(ctx, instanceID)
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, fmt.Errorf("inbox não encontrada para instância '%s' — verifique se a instância foi criada corretamente", instanceID)
	}

	c.inboxCacheMu.Lock()
	c.inboxCache[instanceID] = id
	c.inboxCacheMu.Unlock()

	zap.L().Info("chatwoot: ✅ inboxID resolvido e cacheado",
		zap.String("instanceID", instanceID),
		zap.Int("inboxId", id))

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
		return 0, fmt.Errorf("erro ao decodificar lista de inboxes: %w", err)
	}

	for _, inbox := range listResp.Payload {
		if inbox.Name == name {
			return inbox.ID, nil
		}
	}

	return 0, nil
}

// ── Tipos de resposta da API ──────────────────────────────────────────────────

type chatwootContact struct {
	ID          int    `json:"id"`
	PhoneNumber string `json:"phone_number"`
}

type chatwootContactFilterResponse struct {
	Payload []chatwootContact `json:"payload"`
}

// A API do Chatwoot retorna duas estruturas diferentes dependendo do endpoint:
// POST /contacts → {"payload": {"contact": {"id": 123, ...}, "contact_inbox": {...}}}
// Mas às vezes retorna → {"payload": {"id": 123, ...}} (versões mais antigas)
// Suportamos os dois formatos.
type chatwootContactCreatePayload struct {
	// Formato novo: payload.contact.id
	Contact *chatwootContact `json:"contact"`
	// Formato antigo: payload.id (embutido diretamente)
	ID          int    `json:"id"`
	PhoneNumber string `json:"phone_number"`
}

type chatwootContactCreateResponse struct {
	Payload chatwootContactCreatePayload `json:"payload"`
}

// contactIDFromCreateResponse extrai o ID do contato independente do formato da resposta.
func contactIDFromCreateResponse(r chatwootContactCreateResponse) int {
	// Formato novo: payload.contact.id
	if r.Payload.Contact != nil && r.Payload.Contact.ID > 0 {
		return r.Payload.Contact.ID
	}
	// Formato antigo: payload.id
	return r.Payload.ID
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

type chatwootInboxCreateResponse struct {
	ID int `json:"id"`
}

// ── Init Instance ─────────────────────────────────────────────────────────────

func (c *ChatwootService) InitInstance(ctx context.Context, inboxName, webhookURL, organization, logo string) (int, error) {
	if !c.IsEnabled() {
		return 0, fmt.Errorf("chatwoot não está habilitado")
	}

	zap.L().Info("chatwoot: 🚀 inicializando instância",
		zap.String("inboxName", inboxName),
		zap.String("webhookURL", webhookURL))

	inboxID, err := c.findOrCreateInbox(ctx, inboxName, webhookURL)
	if err != nil {
		return 0, fmt.Errorf("erro ao buscar/criar inbox: %w", err)
	}

	zap.L().Info("chatwoot: ✅ inbox pronta", zap.Int("inboxId", inboxID))

	// Popula o cache imediatamente para o HandleMessage não precisar buscar na API
	c.inboxCacheMu.Lock()
	c.inboxCache[inboxName] = inboxID
	c.inboxCacheMu.Unlock()

	// Guarda o nome da inbox para usar como label nos contatos
	c.labelName = inboxName

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
	} else {
		zap.L().Info("chatwoot: ✅ contato bot pronto", zap.Int("contactId", botContactID))

		if botContactID > 0 {
			if err := c.createBotConversation(ctx, botContactID, inboxID); err != nil {
				zap.L().Warn("chatwoot: erro ao criar conversa bot (não crítico)", zap.Error(err))
			}
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
		return 0, fmt.Errorf("erro ao decodificar lista de inboxes: %w", err)
	}

	for _, inbox := range listResp.Payload {
		if inbox.Name == inboxName {
			zap.L().Info("chatwoot: inbox já existe",
				zap.String("name", inboxName),
				zap.Int("id", inbox.ID))
			return inbox.ID, nil
		}
	}

	zap.L().Info("chatwoot: criando nova inbox", zap.String("name", inboxName))
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
		return 0, fmt.Errorf("erro ao decodificar resposta de criar inbox: %w", err)
	}

	if result.ID <= 0 {
		return 0, fmt.Errorf("inbox criada com ID inválido: %s", string(bodyBytes))
	}

	zap.L().Info("chatwoot: ✅ inbox criada", zap.Int("inboxId", result.ID))
	return result.ID, nil
}

func (c *ChatwootService) findOrCreateBotContact(ctx context.Context, inboxID int, orgName, logoURL string) (int, error) {
	id, err := c.searchContactByPhone(ctx, "123456")
	if err == nil && id > 0 {
		zap.L().Info("chatwoot: contato bot já existe", zap.Int("contactId", id))
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
		return 0, fmt.Errorf("erro ao decodificar contato bot: %w", err)
	}

	if result.Payload.ID <= 0 {
		return 0, fmt.Errorf("contato bot criado com ID inválido")
	}

	zap.L().Info("chatwoot: ✅ contato bot criado", zap.Int("contactId", result.Payload.ID))
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
		return fmt.Errorf("erro ao decodificar conversa bot: %w", err)
	}

	if conv.ID <= 0 {
		return fmt.Errorf("conversa bot criada com ID inválido")
	}

	msgURL := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages",
		c.config.URL, c.config.AccountID, conv.ID)
	msgBody := map[string]interface{}{
		"content":      "init",
		"message_type": "outgoing",
	}

	msgResp, err := c.doRequest(ctx, "POST", msgURL, msgBody)
	if err != nil {
		return fmt.Errorf("erro ao enviar mensagem init: %w", err)
	}
	defer msgResp.Body.Close()

	zap.L().Info("chatwoot: ✅ conversa bot criada e mensagem init enviada",
		zap.Int("conversationId", conv.ID))

	return nil
}

// ── Handler principal ─────────────────────────────────────────────────────────

// HandleMessageOptions contém opções adicionais para o HandleMessage.
type HandleMessageOptions struct {
	// ProfilePicURL é a URL da foto de perfil do contato (pode ser "").
	ProfilePicURL string
	// InstanceJID é o JID da própria instância (ex: "5511999...@s.whatsapp.net").
	// Usado para ignorar mensagens enviadas para si mesmo (Mensagens Salvas / sync QR code).
	InstanceJID string
	// GroupName é o nome real do grupo (obtido via client.GetGroupInfo).
	// Se vazio, usa o ID numérico do grupo como nome.
	GroupName string
}

// HandleMessage processa uma mensagem e envia ao Chatwoot.
// instanceID é o ID da instância WhatsMiau (ex: "VENDAS") — usado para
// resolver dinamicamente qual inbox usar, via cache ou busca na API.
// profilePicURL é a URL da foto de perfil do contato (pode ser "").
func (c *ChatwootService) HandleMessage(instanceID string, messageData *WookMessageData, opts HandleMessageOptions) {
	if !c.IsEnabled() {
		return
	}

	if messageData == nil || messageData.Key == nil {
		return
	}

	// Ignora mensagens de canais que não devem abrir conversas no Chatwoot:
	// - status@broadcast: atualizações de status do WhatsApp
	// - newsletter: canais/newsletters do WhatsApp
	// - remoteJid vazio ou inválido
	remoteJidRaw := messageData.Key.RemoteJid
	if strings.Contains(remoteJidRaw, "status@broadcast") ||
		strings.Contains(remoteJidRaw, "newsletter") ||
		strings.Contains(remoteJidRaw, "lid") {
		return
	}

	isFromMe := messageData.Key.FromMe

	// Ignora mensagens do próprio número para si mesmo (Mensagens Salvas / sync pós-QR code).
	// Só é possível detectar se o JID da instância foi passado — compara o remoteJid com o próprio número.
	if isFromMe && opts.InstanceJID != "" {
		ownPhone := extractPhone(opts.InstanceJID)
		remotePhone := extractPhone(remoteJidRaw)
		if ownPhone != "" && ownPhone == remotePhone {
			zap.L().Debug("chatwoot: ignorando mensagem para si mesmo (saved messages / history sync)",
				zap.String("remoteJid", remoteJidRaw))
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Garante que o labelName está preenchido com o instanceID
	if c.labelName == "" {
		c.labelName = instanceID
	}

	// Resolve o inboxID dinamicamente para esta instância (sem depender do env)
	inboxID, err := c.getInboxIDForInstance(ctx, instanceID)
	if err != nil {
		zap.L().Error("chatwoot: inbox não encontrada, ignorando mensagem",
			zap.String("instanceID", instanceID),
			zap.Error(err))
		return
	}

	remoteJid := remoteJidRaw

	phone := extractPhone(remoteJid)
	if phone == "" {
		zap.L().Warn("chatwoot: número inválido", zap.String("remoteJid", remoteJid))
		return
	}

	isGroup := strings.Contains(remoteJid, "@g.us")

	// Nome do contato/grupo no Chatwoot:
	// - Grupos: usa o nome do grupo (remoteJid sem sufixo) — pushName aqui é do remetente interno
	// - fromMe=true: pushName é do operador; usamos o número até o cliente responder
	// - incoming normal: usa o pushName do cliente
	pushName := messageData.PushName
	contactName := pushName
	if isGroup {
		// Para grupos, usa o nome real do grupo se disponível (passado via opts.GroupName).
		// pushName é o nome do REMETENTE, não do grupo — nunca usar para nomear o contato do grupo.
		groupID := extractPhone(remoteJid)
		if opts.GroupName != "" {
			contactName = opts.GroupName
		} else {
			contactName = fmt.Sprintf("Grupo %s", groupID)
		}
	} else if isFromMe {
		// fromMe=true: pushName é do operador — usa número como nome do cliente
		contactName = phone
	} else if contactName == "" {
		contactName = phone
	}

	// Remetente real dentro do grupo (para prefixar a mensagem)
	var senderName, senderPhone string
	if isGroup && !isFromMe {
		participant := messageData.Key.Participant
		senderPhone = extractPhone(participant)
		senderName = pushName // em grupos, pushName é o nome do remetente
	}

	messageID := messageData.Key.Id

	messageType := "incoming"
	if isFromMe {
		messageType = "outgoing"
	}

	zap.L().Info("chatwoot: 📞 processando mensagem",
		zap.String("phone", phone),
		zap.String("messageId", messageID),
		zap.String("contactName", contactName),
		zap.String("type", messageType),
		zap.Bool("isGroup", isGroup))

	// Passa o remoteJid completo como identifier e a foto de perfil
	contactID, err := c.findOrCreateContact(ctx, phone, contactName, remoteJid, opts.ProfilePicURL)
	if err != nil {
		zap.L().Error("chatwoot: erro ao buscar/criar contato", zap.Error(err))
		return
	}

	if contactID <= 0 {
		zap.L().Error("chatwoot: contactID inválido retornado",
			zap.Int("contactId", contactID),
			zap.String("phone", phone))
		return
	}

	zap.L().Info("chatwoot: ✅ contato obtido", zap.Int("contactId", contactID))

	conversationID, err := c.findOrCreateConversation(ctx, contactID, inboxID, isFromMe)
	if err != nil {
		zap.L().Error("chatwoot: erro ao buscar/criar conversa", zap.Error(err))
		return
	}

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

	// Reactions do WhatsApp: envia como reply no Chatwoot apontando para a mensagem original.
	// Usa content_attributes.in_reply_to com o ID interno da mensagem no Chatwoot,
	// buscado via SQL pelo source_id = WAID:{reactionKey.Id}.
	if msg.ReactionMessage != nil {
		emoji := msg.ReactionMessage.Text
		if emoji == "" {
			// Reaction vazia = remoção de reaction, ignorar
			return
		}
		var originalMsgID string
		if msg.ReactionMessage.Key != nil {
			originalMsgID = msg.ReactionMessage.Key.Id
		}
		if err := c.sendReactionAsReply(ctx, conversationID, emoji, originalMsgID, messageID, isFromMe); err != nil {
			zap.L().Error("chatwoot: erro ao enviar reaction como reply", zap.Error(err))
		}
		return
	}


	// Prefixo para mensagens de grupo: "**Nome** (telefone) diz:"
	// Só em incoming (não em mensagens enviadas pelo operador)
	groupPrefix := ""
	if isGroup && !isFromMe {
		if senderName != "" && senderPhone != "" {
			groupPrefix = fmt.Sprintf("**%s** (%s) diz:\n", senderName, senderPhone)
		} else if senderName != "" {
			groupPrefix = fmt.Sprintf("**%s** diz:\n", senderName)
		} else if senderPhone != "" {
			groupPrefix = fmt.Sprintf("**%s** diz:\n", senderPhone)
		}
	}

	if msg.Base64 != "" {
		filename, caption, mimetype := extractMediaMeta(messageData)

		// Adiciona prefixo na legenda da mídia (se houver) ou envia mensagem separada
		if groupPrefix != "" {
			caption = groupPrefix + caption
		}

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

	messageText := extractMessageText(messageData)
	if messageText == "" {
		zap.L().Warn("chatwoot: mensagem sem conteúdo, ignorando",
			zap.String("type", messageData.MessageType))
		return
	}

	// Adiciona prefixo de grupo ao texto
	if groupPrefix != "" {
		messageText = groupPrefix + messageText
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
	if c.db == nil {
		return false, nil
	}

	if conversationID <= 0 {
		return false, nil
	}

	query := `
		SELECT COUNT(*) 
		FROM messages 
		WHERE conversation_id = $1 
		AND source_id = $2
		LIMIT 1
	`

	var count int
	err := c.db.QueryRowContext(ctx, query, conversationID, sourceID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("erro ao verificar mensagem existente: %w", err)
	}

	exists := count > 0
	if exists {
		zap.L().Info("chatwoot: 🚫 DUPLICATA DETECTADA",
			zap.String("sourceId", sourceID),
			zap.Int("conversationId", conversationID))
	}

	return exists, nil
}

// ── Contatos ──────────────────────────────────────────────────────────────────

func (c *ChatwootService) findOrCreateContact(ctx context.Context, phone, name, identifier, profilePicURL string) (int, error) {
	zap.L().Info("chatwoot: 🔍 buscando contato",
		zap.String("phone", phone),
		zap.String("name", name))

	isGroup := strings.Contains(identifier, "@g.us")

	if isGroup {
		// Para grupos, busca pelo identifier (chatId sem "@g.us") — nunca pelo phone_number,
		// pois o "phone" de um grupo é o ID numérico do grupo (ex: 559991905538-1635585672)
		// que não corresponde a nenhum phone_number real e pode causar falsos positivos.
		contactID, err := c.searchContactByIdentifier(ctx, identifier)
		if err == nil && contactID > 0 {
			zap.L().Info("chatwoot: ✅ contato de grupo ENCONTRADO por identifier", zap.Int("contactId", contactID))
			return contactID, nil
		}
		zap.L().Info("chatwoot: 📝 criando novo contato de grupo", zap.String("phone", phone))
		return c.createContact(ctx, phone, name, identifier, profilePicURL)
	}

	// Contatos individuais: busca pelo phone_number normalmente
	contactID, err := c.searchContactByPhone(ctx, phone)
	if err != nil {
		return 0, err
	}
	if contactID > 0 {
		zap.L().Info("chatwoot: ✅ contato ENCONTRADO", zap.Int("contactId", contactID))
		return contactID, nil
	}

	zap.L().Info("chatwoot: 📝 criando novo contato", zap.String("phone", phone))
	return c.createContact(ctx, phone, name, identifier, profilePicURL)
}

// getBrazilianPhoneVariants retorna variantes do número para Brasil (+55):
// números com 9 dígito (14 chars com "+") geram versão sem o 9 e vice-versa.
func getBrazilianPhoneVariants(phone string) []string {
	numbers := []string{phone}
	// phone aqui não tem "+", ex: "5511987654321" (13 chars) ou "551187654321" (12 chars)
	if strings.HasPrefix(phone, "55") {
		withPlus := "+" + phone
		if len(withPlus) == 14 { // +55 + DDD(2) + 9(1) + número(8) = 14
			withoutNine := withPlus[:5] + withPlus[6:]
			numbers = append(numbers, strings.TrimPrefix(withoutNine, "+"))
		} else if len(withPlus) == 13 { // +55 + DDD(2) + número(8) = 13
			withNine := withPlus[:5] + "9" + withPlus[5:]
			numbers = append(numbers, strings.TrimPrefix(withNine, "+"))
		}
	}
	return numbers
}

// buildFilterPayload constrói o payload de filtro com variantes de número.
// Os values são enviados SEM "+", espelhando o getFilterPayload do Evolution API.
func buildFilterPayload(phone string) []map[string]interface{} {
	numbers := getBrazilianPhoneVariants(phone)
	payload := make([]map[string]interface{}, 0, len(numbers))
	lastIdx := len(numbers) - 1

	for i, number := range numbers {
		var queryOperator interface{} = "OR"
		if i == lastIdx {
			queryOperator = nil
		}
		payload = append(payload, map[string]interface{}{
			"attribute_key":   "phone_number",
			"filter_operator": "equal_to",
			"values":          []string{number}, // sem "+" — igual ao Evolution API
			"query_operator":  queryOperator,
		})
	}
	return payload
}

func (c *ChatwootService) searchContactByPhone(ctx context.Context, phone string) (int, error) {
	filterURL := fmt.Sprintf("%s/api/v1/accounts/%s/contacts/filter", c.config.URL, c.config.AccountID)
	body := map[string]interface{}{
		"payload": buildFilterPayload(phone),
	}

	resp, err := c.doRequest(ctx, "POST", filterURL, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	var result chatwootContactFilterResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return 0, fmt.Errorf("erro ao decodificar filtro de contatos: %w", err)
	}

	if len(result.Payload) == 0 {
		return 0, nil
	}

	// Múltiplos resultados (ex: +55 com/sem 9): pega o de número mais longo
	if len(result.Payload) > 1 {
		return c.findBestContactMatch(result.Payload, phone), nil
	}

	return result.Payload[0].ID, nil
}

// findBestContactMatch seleciona o contato cujo phone_number bate com a variante mais longa.
func (c *ChatwootService) findBestContactMatch(contacts []chatwootContact, phone string) int {
	variants := getBrazilianPhoneVariants(phone)

	longestVariant := ""
	for _, v := range variants {
		if len(v) > len(longestVariant) {
			longestVariant = v
		}
	}

	for _, contact := range contacts {
		if contact.PhoneNumber == "+"+longestVariant {
			return contact.ID
		}
	}

	return contacts[0].ID
}

// searchContactByIdentifier busca contato pelo remoteJid (identifier) via filtro.
func (c *ChatwootService) searchContactByIdentifier(ctx context.Context, identifier string) (int, error) {
	filterURL := fmt.Sprintf("%s/api/v1/accounts/%s/contacts/filter", c.config.URL, c.config.AccountID)
	body := map[string]interface{}{
		"payload": []map[string]interface{}{
			{
				"attribute_key":   "identifier",
				"filter_operator": "equal_to",
				"values":          []string{identifier},
				"query_operator":  nil,
			},
		},
	}

	resp, err := c.doRequest(ctx, "POST", filterURL, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	var result chatwootContactFilterResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return 0, fmt.Errorf("erro ao decodificar filtro por identifier: %w", err)
	}

	if len(result.Payload) > 0 {
		return result.Payload[0].ID, nil
	}
	return 0, nil
}

// searchContact mantido para compatibilidade com o bot contact (busca por "123456")
func (c *ChatwootService) searchContact(ctx context.Context, phone string) (int, error) {
	return c.searchContactByPhone(ctx, phone)
}

func (c *ChatwootService) createContact(ctx context.Context, phone, name, identifier, profilePicURL string) (int, error) {
	apiURL := fmt.Sprintf("%s/api/v1/accounts/%s/contacts", c.config.URL, c.config.AccountID)

	isGroup := strings.Contains(identifier, "@g.us")

	var body map[string]interface{}
	if isGroup {
		// Grupos: identifier é o próprio remoteJid do grupo, sem phone_number
		body = map[string]interface{}{
			"name":       name,
			"identifier": identifier, // para grupos usa o chatId sem "@g.us"
		}
	} else {
		// Contatos individuais: phone_number com "+" e identifier = remoteJid
		body = map[string]interface{}{
			"name":       name,
			"identifier": identifier,
		}
		// Inclui phone_number se o identifier contiver "@" (JID normal) ou se não tiver identifier
		if strings.Contains(identifier, "@") || identifier == "" {
			body["phone_number"] = fmt.Sprintf("+%s", phone)
		}
	}

	// Inclui avatar se disponível
	if profilePicURL != "" {
		body["avatar_url"] = profilePicURL
	}

	resp, err := c.doRequest(ctx, "POST", apiURL, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	zap.L().Info("chatwoot: resposta bruta da criação de contato",
		zap.String("response", string(bodyBytes)))

	// HTTP 422 = conflito (contato já existe com esse identifier ou phone_number).
	// Espelha o tratamento do Evolution API: tenta recuperar o contato existente.
	if resp.StatusCode == 422 {
		zap.L().Warn("chatwoot: criação retornou 422, buscando contato existente",
			zap.String("identifier", identifier),
			zap.String("phone", phone))

		isGroupRetry := strings.Contains(identifier, "@g.us")

		if isGroupRetry {
			// Para grupos, o identifier armazenado é o phone (sem @g.us)
			existingID, err := c.searchContactByIdentifier(ctx, phone)
			if err == nil && existingID > 0 {
				zap.L().Info("chatwoot: ✅ grupo recuperado por identifier (phone) após 422", zap.Int("contactId", existingID))
				_ = c.addLabelToContact(ctx, existingID)
				return existingID, nil
			}
			return 0, fmt.Errorf("grupo já existe (422) mas não foi possível recuperá-lo: phone=%s", phone)
		}

		// Contatos individuais: tenta por identifier completo primeiro
		if identifier != "" {
			existingID, err := c.searchContactByIdentifier(ctx, identifier)
			if err == nil && existingID > 0 {
				zap.L().Info("chatwoot: ✅ contato recuperado por identifier após 422", zap.Int("contactId", existingID))
				_ = c.addLabelToContact(ctx, existingID)
				return existingID, nil
			}
		}

		// Fallback: busca pelo número
		existingID, err := c.searchContactByPhone(ctx, phone)
		if err == nil && existingID > 0 {
			zap.L().Info("chatwoot: ✅ contato recuperado por phone após 422", zap.Int("contactId", existingID))
			_ = c.addLabelToContact(ctx, existingID)
			return existingID, nil
		}

		return 0, fmt.Errorf("contato já existe (422) mas não foi possível recuperá-lo: identifier=%s phone=%s", identifier, phone)
	}

	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("erro ao criar contato: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var result chatwootContactCreateResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		zap.L().Error("chatwoot: erro ao decodificar resposta de criar contato",
			zap.Error(err),
			zap.String("response", string(bodyBytes)))
		return 0, fmt.Errorf("erro ao decodificar resposta: %w", err)
	}

	contactID := contactIDFromCreateResponse(result)
	if contactID <= 0 {
		zap.L().Error("chatwoot: ID de contato inválido retornado pela API",
			zap.Int("contactId", contactID),
			zap.String("response", string(bodyBytes)))
		return 0, fmt.Errorf("API retornou contactId inválido: %d", contactID)
	}

	zap.L().Info("chatwoot: ✅ contato CRIADO", zap.Int("contactId", contactID))

	// Aplica label com nome da inbox ao contato recém-criado
	_ = c.addLabelToContact(ctx, contactID)

	return contactID, nil
}

// addLabelToContact aplica a label com o nome da inbox ao contato via SQL direto,
// espelhando o addLabelToContact do Evolution API.
func (c *ChatwootService) addLabelToContact(ctx context.Context, contactID int) error {
	if c.db == nil || c.labelName == "" {
		return nil
	}

	// Upsert da tag (incrementa taggings_count)
	sqlTag := `INSERT INTO tags (name, taggings_count)
		VALUES ($1, 1)
		ON CONFLICT (name)
		DO UPDATE SET taggings_count = tags.taggings_count + 1
		RETURNING id`

	var tagID int
	if err := c.db.QueryRowContext(ctx, sqlTag, c.labelName).Scan(&tagID); err != nil {
		return fmt.Errorf("erro ao upsert tag: %w", err)
	}

	// Verifica se o tagging já existe antes de inserir
	var exists int
	sqlCheck := `SELECT 1 FROM taggings
		WHERE tag_id = $1 AND taggable_type = 'Contact' AND taggable_id = $2 AND context = 'labels'
		LIMIT 1`
	_ = c.db.QueryRowContext(ctx, sqlCheck, tagID, contactID).Scan(&exists)

	if exists == 0 {
		sqlInsert := `INSERT INTO taggings (tag_id, taggable_type, taggable_id, context, created_at)
			VALUES ($1, 'Contact', $2, 'labels', NOW())`
		if _, err := c.db.ExecContext(ctx, sqlInsert, tagID, contactID); err != nil {
			return fmt.Errorf("erro ao inserir tagging: %w", err)
		}
		zap.L().Info("chatwoot: ✅ label aplicada ao contato",
			zap.String("label", c.labelName),
			zap.Int("contactId", contactID))
	}

	return nil
}

// ── Conversas ─────────────────────────────────────────────────────────────────

func (c *ChatwootService) findOrCreateConversation(ctx context.Context, contactID, inboxID int, isFromMe bool) (int, error) {
	if contactID <= 0 {
		return 0, fmt.Errorf("contactID inválido: %d", contactID)
	}

	lockKey := fmt.Sprintf("%d-%d", contactID, inboxID)
	lock := getConversationLock(lockKey)

	lock.Lock()
	defer lock.Unlock()

	zap.L().Info("chatwoot: 🔒 lock conversa",
		zap.String("key", lockKey))

	id, err := c.findOrReopenConversation(ctx, contactID, inboxID)
	if err != nil {
		return 0, err
	}

	if id > 0 {
		return id, nil
	}

	zap.L().Info("chatwoot: 📝 criando nova conversa",
		zap.Int("contactId", contactID))

	return c.createConversation(ctx, contactID, inboxID, isFromMe)
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

	if len(result.Payload) == 0 {
		return 0, nil
	}

	for _, conv := range result.Payload {
		if conv.InboxID != inboxID {
			continue
		}

		zap.L().Info("chatwoot: 📋 conversa encontrada",
			zap.Int("conversationId", conv.ID),
			zap.String("status", conv.Status))

		if conv.Status == "resolved" {
			zap.L().Info("chatwoot: ♻️ reabrindo conversa resolvida",
				zap.Int("conversationId", conv.ID))

			if err := c.reopenConversation(ctx, conv.ID); err != nil {
				zap.L().Warn("chatwoot: erro ao reabrir conversa", zap.Error(err))
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
		return fmt.Errorf("erro ao reabrir conversa: %d - %s", resp.StatusCode, string(raw))
	}

	zap.L().Info("chatwoot: ✅ conversa reaberta", zap.Int("conversationId", conversationID))
	return nil
}

func (c *ChatwootService) createConversation(ctx context.Context, contactID, inboxID int, isFromMe bool) (int, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations",
		c.config.URL,
		c.config.AccountID)

	body := map[string]interface{}{
		"contact_id": contactID,
		"inbox_id":   inboxID,
	}

	// Quando a mensagem parte do operador (fromMe), força status "open" imediatamente
	// para que a conversa apareça no Chatwoot sem precisar aguardar resposta do cliente.
	if isFromMe {
		body["status"] = "open"
	}

	resp, err := c.doRequest(ctx, "POST", url, body)
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

	zap.L().Info("chatwoot: ✅ nova conversa criada",
		zap.Int("conversationId", result.ID))

	return result.ID, nil
}

// ── Envio de mensagens ────────────────────────────────────────────────────────

func (c *ChatwootService) sendMessage(ctx context.Context, conversationID int, content, messageID string, isFromMe bool) error {
	sourceID := fmt.Sprintf("WAID:%s", messageID)

	messageType := "incoming"
	if isFromMe {
		messageType = "outgoing"
	}

	exists, err := c.checkMessageExists(ctx, conversationID, sourceID)
	if err != nil {
		zap.L().Warn("chatwoot: erro ao verificar duplicata, continuando envio", zap.Error(err))
	} else if exists {
		zap.L().Info("chatwoot: 🚫 mensagem já existe, IGNORANDO envio",
			zap.String("sourceId", sourceID),
			zap.Int("conversationId", conversationID))
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

	resp, err := c.doRequest(ctx, "POST", url, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chatwoot erro %d: %s", resp.StatusCode, string(raw))
	}

	zap.L().Info("chatwoot: ✅ mensagem de texto enviada com SUCESSO",
		zap.String("sourceId", sourceID),
		zap.Int("statusCode", resp.StatusCode),
		zap.String("messageType", messageType))

	return nil
}

// sendReactionAsReply envia uma reaction do WhatsApp como mensagem de emoji no Chatwoot.
// A API do Chatwoot não expõe campos de reply (in_reply_to) no endpoint de criação de mensagem,
// portanto o emoji é enviado como mensagem de texto simples na conversa.
func (c *ChatwootService) sendReactionAsReply(ctx context.Context, conversationID int, emoji, _, reactionMsgID string, isFromMe bool) error {
	sourceID := fmt.Sprintf("WAID:%s", reactionMsgID)

	messageType := "incoming"
	if isFromMe {
		messageType = "outgoing"
	}

	exists, err := c.checkMessageExists(ctx, conversationID, sourceID)
	if err != nil {
		zap.L().Warn("chatwoot: erro ao verificar duplicata de reaction", zap.Error(err))
	} else if exists {
		return nil
	}

	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages",
		c.config.URL, c.config.AccountID, conversationID)

	body := map[string]interface{}{
		"content":      emoji,
		"message_type": messageType,
		"private":      false,
		"source_id":    sourceID,
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

	zap.L().Info("chatwoot: ✅ reaction enviada como emoji",
		zap.String("emoji", emoji),
		zap.Int("conversationId", conversationID))

	return nil
}

func (c *ChatwootService) sendMediaMessage(
	ctx context.Context,
	conversationID int,
	mediaBytes []byte,
	filename, mimetype, caption, messageID string,
	isFromMe bool,
) error {
	sourceID := fmt.Sprintf("WAID:%s", messageID)

	messageType := "incoming"
	if isFromMe {
		messageType = "outgoing"
	}

	exists, err := c.checkMessageExists(ctx, conversationID, sourceID)
	if err != nil {
		zap.L().Warn("chatwoot: erro ao verificar duplicata, continuando envio", zap.Error(err))
	} else if exists {
		zap.L().Info("chatwoot: 🚫 mídia já existe, IGNORANDO envio",
			zap.String("sourceId", sourceID),
			zap.Int("conversationId", conversationID))
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

func cleanMimetype(raw, fallback string) string {
	if raw == "" {
		return fallback
	}
	parts := strings.SplitN(raw, ";", 2)
	clean := strings.TrimSpace(parts[0])
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
		nome := msg.ContactMessage.DisplayName
		vcard := msg.ContactMessage.VCard

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
