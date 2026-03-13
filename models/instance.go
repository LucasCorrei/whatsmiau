package models

type InstanceWebhook struct {
	Url      string            `json:"url,omitempty"`
	Base64   *bool             `json:"base64,omitempty"`
	ByEvents *bool             `json:"byEvents,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Events   []string          `json:"events,omitempty"`
}

type InstanceProxy struct {
	ProxyHost     string `json:"proxyHost,omitempty"`
	ProxyPort     string `json:"proxyPort,omitempty"`
	ProxyProtocol string `json:"proxyProtocol,omitempty"`
	ProxyUsername string `json:"proxyUsername,omitempty"`
	ProxyPassword string `json:"proxyPassword,omitempty"`
}

type Instance struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`

	// ===== Core Config =====
	Integration string `json:"integration,omitempty"`
	Token       string `json:"token,omitempty"`
	QRCode      bool   `json:"qrcode"`
	Number      string `json:"number,omitempty"`

	RejectCall        bool   `json:"rejectCall"`
	MsgCall           string `json:"msgCall"`
	GroupsIgnore      bool   `json:"groupsIgnore"`
	AlwaysOnline      bool   `json:"alwaysOnline"`
	ReadMessages      bool   `json:"readMessages"`
	ReadStatus        bool   `json:"readStatus"`
	SyncFullHistory   bool   `json:"syncFullHistory"`
	SyncRecentHistory bool   `json:"syncRecentHistory,omitempty"`
	SGPEnabled        bool   `json:"sgpEnabled"`
	SGPToken          string `json:"sgpToken,omitempty"`
	SGPAllowedIPs     string `json:"sgpAllowedIPs,omitempty"`
	SGPSyncChatwoot   bool   `json:"sgpSyncChatwoot"`
	RemoteJID         string `json:"remoteJID,omitempty"`

	// ===== Webhook =====
	Webhook *InstanceWebhook `json:"webhook,omitempty"`

	// ===== Chatwoot =====
	ChatwootAccountID               string `json:"chatwootAccountId"`
	ChatwootToken                   string `json:"chatwootToken"`
	ChatwootURL                     string `json:"chatwootUrl"`
	ChatwootSignMsg                 bool   `json:"chatwootSignMsg"`
	ChatwootReopenConversation      bool   `json:"chatwootReopenConversation"`
	ChatwootConversationPending     bool   `json:"chatwootConversationPending"`
	ChatwootImportContacts          bool   `json:"chatwootImportContacts"`
	ChatwootNameInbox               string `json:"chatwootNameInbox"`
	ChatwootMergeBrazilContacts     bool   `json:"chatwootMergeBrazilContacts"`
	ChatwootImportMessages          bool   `json:"chatwootImportMessages"`
	ChatwootDaysLimitImportMessages int    `json:"chatwootDaysLimitImportMessages"`
	ChatwootOrganization            string `json:"chatwootOrganization"`
	ChatwootLogo                    string `json:"chatwootLogo"`

	// ===== Proxy =====
	InstanceProxy
}
