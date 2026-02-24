package dto

import "github.com/verbeux-ai/whatsmiau/models"

type CreateInstanceRequest struct {
	ID               string `json:"id,omitempty" validate:"required_without=InstanceName"`
	InstanceName     string `json:"instanceName,omitempty" validate:"required_without=ID"`
	*models.Instance        // optional arguments
}

type CreateInstanceResponse struct {
	*models.Instance
}

type UpdateInstanceRequest struct {
	ID string `json:"id,omitempty" param:"id" validate:"required"`

	// ==============================
	// WEBHOOK
	// ==============================
	Webhook *struct {
		Base64   bool              `json:"base64,omitempty"`
		URL      string            `json:"url,omitempty"`
		ByEvents bool              `json:"byEvents,omitempty"`
		Headers  map[string]string `json:"headers,omitempty"`
		Events   []string          `json:"events,omitempty"`
	} `json:"webhook,omitempty"`

	// ==============================
	// CHATWOOT - ADICIONADO COMPLETO
	// ==============================
	ChatwootEnabled               *bool   `json:"chatwootEnabled,omitempty"`
	ChatwootAccountID             *int    `json:"chatwootAccountId,omitempty"`
	ChatwootToken                 *string `json:"chatwootToken,omitempty"`
	ChatwootURL                   *string `json:"chatwootUrl,omitempty"`
	ChatwootSignMsg               *bool   `json:"chatwootSignMsg,omitempty"`
	ChatwootReopenConversation    *bool   `json:"chatwootReopenConversation,omitempty"`
	ChatwootConversationPending   *bool   `json:"chatwootConversationPending,omitempty"`
	ChatwootImportContacts        *bool   `json:"chatwootImportContacts,omitempty"`
	ChatwootNameInbox             *string `json:"chatwootNameInbox,omitempty"`
	ChatwootMergeBrazilContacts   *bool   `json:"chatwootMergeBrazilContacts,omitempty"`
	ChatwootImportMessages        *bool   `json:"chatwootImportMessages,omitempty"`
	ChatwootDaysLimitImportMsg    *int    `json:"chatwootDaysLimitImportMessages,omitempty"`
	ChatwootOrganization          *string `json:"chatwootOrganization,omitempty"`
	ChatwootLogo                  *string `json:"chatwootLogo,omitempty"`

	// ==============================
	// RABBITMQ
	// ==============================
	RabbitMQ *struct {
		Enabled bool     `json:"enabled,omitempty"`
		Events  []string `json:"events,omitempty"`
	} `json:"rabbitmq,omitempty"`

	// ==============================
	// SQS
	// ==============================
	SQS *struct {
		Enabled bool     `json:"enabled,omitempty"`
		Events  []string `json:"events,omitempty"`
	} `json:"sqs,omitempty"`
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
