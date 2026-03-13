package dto

import "github.com/verbeux-ai/whatsmiau/models"

type CreateInstanceRequest struct {
	InstanceName string `json:"instanceName" validate:"required"`
	Name         string `json:"name,omitempty"`

	Integration string `json:"integration,omitempty"`
	Token       string `json:"token,omitempty"`
	QRCode      bool   `json:"qrcode,omitempty"`
	Number      string `json:"number,omitempty"`

	RejectCall      bool   `json:"rejectCall,omitempty"`
	MsgCall         string `json:"msgCall,omitempty"`
	GroupsIgnore    bool   `json:"groupsIgnore,omitempty"`
	AlwaysOnline    bool   `json:"alwaysOnline,omitempty"`
	ReadMessages    bool   `json:"readMessages,omitempty"`
	ReadStatus      bool   `json:"readStatus,omitempty"`
	SyncFullHistory bool   `json:"syncFullHistory,omitempty"`
	SGPEnabled      bool   `json:"sgpEnabled,omitempty"`
	SGPToken        string `json:"sgpToken,omitempty"`
	SGPAllowedIPs   string `json:"sgpAllowedIPs,omitempty"`
	SGPSyncChatwoot bool   `json:"sgpSyncChatwoot,omitempty"`

	Webhook *models.InstanceWebhook `json:"webhook,omitempty"`

	// ===== Chatwoot =====
	ChatwootAccountID               *string `json:"chatwootAccountId,omitempty"`
	ChatwootToken                   *string `json:"chatwootToken,omitempty"`
	ChatwootURL                     *string `json:"chatwootUrl,omitempty"`
	ChatwootSignMsg                 *bool   `json:"chatwootSignMsg,omitempty"`
	ChatwootReopenConversation      *bool   `json:"chatwootReopenConversation,omitempty"`
	ChatwootConversationPending     *bool   `json:"chatwootConversationPending,omitempty"`
	ChatwootImportContacts          *bool   `json:"chatwootImportContacts,omitempty"`
	ChatwootNameInbox               *string `json:"chatwootNameInbox,omitempty"`
	ChatwootMergeBrazilContacts     *bool   `json:"chatwootMergeBrazilContacts,omitempty"`
	ChatwootImportMessages          *bool   `json:"chatwootImportMessages,omitempty"`
	ChatwootDaysLimitImportMessages *int    `json:"chatwootDaysLimitImportMessages,omitempty"`
	ChatwootOrganization            *string `json:"chatwootOrganization,omitempty"`
	ChatwootLogo                    *string `json:"chatwootLogo,omitempty"`

	// ===== Proxy =====
	ProxyHost     string `json:"proxyHost,omitempty"`
	ProxyPort     string `json:"proxyPort,omitempty"`
	ProxyProtocol string `json:"proxyProtocol,omitempty"`
	ProxyUsername string `json:"proxyUsername,omitempty"`
	ProxyPassword string `json:"proxyPassword,omitempty"`
}

type CreateInstanceResponse struct {
	models.Instance
}

type UpdateInstanceRequest struct {
	ID   string  `param:"id" validate:"required"`
	Name *string `json:"name,omitempty"`

	RejectCall      *bool   `json:"rejectCall,omitempty"`
	MsgCall         *string `json:"msgCall,omitempty"`
	GroupsIgnore    *bool   `json:"groupsIgnore,omitempty"`
	AlwaysOnline    *bool   `json:"alwaysOnline,omitempty"`
	ReadMessages    *bool   `json:"readMessages,omitempty"`
	ReadStatus      *bool   `json:"readStatus,omitempty"`
	SyncFullHistory *bool   `json:"syncFullHistory,omitempty"`
	SGPEnabled      *bool   `json:"sgpEnabled,omitempty"`
	SGPToken        *string `json:"sgpToken,omitempty"`
	SGPAllowedIPs   *string `json:"sgpAllowedIPs,omitempty"`
	SGPSyncChatwoot *bool   `json:"sgpSyncChatwoot,omitempty"`

	Webhook *models.InstanceWebhook `json:"webhook,omitempty"`

	// ===== Chatwoot =====
	ChatwootAccountID               *string `json:"chatwootAccountId,omitempty"`
	ChatwootToken                   *string `json:"chatwootToken,omitempty"`
	ChatwootURL                     *string `json:"chatwootUrl,omitempty"`
	ChatwootSignMsg                 *bool   `json:"chatwootSignMsg,omitempty"`
	ChatwootReopenConversation      *bool   `json:"chatwootReopenConversation,omitempty"`
	ChatwootConversationPending     *bool   `json:"chatwootConversationPending,omitempty"`
	ChatwootImportContacts          *bool   `json:"chatwootImportContacts,omitempty"`
	ChatwootNameInbox               *string `json:"chatwootNameInbox,omitempty"`
	ChatwootMergeBrazilContacts     *bool   `json:"chatwootMergeBrazilContacts,omitempty"`
	ChatwootImportMessages          *bool   `json:"chatwootImportMessages,omitempty"`
	ChatwootDaysLimitImportMessages *int    `json:"chatwootDaysLimitImportMessages,omitempty"`
	ChatwootOrganization            *string `json:"chatwootOrganization,omitempty"`
	ChatwootLogo                    *string `json:"chatwootLogo,omitempty"`

	// ===== Proxy =====
	ProxyHost     *string `json:"proxyHost,omitempty"`
	ProxyPort     *string `json:"proxyPort,omitempty"`
	ProxyProtocol *string `json:"proxyProtocol,omitempty"`
	ProxyUsername *string `json:"proxyUsername,omitempty"`
	ProxyPassword *string `json:"proxyPassword,omitempty"`
}

type UpdateInstanceResponse struct {
	*models.Instance
}

type ListInstancesRequest struct {
	InstanceName string `query:"instanceName"`
	ID           string `query:"id"`
}

// SGPInstanceInfo espelha o objeto "SGP" na resposta da API
type SGPInstanceInfo struct {
	Enabled      bool   `json:"enabled"`
	Token        string `json:"token,omitempty"`
	AllowedIPs   string `json:"allowedIPs,omitempty"`
	SyncChatwoot bool   `json:"syncChatwoot"`
}

// ChatwootInstanceInfo espelha o objeto "Chatwoot" da Evolution API
type ChatwootInstanceInfo struct {
	Enabled                 bool     `json:"enabled"`
	AccountID               string   `json:"accountId"`
	Token                   string   `json:"token"`
	URL                     string   `json:"url"`
	NameInbox               string   `json:"nameInbox"`
	SignMsg                 bool     `json:"signMsg"`
	SignDelimiter           *string  `json:"signDelimiter"`
	Number                  *string  `json:"number"`
	ReopenConversation      bool     `json:"reopenConversation"`
	ConversationPending     bool     `json:"conversationPending"`
	MergeBrazilContacts     bool     `json:"mergeBrazilContacts"`
	ImportContacts          bool     `json:"importContacts"`
	ImportMessages          bool     `json:"importMessages"`
	DaysLimitImportMessages int      `json:"daysLimitImportMessages"`
	Organization            string   `json:"organization"`
	Logo                    string   `json:"logo"`
	IgnoreJids              []string `json:"ignoreJids"`
}

// InstanceBaseInfo contém apenas os campos não-chatwoot da instância.
// Usado para não vazar os campos flat chatwoot na resposta de listagem.
type InstanceBaseInfo struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Integration string `json:"integration,omitempty"`
	Token       string `json:"token,omitempty"`
	QRCode      bool   `json:"qrcode"`
	Number      string `json:"number,omitempty"`

	RejectCall      bool   `json:"rejectCall"`
	MsgCall         string `json:"msgCall"`
	GroupsIgnore    bool   `json:"groupsIgnore"`
	AlwaysOnline    bool   `json:"alwaysOnline"`
	ReadMessages    bool   `json:"readMessages"`
	ReadStatus      bool   `json:"readStatus"`
	SyncFullHistory bool   `json:"syncFullHistory"`

	RemoteJID string                   `json:"remoteJID,omitempty"`
	Webhook   *models.InstanceWebhook  `json:"webhook,omitempty"`

	ProxyHost     string `json:"proxyHost,omitempty"`
	ProxyPort     string `json:"proxyPort,omitempty"`
	ProxyProtocol string `json:"proxyProtocol,omitempty"`
	ProxyUsername string `json:"proxyUsername,omitempty"`
	ProxyPassword string `json:"proxyPassword,omitempty"`
}

type ListInstancesResponse struct {
	InstanceBaseInfo

	OwnerJID         string                `json:"ownerJid,omitempty"`
	InstanceName     string                `json:"instanceName,omitempty"`
	Name             string                `json:"name,omitempty"`
	ConnectionStatus string                `json:"connectionStatus,omitempty"`
	Chatwoot         *ChatwootInstanceInfo `json:"Chatwoot,omitempty"`
	SGP              *SGPInstanceInfo      `json:"SGP"`
}

type ConnectInstanceRequest struct {
	ID string `param:"id" validate:"required"`
}

type ConnectInstanceResponse struct {
	Message   string `json:"message,omitempty"`
	Connected bool   `json:"connected,omitempty"`
	Base64    string `json:"base64,omitempty"`
	*models.Instance
}

type StatusInstanceRequest struct {
	ID string `param:"id" validate:"required"`
}

type StatusInstanceResponse struct {
	ID       string                                        `json:"id,omitempty"`
	Status   string                                        `json:"state,omitempty"`
	Instance *StatusInstanceResponseEvolutionCompatibility `json:"instance,omitempty"`
}

type StatusInstanceResponseEvolutionCompatibility struct {
	InstanceName string `json:"instanceName,omitempty"`
	State        string `json:"state,omitempty"`
}

type DeleteInstanceRequest struct {
	ID string `param:"id" validate:"required"`
}

type DeleteInstanceResponse struct {
	Message string `json:"message,omitempty"`
}

type LogoutInstanceRequest struct {
	ID string `param:"id" validate:"required"`
}

type LogoutInstanceResponse struct {
	Message string `json:"message,omitempty"`
}
