package models

type Instance struct {
	ID string `json:"id,omitempty"`

	// ===== Core Config =====
	Integration string `json:"integration,omitempty"`
	Token       string `json:"token,omitempty"`
	QRCode      bool   `json:"qrcode,omitempty"`
	Number      string `json:"number,omitempty"`

	RejectCall        bool   `json:"rejectCall,omitempty"`
	MsgCall           string `json:"msgCall,omitempty"`
	GroupsIgnore      bool   `json:"groupsIgnore,omitempty"`
	AlwaysOnline      bool   `json:"alwaysOnline,omitempty"`
	ReadMessages      bool   `json:"readMessages,omitempty"`
	ReadStatus        bool   `json:"readStatus,omitempty"`
	SyncFullHistory   bool   `json:"syncFullHistory,omitempty"`
	SyncRecentHistory bool   `json:"syncRecentHistory,omitempty"`
	RemoteJID         string `json:"remoteJID,omitempty"`

	// ===== Webhook =====
	Webhook InstanceWebhook `json:"webhook,omitempty"`

	// ===== Chatwoot =====
	ChatwootAccountID               int    `json:"chatwootAccountId,omitempty"`
	ChatwootToken                   string `json:"chatwootToken,omitempty"`
	ChatwootURL                     string `json:"chatwootUrl,omitempty"`
	ChatwootSignMsg                 bool   `json:"chatwootSignMsg,omitempty"`
	ChatwootReopenConversation      bool   `json:"chatwootReopenConversation,omitempty"`
	ChatwootConversationPending     bool   `json:"chatwootConversationPending,omitempty"`
	ChatwootImportContacts          bool   `json:"chatwootImportContacts,omitempty"`
	ChatwootNameInbox               string `json:"chatwootNameInbox,omitempty"`
	ChatwootMergeBrazilContacts     bool   `json:"chatwootMergeBrazilContacts,omitempty"`
	ChatwootImportMessages          bool   `json:"chatwootImportMessages,omitempty"`
	ChatwootDaysLimitImportMessages int    `json:"chatwootDaysLimitImportMessages,omitempty"`
	ChatwootOrganization            string `json:"chatwootOrganization,omitempty"`
	ChatwootLogo                    string `json:"chatwootLogo,omitempty"`

	// ===== Proxy =====
	InstanceProxy
}
