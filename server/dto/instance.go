package dto

import "github.com/verbeux-ai/whatsmiau/models"

type CreateInstanceRequest struct {
	InstanceName string `json:"instanceName" validate:"required"`

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

	Webhook models.InstanceWebhook `json:"webhook,omitempty"`

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
	ProxyHost     string `json:"proxyHost,omitempty"`
	ProxyPort     string `json:"proxyPort,omitempty"`
	ProxyProtocol string `json:"proxyProtocol,omitempty"`
	ProxyUsername string `json:"proxyUsername,omitempty"`
	ProxyPassword string `json:"proxyPassword,omitempty"`
}

type CreateInstanceResponse struct {
	*models.Instance
}

type UpdateInstanceRequest struct {
	ID      string `json:"id,omitempty" param:"id" validate:"required"`
	Webhook struct {
		Base64 bool     `json:"base64,omitempty"`
		URL    string   `json:"url,omitempty"`
		Events []string `json:"events,omitempty"`
	} `json:"webhook,omitempty"`
}

type UpdateInstanceResponse struct {
	*models.Instance
}

type ListInstancesRequest struct {
	InstanceName string `query:"instanceName"`
	ID           string `query:"id"`
}

type ListInstancesResponse struct {
	*models.Instance

	OwnerJID     string `json:"ownerJid,omitempty"`
	InstanceName string `json:"instanceName,omitempty"`
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
