package whatsmiau

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"math/rand"
	"time"

	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type SendText struct {
	Text           string     `json:"text"`
	InstanceID     string     `json:"instance_id"`
	RemoteJID      *types.JID `json:"remote_jid"`
	QuoteMessageID string     `json:"quote_message_id"`
	QuoteMessage   string     `json:"quote_message"`
	Participant    *types.JID `json:"participant"`
}

type SendTextResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Whatsmiau) SendText(ctx context.Context, data *SendText) (*SendTextResponse, error) {
	client, ok := s.clients.Load(data.InstanceID)
	if !ok {
		return nil, whatsmeow.ErrClientIsNil
	}

	var message *waE2E.Message

	contextInfo := BuildContextInfoWithQuoted(QuotedMessageParams{
		QuoteMessageID: data.QuoteMessageID,
		QuoteMessage:   data.QuoteMessage,
		RemoteJID:      data.RemoteJID,
	})

	if contextInfo != nil {
		message = &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text:        proto.String(data.Text),
				ContextInfo: contextInfo,
			},
		}
	} else {
		message = &waE2E.Message{
			Conversation: proto.String(data.Text),
		}
	}

	res, err := client.SendMessage(ctx, *data.RemoteJID, message)
	if err != nil {
		return nil, err
	}

	return &SendTextResponse{
		ID:        res.ID,
		CreatedAt: res.Timestamp,
	}, nil
}

type SendAudioRequest struct {
	AudioURL       string         `json:"text"`
	InstanceID     string         `json:"instance_id"`
	RemoteJID      *types.JID     `json:"remote_jid"`
	QuoteMessageID string         `json:"quote_message_id"`
	QuoteMessage   string         `json:"quote_message"`
	QuotedMessage  *waE2E.Message `json:"quoted_message,omitempty"`
}

type SendAudioResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Whatsmiau) SendAudio(ctx context.Context, data *SendAudioRequest) (*SendAudioResponse, error) {
	client, ok := s.clients.Load(data.InstanceID)
	if !ok {
		return nil, whatsmeow.ErrClientIsNil
	}

	resAudio, err := s.getCtx(ctx, data.AudioURL)
	if err != nil {
		return nil, err
	}

	dataBytes, err := io.ReadAll(resAudio.Body)
	if err != nil {
		return nil, err
	}

	audioData, waveForm, secs, err := convertAudio(dataBytes, 64)
	if err != nil {
		return nil, err
	}

	uploaded, err := client.Upload(ctx, audioData, whatsmeow.MediaAudio)
	if err != nil {
		return nil, err
	}

	audio := &waE2E.AudioMessage{
		URL:           proto.String(uploaded.URL),
		Mimetype:      proto.String("audio/ogg; codecs=opus"),
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    proto.Uint64(uploaded.FileLength),
		Seconds:       proto.Uint32(uint32(secs)),
		PTT:           proto.Bool(true),
		MediaKey:      uploaded.MediaKey,
		FileEncSHA256: uploaded.FileEncSHA256,
		DirectPath:    proto.String(uploaded.DirectPath),
		Waveform:      waveForm,
	}

	contextInfo := BuildContextInfoWithQuoted(QuotedMessageParams{
		QuoteMessageID: data.QuoteMessageID,
		QuoteMessage:   data.QuoteMessage,
		RemoteJID:      data.RemoteJID,
		QuotedMessage:  data.QuotedMessage,
	})

	if contextInfo != nil {
		audio.ContextInfo = contextInfo
	}

	res, err := client.SendMessage(ctx, *data.RemoteJID, &waE2E.Message{
		AudioMessage: audio,
	})
	if err != nil {
		return nil, err
	}

	return &SendAudioResponse{
		ID:        res.ID,
		CreatedAt: res.Timestamp,
	}, nil
}

type SendDocumentRequest struct {
	InstanceID     string         `json:"instance_id"`
	MediaURL       string         `json:"media_url"`
	Caption        string         `json:"caption"`
	FileName       string         `json:"file_name"`
	RemoteJID      *types.JID     `json:"remote_jid"`
	Mimetype       string         `json:"mimetype"`
	QuoteMessageID string         `json:"quote_message_id"`
	QuoteMessage   string         `json:"quote_message"`
	QuotedMessage  *waE2E.Message `json:"quoted_message,omitempty"`
}

type SendDocumentResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Whatsmiau) SendDocument(ctx context.Context, data *SendDocumentRequest) (*SendDocumentResponse, error) {
	client, ok := s.clients.Load(data.InstanceID)
	if !ok {
		return nil, whatsmeow.ErrClientIsNil
	}

	resMedia, err := s.getCtx(ctx, data.MediaURL)
	if err != nil {
		return nil, err
	}

	dataBytes, err := io.ReadAll(resMedia.Body)
	if err != nil {
		return nil, err
	}

	uploaded, err := client.Upload(ctx, dataBytes, whatsmeow.MediaDocument)
	if err != nil {
		return nil, err
	}

	doc := &waE2E.DocumentMessage{
		URL:           proto.String(uploaded.URL),
		Mimetype:      proto.String(data.Mimetype),
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    proto.Uint64(uploaded.FileLength),
		MediaKey:      uploaded.MediaKey,
		FileName:      proto.String(data.FileName),
		FileEncSHA256: uploaded.FileEncSHA256,
		DirectPath:    proto.String(uploaded.DirectPath),
		Caption:       proto.String(data.Caption),
	}

	contextInfo := BuildContextInfoWithQuoted(QuotedMessageParams{
		QuoteMessageID: data.QuoteMessageID,
		QuoteMessage:   data.QuoteMessage,
		RemoteJID:      data.RemoteJID,
		QuotedMessage:  data.QuotedMessage,
	})

	if contextInfo != nil {
		doc.ContextInfo = contextInfo
	}

	res, err := client.SendMessage(ctx, *data.RemoteJID, &waE2E.Message{
		DocumentMessage: doc,
	})
	if err != nil {
		return nil, err
	}

	return &SendDocumentResponse{
		ID:        res.ID,
		CreatedAt: res.Timestamp,
	}, nil
}

type SendImageRequest struct {
	InstanceID     string         `json:"instance_id"`
	MediaURL       string         `json:"media_url"`
	Caption        string         `json:"caption"`
	RemoteJID      *types.JID     `json:"remote_jid"`
	Mimetype       string         `json:"mimetype"`
	QuoteMessageID string         `json:"quote_message_id"`
	QuoteMessage   string         `json:"quote_message"`
	QuotedMessage  *waE2E.Message `json:"quoted_message,omitempty"`
}

type SendImageResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Whatsmiau) SendImage(ctx context.Context, data *SendImageRequest) (*SendImageResponse, error) {
	client, ok := s.clients.Load(data.InstanceID)
	if !ok {
		return nil, whatsmeow.ErrClientIsNil
	}

	resMedia, err := s.getCtx(ctx, data.MediaURL)
	if err != nil {
		return nil, err
	}

	dataBytes, err := io.ReadAll(resMedia.Body)
	if err != nil {
		return nil, err
	}

	uploaded, err := client.Upload(ctx, dataBytes, whatsmeow.MediaImage)
	if err != nil {
		return nil, err
	}

	if data.Mimetype == "" {
		data.Mimetype, err = extractMimetype(dataBytes, uploaded.URL)
		if err != nil {
			return nil, err
		}
	}

	img := &waE2E.ImageMessage{
		URL:           proto.String(uploaded.URL),
		Mimetype:      proto.String(data.Mimetype),
		Caption:       proto.String(data.Caption),
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    proto.Uint64(uploaded.FileLength),
		MediaKey:      uploaded.MediaKey,
		FileEncSHA256: uploaded.FileEncSHA256,
		DirectPath:    proto.String(uploaded.DirectPath),
	}

	contextInfo := BuildContextInfoWithQuoted(QuotedMessageParams{
		QuoteMessageID: data.QuoteMessageID,
		QuoteMessage:   data.QuoteMessage,
		RemoteJID:      data.RemoteJID,
		QuotedMessage:  data.QuotedMessage,
	})

	if contextInfo != nil {
		img.ContextInfo = contextInfo
	}

	res, err := client.SendMessage(ctx, *data.RemoteJID, &waE2E.Message{
		ImageMessage: img,
	})
	if err != nil {
		return nil, err
	}

	return &SendImageResponse{
		ID:        res.ID,
		CreatedAt: res.Timestamp,
	}, nil
}

type SendReactionRequest struct {
	InstanceID string     `json:"instance_id"`
	Reaction   string     `json:"reaction"`
	RemoteJID  *types.JID `json:"remote_jid"`
	MessageID  string     `json:"message_id"`
	FromMe     bool       `json:"from_me"`
}

type SendReactionResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Whatsmiau) SendReaction(ctx context.Context, data *SendReactionRequest) (*SendReactionResponse, error) {
	client, ok := s.clients.Load(data.InstanceID)
	if !ok {
		return nil, whatsmeow.ErrClientIsNil
	}

	if len(data.Reaction) <= 0 {
		return nil, fmt.Errorf("empty reaction, len: %d", len(data.Reaction))
	}

	if len(data.MessageID) <= 0 {
		return nil, fmt.Errorf("invalid message_id")
	}

	if client.Store == nil || client.Store.ID == nil {
		return nil, fmt.Errorf("device is not connected")
	}

	sender := data.RemoteJID
	if data.FromMe {
		sender = client.Store.ID
	}

	doc := client.BuildReaction(*data.RemoteJID, *sender, data.MessageID, data.Reaction)
	res, err := client.SendMessage(ctx, *data.RemoteJID, doc)
	if err != nil {
		return nil, err
	}

	return &SendReactionResponse{
		ID:        res.ID,
		CreatedAt: res.Timestamp,
	}, nil
}

// ── SendButtons ───────────────────────────────────────────────────────────────

type ButtonItem struct {
	Type        string `json:"type"`        // "reply" | "copy" | "url" | "call"
	DisplayText string `json:"displayText"`
	ID          string `json:"id"`          // reply
	CopyCode    string `json:"copyCode"`    // copy
	URL         string `json:"url"`         // url
	PhoneNumber string `json:"phoneNumber"` // call
	// campos legados — mantidos para compatibilidade com o controller
	Currency string `json:"currency"`
	Name     string `json:"name"`
	KeyType  string `json:"keyType"`
	Key      string `json:"key"`
}

type SendButtonsRequest struct {
	InstanceID     string       `json:"instance_id"`
	RemoteJID      *types.JID   `json:"remote_jid"`
	Title          string       `json:"title"`
	Description    string       `json:"description"`
	Footer         string       `json:"footer"`
	Buttons        []ButtonItem `json:"buttons"`
	QuoteMessageID string       `json:"quote_message_id"`
	QuoteMessage   string       `json:"quote_message"`
	Participant    *types.JID   `json:"participant"`
}

type SendButtonsResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// allReply retorna true se todos os botões são do tipo "reply"
func allReply(buttons []ButtonItem) bool {
	for _, b := range buttons {
		if b.Type != "reply" {
			return false
		}
	}
	return true
}

func (s *Whatsmiau) SendButtons(ctx context.Context, data *SendButtonsRequest) (*SendButtonsResponse, error) {
	client, ok := s.clients.Load(data.InstanceID)
	if !ok {
		return nil, whatsmeow.ErrClientIsNil
	}

	if len(data.Buttons) == 0 {
		return nil, fmt.Errorf("buttons: lista vazia")
	}

	contextInfo := BuildContextInfoWithQuoted(QuotedMessageParams{
		QuoteMessageID: data.QuoteMessageID,
		QuoteMessage:   data.QuoteMessage,
		RemoteJID:      data.RemoteJID,
		Participant:    data.Participant,
	})

	var (
		msg        *waE2E.Message
		extraNodes []waBinary.Node
		err        error
	)

	if allReply(data.Buttons) {
		// ── tipo "reply": ButtonsMessage + nodes native_flow ──────────────────
		msg, err = buildReplyButtons(data, contextInfo)
		if err != nil {
			return nil, err
		}
		extraNodes = []waBinary.Node{{
			Tag: "biz",
			Content: []waBinary.Node{{
				Tag: "interactive",
				Attrs: waBinary.Attrs{
					"type": "native_flow",
					"v":    "1",
				},
				Content: []waBinary.Node{{
					Tag: "native_flow",
					Attrs: waBinary.Attrs{
						"v":    "9",
						"name": "mixed",
					},
				}},
			}},
		}}
	} else {
		// ── tipos "copy", "url", "call": InteractiveMessage + nodes native_flow
		msg, err = buildInteractiveButtons(data, contextInfo)
		if err != nil {
			return nil, err
		}
		extraNodes = []waBinary.Node{{
			Tag: "biz",
			Content: []waBinary.Node{{
				Tag: "interactive",
				Attrs: waBinary.Attrs{
					"type": "native_flow",
					"v":    "1",
				},
				Content: []waBinary.Node{{
					Tag: "native_flow",
					Attrs: waBinary.Attrs{
						"v":    "9",
						"name": "mixed",
					},
				}},
			}},
		}}
	}

	res, err := client.SendMessage(ctx, *data.RemoteJID, msg, whatsmeow.SendRequestExtra{
		AdditionalNodes: &extraNodes,
	})
	if err != nil {
		return nil, err
	}

	return &SendButtonsResponse{
		ID:        res.ID,
		CreatedAt: res.Timestamp,
	}, nil
}

// ── ButtonsMessage — tipo "reply" ─────────────────────────────────────────────

func buildReplyButtons(data *SendButtonsRequest, contextInfo *waE2E.ContextInfo) (*waE2E.Message, error) {
	if len(data.Buttons) > 3 {
		return nil, fmt.Errorf("buttons reply: máximo 3, recebido %d", len(data.Buttons))
	}

	protoButtons := make([]*waE2E.ButtonsMessage_Button, 0, len(data.Buttons))
	for i, b := range data.Buttons {
		title := strings.TrimSpace(b.DisplayText)
		if title == "" {
			continue
		}
		id := strings.TrimSpace(b.ID)
		if id == "" {
			id = fmt.Sprintf("btn_%d", i)
		}
		protoButtons = append(protoButtons, &waE2E.ButtonsMessage_Button{
			ButtonID: proto.String(id),
			ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{
				DisplayText: proto.String(title),
			},
			Type:           waE2E.ButtonsMessage_Button_RESPONSE.Enum(),
			NativeFlowInfo: &waE2E.ButtonsMessage_Button_NativeFlowInfo{},
		})
	}

	if len(protoButtons) == 0 {
		return nil, fmt.Errorf("buttons: nenhum botão válido")
	}

	buttonsMsg := &waE2E.ButtonsMessage{
		ContentText: proto.String(data.Description),
		FooterText:  proto.String(data.Footer),
		HeaderType:  waE2E.ButtonsMessage_EMPTY.Enum(),
		Buttons:     protoButtons,
		ContextInfo: contextInfo,
	}

	if strings.TrimSpace(data.Title) != "" {
		buttonsMsg.HeaderType = waE2E.ButtonsMessage_TEXT.Enum()
		buttonsMsg.Header = &waE2E.ButtonsMessage_Text{Text: data.Title}
	}

	// FutureProofMessage wrapper — obrigatório para renderizar no celular
	return &waE2E.Message{
		DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				ButtonsMessage: buttonsMsg,
			},
		},
	}, nil
}

// ── InteractiveMessage — tipos "copy", "url", "call" ─────────────────────────

// Params para cada tipo de NativeFlow
type nativeFlowCopy struct {
	DisplayText string `json:"display_text"`
	CopyCode    string `json:"copy_code"`
}

type nativeFlowURL struct {
	DisplayText string `json:"display_text"`
	URL         string `json:"url"`
	MerchantURL string `json:"merchant_url"`
}

type nativeFlowCall struct {
	DisplayText string `json:"display_text"`
	PhoneNumber string `json:"phone_number"`
}


// ── Structs para NativeFlow copy/url/call ─────────────────────────────────────

type nativeFlowCopy struct {
	DisplayText string `json:"display_text"`
	CopyCode    string `json:"copy_code"`
}

type nativeFlowURL struct {
	DisplayText string `json:"display_text"`
	URL         string `json:"url"`
	MerchantURL string `json:"merchant_url"`
}

type nativeFlowCall struct {
	DisplayText string `json:"display_text"`
	PhoneNumber string `json:"phone_number"`
}

// ── Structs para PIX ──────────────────────────────────────────────────────────

type pixStaticCode struct {
	MerchantName string `json:"merchant_name"`
	Key          string `json:"key"`
	KeyType      string `json:"key_type"` // PHONE, EMAIL, CPF, CNPJ, EVP
}

type pixPaymentSetting struct {
	Type          string        `json:"type"` // "pix_static_code"
	PixStaticCode pixStaticCode `json:"pix_static_code"`
}

type pixOrderItem struct {
	Name       string         `json:"name"`
	Amount     map[string]any `json:"amount"`
	Quantity   int            `json:"quantity"`
	SaleAmount map[string]any `json:"sale_amount"`
}

type pixOrder struct {
	Status    string         `json:"status"`    // "pending"
	Subtotal  map[string]any `json:"subtotal"`
	OrderType string         `json:"order_type"` // "ORDER"
	Items     []pixOrderItem `json:"items"`
}

// nativeFlowPix — payload completo conforme protocolo WhatsApp
type nativeFlowPix struct {
	Currency        string              `json:"currency"`        // "BRL"
	TotalAmount     map[string]any      `json:"total_amount"`
	ReferenceID     string              `json:"reference_id"`
	Type            string              `json:"type"`            // "physical-goods"
	Order           pixOrder            `json:"order"`
	PaymentSettings []pixPaymentSetting `json:"payment_settings"`
}

// pixKeyTypeToUpper converte keyType do usuário → formato uppercase WhatsApp
// random → EVP, phone → PHONE, email → EMAIL, cpf → CPF, cnpj → CNPJ
func pixKeyTypeToUpper(keyType string) string {
	switch strings.ToLower(strings.TrimSpace(keyType)) {
	case "random", "evp", "aleatorio", "aleatório":
		return "EVP"
	case "phone", "telefone", "celular":
		return "PHONE"
	case "email":
		return "EMAIL"
	case "cpf":
		return "CPF"
	case "cnpj":
		return "CNPJ"
	default:
		return strings.ToUpper(keyType)
	}
}

// generateReferenceID gera um ID aleatório de 11 chars (A-Z0-9)
func generateReferenceID() string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, 11)
	for i := range b {
		b[i] = chars[r.Intn(len(chars))]
	}
	return string(b)
}

func buildInteractiveButtons(data *SendButtonsRequest, contextInfo *waE2E.ContextInfo) (*waE2E.Message, error) {
	nativeButtons := make([]*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton, 0, len(data.Buttons))

	hasPix := false

	for i, b := range data.Buttons {
		displayText := strings.TrimSpace(b.DisplayText)

		var flowName string
		var paramsData any

		switch b.Type {
		case "copy":
			if displayText == "" {
				displayText = "Copiar"
			}
			if strings.TrimSpace(b.CopyCode) == "" {
				return nil, fmt.Errorf("botão copy[%d]: campo 'copyCode' obrigatório", i)
			}
			flowName = "cta_copy"
			paramsData = nativeFlowCopy{
				DisplayText: displayText,
				CopyCode:    b.CopyCode,
			}
		case "url":
			if displayText == "" {
				displayText = "Abrir link"
			}
			if strings.TrimSpace(b.URL) == "" {
				return nil, fmt.Errorf("botão url[%d]: campo 'url' obrigatório", i)
			}
			flowName = "cta_url"
			paramsData = nativeFlowURL{
				DisplayText: displayText,
				URL:         b.URL,
				MerchantURL: b.URL,
			}
		case "call":
			if displayText == "" {
				displayText = "Ligar"
			}
			if strings.TrimSpace(b.PhoneNumber) == "" {
				return nil, fmt.Errorf("botão call[%d]: campo 'phoneNumber' obrigatório", i)
			}
			flowName = "cta_call"
			paramsData = nativeFlowCall{
				DisplayText: displayText,
				PhoneNumber: b.PhoneNumber,
			}
		case "pix":
			hasPix = true
			if strings.TrimSpace(b.Key) == "" {
				return nil, fmt.Errorf("botão pix[%d]: campo 'key' obrigatório", i)
			}
			if strings.TrimSpace(b.KeyType) == "" {
				return nil, fmt.Errorf("botão pix[%d]: campo 'keyType' obrigatório (phone, email, cpf, cnpj, random)", i)
			}
			flowName = "payment_info"
			paramsData = nativeFlowPix{
				Currency: "BRL",
				TotalAmount: map[string]any{
					"value":  0,
					"offset": 100,
				},
				ReferenceID: generateReferenceID(),
				Type:        "physical-goods",
				Order: pixOrder{
					Status: "pending",
					Subtotal: map[string]any{
						"value":  0,
						"offset": 100,
					},
					OrderType: "ORDER",
					Items: []pixOrderItem{{
						Name: b.Name,
						Amount: map[string]any{
							"value":  0,
							"offset": 100,
						},
						Quantity: 0,
						SaleAmount: map[string]any{
							"value":  0,
							"offset": 100,
						},
					}},
				},
				PaymentSettings: []pixPaymentSetting{{
					Type: "pix_static_code",
					PixStaticCode: pixStaticCode{
						MerchantName: b.Name,
						Key:          b.Key,
						KeyType:      pixKeyTypeToUpper(b.KeyType),
					},
				}},
			}
		default:
			return nil, fmt.Errorf("botão[%d]: tipo '%s' não suportado", i, b.Type)
		}

		paramsJSON, err := json.Marshal(paramsData)
		if err != nil {
			return nil, fmt.Errorf("botão[%d]: erro ao serializar params: %w", i, err)
		}

		nativeButtons = append(nativeButtons, &waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
			Name:             proto.String(flowName),
			ButtonParamsJSON: proto.String(string(paramsJSON)),
		})
	}

	if len(nativeButtons) == 0 {
		return nil, fmt.Errorf("buttons: nenhum botão válido")
	}

	// messageParamsJSON obrigatório para PIX renderizar
	messageParamsJSON := fmt.Sprintf(`{"from":"api","templateId":"%s"}`, generateReferenceID())

	interactiveMsg := &waE2E.InteractiveMessage{
		Body: &waE2E.InteractiveMessage_Body{
			Text: proto.String(data.Description),
		},
		Footer: &waE2E.InteractiveMessage_Footer{
			Text: proto.String(data.Footer),
		},
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
				Buttons:           nativeButtons,
				MessageVersion:    proto.Int32(1),
				MessageParamsJSON: proto.String(messageParamsJSON),
			},
		},
		ContextInfo: contextInfo,
	}

	if strings.TrimSpace(data.Title) != "" {
		interactiveMsg.Header = &waE2E.InteractiveMessage_Header{
			Title: proto.String(data.Title),
		}
	}

	innerMsg := &waE2E.Message{
		InteractiveMessage: interactiveMsg,
	}

	// PIX precisa de viewOnceMessage como wrapper — copy/url/call usam FutureProofMessage
	if hasPix {
		return &waE2E.Message{
			ViewOnceMessage: &waE2E.FutureProofMessage{
				Message: innerMsg,
			},
		}, nil
	}

	return &waE2E.Message{
		DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
			Message: innerMsg,
		},
	}, nil
}
