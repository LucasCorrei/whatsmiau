package models

type Instance struct {
	ID        string `json:"id"`
	RemoteJID string `json:"remoteJid,omitempty"`

	// ==============================
	// CONFIG PRINCIPAL
	// ==============================
	Integration     string `json:"integration,omitempty"`
	Token           string `json:"token,omitempty"`
	Number          string `json:"number,omitempty"`
	QrCode          bool   `json:"qrcode,omitempty"`
	RejectCall      bool   `json:"rejectCall,omitempty"`
	MsgCall         string `json:"msgCall,omitempty"`
	GroupsIgnore    bool   `json:"groupsIgnore,omitempty"`
	AlwaysOnline    bool   `json:"alwaysOnline,omitempty"`
	ReadMessages    bool   `json:"readMessages,omitempty"`
	ReadStatus      bool   `json:"readStatus,omitempty"`
	SyncFullHistory bool   `json:"syncFullHistory,omitempty"`

	// ==============================
	// PROXY
	// ==============================
	ProxyHost     string `json:"proxyHost,omitempty"`
	ProxyPort     string `json:"proxyPort,omitempty"`
	ProxyProtocol string `json:"proxyProtocol,omitempty"`
	ProxyUsername string `json:"proxyUsername,omitempty"`
	ProxyPassword string `json:"proxyPassword,omitempty"`

	// ==============================
	// WEBHOOK
	// ==============================
	Webhook *InstanceWebhook `json:"webhook,omitempty"`

	// ==============================
	// RABBITMQ
	// ==============================
	RabbitMQ *InstanceBroker `json:"rabbitmq,omitempty"`

	// ==============================
	// SQS
	// ==============================
	SQS *InstanceBroker `json:"sqs,omitempty"`

	// ==============================
	// CHATWOOT (COMPATÍVEL COM EVOLUTION API)
	// ==============================
	ChatwootEnabled               bool   `json:"chatwootEnabled,omitempty"`               // ADICIONADO
	ChatwootAccountId             int    `json:"chatwootAccountId,omitempty"`             // Mantido minúsculo (padrão Evolution)
	ChatwootToken                 string `json:"chatwootToken,omitempty"`
	ChatwootUrl                   string `json:"chatwootUrl,omitempty"`                   // Mantido "Url" (padrão do projeto)
	ChatwootSignMsg               bool   `json:"chatwootSignMsg,omitempty"`
	ChatwootReopenConversation    bool   `json:"chatwootReopenConversation,omitempty"`
	ChatwootConversationPending   bool   `json:"chatwootConversationPending,omitempty"`
	ChatwootImportContacts        bool   `json:"chatwootImportContacts,omitempty"`
	ChatwootNameInbox             string `json:"chatwootNameInbox,omitempty"`
	ChatwootMergeBrazilContacts   bool   `json:"chatwootMergeBrazilContacts,omitempty"`
	ChatwootImportMessages        bool   `json:"chatwootImportMessages,omitempty"`
	ChatwootDaysLimitImportMessages int  `json:"chatwootDaysLimitImportMessages,omitempty"` // Corrigido nome do campo
	ChatwootOrganization          string `json:"chatwootOrganization,omitempty"`
	ChatwootLogo                  string `json:"chatwootLogo,omitempty"`
	ChatwootInboxId               string `json:"chatwootInboxId,omitempty"`               // ADICIONADO - ID da inbox criada
}

type InstanceWebhook struct {
	Url      string            `json:"url,omitempty"`      // "Url" com U maiúsculo (padrão do projeto)
	ByEvents *bool             `json:"byEvents,omitempty"` // Ponteiro para permitir nil
	Base64   *bool             `json:"base64,omitempty"`   // Ponteiro para permitir nil
	Headers  map[string]string `json:"headers,omitempty"`
	Events   []string          `json:"events,omitempty"`
}

type InstanceBroker struct {
	Enabled bool     `json:"enabled,omitempty"`
	Events  []string `json:"events,omitempty"`
}
