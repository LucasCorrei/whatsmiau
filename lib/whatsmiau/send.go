package whatsmiau

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
	Type        string `json:"type"`        // "reply" | "copy" | "url" | "call" | "pix"
	DisplayText string `json:"displayText"`
	ID          string `json:"id"`          // reply
	CopyCode    string `json:"copyCode"`    // copy
	URL         string `json:"url"`         // url
	PhoneNumber string `json:"phoneNumber"` // call
	// pix
	Currency string `json:"currency"` // default: "BRL"
	Name     string `json:"name"`     // merchant name
	KeyType  string `json:"keyType"`  // phone, email, cpf, cnpj, random
	Key      string `json:"key"`      // pix key
	Amount   int    `json:"amount"`   // valor em centavos (ex: 1000 = R$10,00)
	ItemName string `json:"itemName"` // nome do item/produto
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

	// PIX usa caminho completamente diferente: InteractiveMessage direto +
	// extra node flat biz(native_flow_name="payment_info")
	if len(data.Buttons) == 1 && data.Buttons[0].Type == "pix" {
		msg, err = buildPixButton(data, contextInfo)
		if err != nil {
			return nil, err
		}
		extraNodes = []waBinary.Node{{
			Tag: "biz",
			Attrs: waBinary.Attrs{
				"native_flow_name": "payment_info",
			},
		}}
	} else if allReply(data.Buttons) {
		// tipo "reply": ButtonsMessage + nodes nested native_flow
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
		// tipos "copy", "url", "call": InteractiveMessage + nodes nested native_flow
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


// ── Structs NativeFlow copy/url/call ──────────────────────────────────────────

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

// ── buildPixButton — InteractiveMessage direto, sem wrapper ───────────────────

func buildPixButton(data *SendButtonsRequest, contextInfo *waE2E.ContextInfo) (*waE2E.Message, error) {
	b := data.Buttons[0]

	if strings.TrimSpace(b.Key) == "" {
		return nil, fmt.Errorf("pix: campo 'key' obrigatório")
	}
	if strings.TrimSpace(b.KeyType) == "" {
		return nil, fmt.Errorf("pix: campo 'keyType' obrigatório (phone, email, cpf, cnpj, random)")
	}
	if strings.TrimSpace(b.Name) == "" {
		return nil, fmt.Errorf("pix: campo 'name' (nome do recebedor) obrigatório")
	}

	currency := strings.TrimSpace(b.Currency)
	if currency == "" {
		currency = "BRL"
	}
	displayText := strings.TrimSpace(b.DisplayText)
	if displayText == "" {
		displayText = "Pagar com PIX"
	}
	itemName := strings.TrimSpace(b.ItemName)
	if itemName == "" {
		itemName = b.Name
	}
	referenceID := fmt.Sprintf("PIX%d", time.Now().UnixMilli())

	// Payload conforme protocolo WhatsApp — campos obrigatórios
	buttonParams := map[string]interface{}{
		"display_text": displayText,
		"currency":     currency,
		"total_amount": map[string]interface{}{
			"value":  b.Amount,
			"offset": 100,
		},
		"reference_id": referenceID,
		"type":         "physical-goods",
		"order": map[string]interface{}{
			"status": "pending",
			"subtotal": map[string]interface{}{
				"value":  b.Amount,
				"offset": 100,
			},
			"order_type": "ORDER",
			"items": []map[string]interface{}{
				{
					"retailer_id": "0",
					"product_id":  "0",
					"name":        itemName,
					"amount": map[string]interface{}{
						"value":  b.Amount,
						"offset": 100,
					},
					"quantity": 1,
				},
			},
		},
		"payment_settings": []map[string]interface{}{
			{
				"type": "pix_static_code",
				"pix_static_code": map[string]interface{}{
					"merchant_name": b.Name,
					"key":           b.Key,
					"key_type":      pixKeyTypeToUpper(b.KeyType),
				},
			},
		},
	}

	buttonParamsJSON, err := json.Marshal(buttonParams)
	if err != nil {
		return nil, fmt.Errorf("pix: erro ao serializar params: %w", err)
	}

	interactiveMsg := &waE2E.InteractiveMessage{
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
				MessageVersion: proto.Int32(1),
				Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
					{
						Name:             proto.String("payment_info"),
						ButtonParamsJSON: proto.String(string(buttonParamsJSON)),
					},
				},
			},
		},
		ContextInfo: contextInfo,
	}

	// PIX: InteractiveMessage DIRETO no Message — sem FutureProofMessage wrapper
	return &waE2E.Message{
		InteractiveMessage: interactiveMsg,
	}, nil
}

// ── pixKeyTypeToUpper ─────────────────────────────────────────────────────────

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

// ── buildInteractiveButtons — copy, url, call ─────────────────────────────────

func buildInteractiveButtons(data *SendButtonsRequest, contextInfo *waE2E.ContextInfo) (*waE2E.Message, error) {
	nativeButtons := make([]*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton, 0, len(data.Buttons))

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

	interactiveMsg := &waE2E.InteractiveMessage{
  	  Header: &waE2E.InteractiveMessage_Header{
 	       Title: proto.String(data.Title),
	        HasMediaAttachment: proto.Bool(false),
 	   },
  	  Body: &waE2E.InteractiveMessage_Body{
  	      Text: proto.String(data.Description),
 	   },
   	 Footer: &waE2E.InteractiveMessage_Footer{
   	     Text: proto.String(data.Footer),
  	  },
 	   InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
   	     NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
    	        Buttons:        nativeButtons,
    	        MessageVersion: proto.Int32(1),
     	   },
  	  },
  	  ContextInfo: contextInfo,
	}

	if strings.TrimSpace(data.Title) != "" {
		interactiveMsg.Header = &waE2E.InteractiveMessage_Header{
			Title: proto.String(data.Title),
		}
	}

	return &waE2E.Message{
    	InteractiveMessage: interactiveMsg,
	}, nil
}
