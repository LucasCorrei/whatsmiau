package whatsmiau

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/verbeux-ai/whatsmiau/models"
	"go.uber.org/zap"
)

type ChatwootService struct {
	BaseURL    string
	AccountID  int
	Token      string
	InboxID    int
	HttpClient *http.Client
}

func NewChatwootService(baseURL string, accountID int, token string, inboxID int) *ChatwootService {
	return &ChatwootService{
		BaseURL:    baseURL,
		AccountID:  accountID,
		Token:      token,
		InboxID:    inboxID,
		HttpClient: &http.Client{},
	}
}

func extractPhone(remoteJid string) string {
	if remoteJid == "" {
		return ""
	}

	if strings.Contains(remoteJid, "@") {
		remoteJid = strings.Split(remoteJid, "@")[0]
	}

	if strings.Contains(remoteJid, "-") {
		remoteJid = strings.Split(remoteJid, "-")[0]
	}

	return remoteJid
}

func normalizePhone(phone string) string {

	if phone == "" {
		return ""
	}

	if strings.Contains(phone, "-") {
		phone = strings.Split(phone, "-")[0]
	}

	re := regexp.MustCompile(`\D`)
	phone = re.ReplaceAllString(phone, "")

	phone = strings.TrimLeft(phone, "0")

	return phone
}

func (c *ChatwootService) HandleMessage(ctx context.Context, instance models.Instance, remoteJid string, text string) {

	phone := extractPhone(remoteJid)
	phone = normalizePhone(phone)

	if phone == "" {
		return
	}

	name := phone
	identifier := phone

	contactID, err := c.findOrCreateContact(ctx, phone, name, identifier)
	if err != nil {
		zap.L().Error("failed to find/create contact", zap.Error(err))
		return
	}

	conversationID, err := c.findOrCreateConversation(ctx, contactID)
	if err != nil {
		zap.L().Error("failed to find/create conversation", zap.Error(err))
		return
	}

	err = c.sendMessage(ctx, conversationID, text)
	if err != nil {
		zap.L().Error("failed to send message", zap.Error(err))
	}
}

func (c *ChatwootService) findOrCreateContact(ctx context.Context, phone, name, identifier string) (int, error) {

	phone = normalizePhone(phone)

	if id, err := c.searchContact(ctx, phone); err == nil && id > 0 {
		return id, nil
	}

	id, err := c.createContact(ctx, phone, name, identifier)
	if err != nil {

		if strings.Contains(err.Error(), "taken") {

			id, err2 := c.searchContact(ctx, phone)
			if err2 == nil && id > 0 {
				return id, nil
			}
		}

		return 0, err
	}

	return id, nil
}

func (c *ChatwootService) searchContact(ctx context.Context, phone string) (int, error) {

	url := fmt.Sprintf("%s/api/v1/accounts/%d/contacts/search", c.BaseURL, c.AccountID)

	body := map[string]interface{}{
		"query": map[string]interface{}{
			"attribute_key": "phone_number",
			"filter_operator": "equal_to",
			"values": []string{
				fmt.Sprintf("+%s", normalizePhone(phone)),
			},
		},
	}

	data, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api_access_token", c.Token)

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return 0, err
	}

	defer resp.Body.Close()

	bodyResp, _ := io.ReadAll(resp.Body)

	var result struct {
		Payload []struct {
			ID int `json:"id"`
		} `json:"payload"`
	}

	err = json.Unmarshal(bodyResp, &result)
	if err != nil {
		return 0, err
	}

	if len(result.Payload) > 0 {
		return result.Payload[0].ID, nil
	}

	return 0, fmt.Errorf("contact not found")
}

func (c *ChatwootService) createContact(ctx context.Context, phone, name, identifier string) (int, error) {

	url := fmt.Sprintf("%s/api/v1/accounts/%d/contacts", c.BaseURL, c.AccountID)

	body := map[string]interface{}{
		"name":        name,
		"identifier":  identifier,
		"phone_number": fmt.Sprintf("+%s", normalizePhone(phone)),
	}

	data, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api_access_token", c.Token)

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return 0, err
	}

	defer resp.Body.Close()

	bodyResp, _ := io.ReadAll(resp.Body)

	var result struct {
		Payload struct {
			Contact struct {
				ID int `json:"id"`
			} `json:"contact"`
		} `json:"payload"`
	}

	err = json.Unmarshal(bodyResp, &result)
	if err != nil {
		return 0, err
	}

	return result.Payload.Contact.ID, nil
}

func (c *ChatwootService) findOrCreateConversation(ctx context.Context, contactID int) (int, error) {

	url := fmt.Sprintf("%s/api/v1/accounts/%d/conversations", c.BaseURL, c.AccountID)

	body := map[string]interface{}{
		"inbox_id":   c.InboxID,
		"contact_id": contactID,
	}

	data, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api_access_token", c.Token)

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return 0, err
	}

	defer resp.Body.Close()

	bodyResp, _ := io.ReadAll(resp.Body)

	var result struct {
		ID int `json:"id"`
	}

	err = json.Unmarshal(bodyResp, &result)
	if err != nil {
		return 0, err
	}

	return result.ID, nil
}

func (c *ChatwootService) sendMessage(ctx context.Context, conversationID int, text string) error {

	url := fmt.Sprintf("%s/api/v1/accounts/%d/conversations/%d/messages", c.BaseURL, c.AccountID, conversationID)

	body := map[string]interface{}{
		"content": text,
		"message_type": "incoming",
	}

	data, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api_access_token", c.Token)

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	return nil
}
