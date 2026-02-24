package dto

type ChatwootWebhookRequest struct {
	Event        string                     `json:"event"`
	Account      *ChatwootAccount           `json:"account,omitempty"`
	User         *ChatwootUser              `json:"user,omitempty"`
	Conversation ChatwootConversation       `json:"conversation"`
	Content      string                     `json:"content,omitempty"`
	ContentType  string                     `json:"content_type,omitempty"`
	MessageType  string                     `json:"message_type,omitempty"`
	Private      bool                       `json:"private"`
	Sender       *ChatwootSender            `json:"sender,omitempty"`
	ID           int64                      `json:"id,omitempty"`
	Inbox        *ChatwootInbox             `json:"inbox,omitempty"`
	Attachments  []ChatwootAttachment       `json:"attachments,omitempty"`
	IsPrivate    bool                       `json:"is_private"`
	ContentAttributes map[string]interface{} `json:"content_attributes,omitempty"`
}

type ChatwootAccount struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type ChatwootUser struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Type  string `json:"type"`
}

type ChatwootConversation struct {
	ID               int64                  `json:"id"`
	InboxID          int                    `json:"inbox_id"`
	Status           string                 `json:"status"`
	ContactInbox     ChatwootContactInbox   `json:"contact_inbox"`
	Messages         []ChatwootMessage      `json:"messages"`
	Meta             ChatwootMeta           `json:"meta"`
	AdditionalAttributes map[string]interface{} `json:"additional_attributes"`
}

type ChatwootContactInbox struct {
	ID       int    `json:"id"`
	SourceID string `json:"source_id"`
	InboxID  int    `json:"inbox_id"`
}

type ChatwootMessage struct {
	ID          int64                `json:"id"`
	Content     string               `json:"content"`
	MessageType int                  `json:"message_type"`
	Private     bool                 `json:"private"`
	Sender      *ChatwootSender      `json:"sender,omitempty"`
	Attachments []ChatwootAttachment `json:"attachments,omitempty"`
	Conversation *ChatwootConversationNested `json:"conversation,omitempty"`
}

type ChatwootConversationNested struct {
	ContactInbox struct {
		SourceID string `json:"source_id"`
	} `json:"contact_inbox"`
}

type ChatwootMeta struct {
	Sender   ChatwootContact `json:"sender"`
	Assignee *ChatwootSender `json:"assignee,omitempty"`
}

type ChatwootContact struct {
	ID          int    `json:"id"`
	Identifier  string `json:"identifier"`
	Name        string `json:"name"`
	PhoneNumber string `json:"phone_number"`
}

type ChatwootSender struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	Type  string `json:"type"`
}

type ChatwootInbox struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type ChatwootAttachment struct {
	ID       int64  `json:"id"`
	FileType string `json:"file_type"`
	DataURL  string `json:"data_url"`
}
