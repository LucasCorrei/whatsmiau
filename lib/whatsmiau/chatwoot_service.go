package whatsmiau

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/verbeux-ai/whatsmiau/models"
	"go.uber.org/zap"
)

type Service struct {
	httpClient *http.Client
}

func NewService() *Service {
	return &Service{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ========================================
// EnsureInbox - Cria ou retorna inbox existente
// IMPORTANTE: Usa as configs DA INSTÂNCIA, não de variáveis de ambiente
// ========================================
func (s *Service) EnsureInbox(instance *models.Instance) (string, error) {
	// Validações
	if instance.ChatwootURL == "" {
		return "", fmt.Errorf("chatwoot URL not configured for instance %s", instance.ID)
	}
	if instance.ChatwootToken == "" {
		return "", fmt.Errorf("chatwoot token not configured for instance %s", instance.ID)
	}
	if instance.ChatwootAccountID == 0 {
		return "", fmt.Errorf("chatwoot account ID not configured for instance %s", instance.ID)
	}

	// Se já tem inbox ID, verificar se ainda existe
	if instance.ChatwootInboxID != "" {
		exists, err := s.checkInboxExists(instance)
		if err == nil && exists {
			zap.L().Info("inbox already exists",
				zap.String("instance", instance.ID),
				zap.String("inboxId", instance.ChatwootInboxID),
			)
			return instance.ChatwootInboxID, nil
		}
		zap.L().Warn("inbox not found, creating new one",
			zap.String("instance", instance.ID),
			zap.String("oldInboxId", instance.ChatwootInboxID),
		)
	}

	// Criar nova inbox
	return s.createInbox(instance)
}

// ========================================
// checkInboxExists - Verifica se inbox existe
// ========================================
func (s *Service) checkInboxExists(instance *models.Instance) (bool, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%d/inboxes/%s",
		instance.ChatwootURL,
		instance.ChatwootAccountID,
		instance.ChatwootInboxID,
	)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, err
	}

	req.Header.Set("api_access_token", instance.ChatwootToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}

// ========================================
// createInbox - Cria nova inbox no Chatwoot
// ========================================
func (s *Service) createInbox(instance *models.Instance) (string, error) {
	// Nome da inbox (usar o configurado ou gerar padrão)
	inboxName := instance.ChatwootNameInbox
	if inboxName == "" {
		inboxName = fmt.Sprintf("WhatsApp - %s", instance.ID)
	}

	// Payload para criar inbox
	payload := map[string]interface{}{
		"name":    inboxName,
		"channel": map[string]interface{}{
			"type":         "api",
			"webhook_url":  "", // Será configurado depois se necessário
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal inbox payload: %w", err)
	}

	// Endpoint para criar inbox
	url := fmt.Sprintf("%s/api/v1/accounts/%d/inboxes",
		instance.ChatwootURL,
		instance.ChatwootAccountID,
	)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Headers com token DA INSTÂNCIA
	req.Header.Set("api_access_token", instance.ChatwootToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("chatwoot API error: %d - %s", resp.StatusCode, string(body))
	}

	// Parse resposta
	var result struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	inboxID := fmt.Sprintf("%d", result.ID)

	zap.L().Info("chatwoot inbox created successfully",
		zap.String("instance", instance.ID),
		zap.String("inboxId", inboxID),
		zap.String("inboxName", result.Name),
		zap.String("chatwootUrl", instance.ChatwootURL),
	)

	return inboxID, nil
}

// ========================================
// CreateOrUpdateContact - Cria ou atualiza contato
// ========================================
func (s *Service) CreateOrUpdateContact(instance *models.Instance, phoneNumber, name string) (int, error) {
	// Endpoint para criar contato
	url := fmt.Sprintf("%s/api/v1/accounts/%d/contacts",
		instance.ChatwootURL,
		instance.ChatwootAccountID,
	)

	payload := map[string]interface{}{
		"inbox_id":     instance.ChatwootInboxID,
		"name":         name,
		"phone_number": phoneNumber,
	}

	jsonData, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	req.Header.Set("api_access_token", instance.ChatwootToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result struct {
		Payload struct {
			Contact struct {
				ID int `json:"id"`
			} `json:"contact"`
		} `json:"payload"`
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}

	return result.Payload.Contact.ID, nil
}

// ========================================
// CreateConversation - Cria conversa no Chatwoot
// ========================================
func (s *Service) CreateConversation(instance *models.Instance, contactID int, sourceID string) (int, error) {
	url := fmt.Sprintf("%s/api/v1/accounts/%d/conversations",
		instance.ChatwootURL,
		instance.ChatwootAccountID,
	)

	payload := map[string]interface{}{
		"source_id":  sourceID,
		"inbox_id":   instance.ChatwootInboxID,
		"contact_id": contactID,
		"status":     "open",
	}

	jsonData, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	req.Header.Set("api_access_token", instance.ChatwootToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result struct {
		ID int `json:"id"`
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}

	return result.ID, nil
}

// ========================================
// SendMessage - Envia mensagem para conversa
// ========================================
func (s *Service) SendMessage(instance *models.Instance, conversationID int, content string, messageType string) error {
	url := fmt.Sprintf("%s/api/v1/accounts/%d/conversations/%d/messages",
		instance.ChatwootURL,
		instance.ChatwootAccountID,
		conversationID,
	)

	payload := map[string]interface{}{
		"content":      content,
		"message_type": messageType, // incoming ou outgoing
		"private":      false,
	}

	jsonData, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	req.Header.Set("api_access_token", instance.ChatwootToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to send message: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}
