package whatsmiau

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"go.mau.fi/whatsmeow"
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

// SendText envia uma mensagem de texto com suporte a quoted (responder mensagens)
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
	ID          string `json:"id"`
	CopyCode    string `json:"copyCode"`
	URL         string `json:"url"`
	PhoneNumber string `json:"phoneNumber"`
	Currency    string `json:"currency"`
	Name        string `json:"name"`
	KeyType     string `json:"keyType"`
	Key         string `json:"key"`
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

	var msg *waE2E.Message
	var err error

	if allReply(data.Buttons) {
		msg, err = buildReplyButtonsMessage(data, contextInfo)
	} else {
		msg, err = buildInteractiveButtonsMessage(data, contextInfo)
	}
	if err != nil {
		return nil, err
	}

	res, err := client.SendMessage(ctx, *data.RemoteJID, msg)
	if err != nil {
		return nil, err
	}

	return &SendButtonsResponse{
		ID:        res.ID,
		CreatedAt: res.Timestamp,
	}, nil
}

func allReply(buttons []ButtonItem) bool {
	for _, b := range buttons {
		if b.Type != "reply" {
			return false
		}
	}
	return true
}

func buildReplyButtonsMessage(data *SendButtonsRequest, contextInfo *waE2E.ContextInfo) (*waE2E.Message, error) {
	if len(data.Buttons) > 3 {
		return nil, fmt.Errorf("buttons reply: máximo 3, recebido %d", len(data.Buttons))
	}

	protoButtons := make([]*waE2E.ButtonsMessage_Button, 0, len(data.Buttons))
	for i, b := range data.Buttons {
		id := b.ID
		if id == "" {
			id = fmt.Sprintf("btn_%d", i)
		}
		protoButtons = append(protoButtons, &waE2E.ButtonsMessage_Button{
			ButtonID: proto.String(id),
			ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{
				DisplayText: proto.String(b.DisplayText),
			},
			Type: waE2E.ButtonsMessage_Button_RESPONSE.Enum(),
		})
	}

	return &waE2E.Message{
		ButtonsMessage: &waE2E.ButtonsMessage{
			Header:      &waE2E.ButtonsMessage_Text{Text: data.Title},
			ContentText: proto.String(data.Description),
			FooterText:  proto.String(data.Footer),
			Buttons:     protoButtons,
			HeaderType:  waE2E.ButtonsMessage_TEXT.Enum(),
			ContextInfo: contextInfo,
		},
	}, nil
}

type nativeFlowParams struct {
	DisplayText string `json:"display_text"`
	URL         string `json:"url,omitempty"`
	MerchantURL string `json:"merchant_url,omitempty"`
	PhoneNumber string `json:"phone_number,omitempty"`
	CopyCode    string `json:"copy_code,omitempty"`
	Currency    string `json:"currency,omitempty"`
	Name        string `json:"name,omitempty"`
	KeyType     string `json:"key_type,omitempty"`
	Key         string `json:"key,omitempty"`
}

func buildInteractiveButtonsMessage(data *SendButtonsRequest, contextInfo *waE2E.ContextInfo) (*waE2E.Message, error) {
	nativeButtons := make([]*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton, 0, len(data.Buttons))

	for i, b := range data.Buttons {
		flowName, params, err := resolveNativeFlow(i, b)
		if err != nil {
			return nil, err
		}
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("buttons: serializar params: %w", err)
		}
		nativeButtons = append(nativeButtons, &waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
			Name:             proto.String(flowName),
			ButtonParamsJSON: proto.String(string(paramsJSON)),
		})
	}

	return &waE2E.Message{
		InteractiveMessage: &waE2E.InteractiveMessage{
			Header: &waE2E.InteractiveMessage_Header{
				Title: proto.String(data.Title),
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
		},
	}, nil
}

func resolveNativeFlow(idx int, b ButtonItem) (string, nativeFlowParams, error) {
	p := nativeFlowParams{DisplayText: b.DisplayText}

	switch b.Type {
	case "reply":
		return "quick_reply", p, nil
	case "url":
		if b.URL == "" {
			return "", p, fmt.Errorf("botão url[%d]: campo 'url' obrigatório", idx)
		}
		p.URL = b.URL
		p.MerchantURL = b.URL
		return "cta_url", p, nil
	case "call":
		if b.PhoneNumber == "" {
			return "", p, fmt.Errorf("botão call[%d]: campo 'phoneNumber' obrigatório", idx)
		}
		p.PhoneNumber = b.PhoneNumber
		return "cta_call", p, nil
	case "copy":
		if b.CopyCode == "" {
			return "", p, fmt.Errorf("botão copy[%d]: campo 'copyCode' obrigatório", idx)
		}
		p.CopyCode = b.CopyCode
		return "cta_copy", p, nil
	case "pix":
		if b.Key == "" {
			return "", p, fmt.Errorf("botão pix[%d]: campo 'key' obrigatório", idx)
		}
		p.Currency = b.Currency
		p.Name = b.Name
		p.KeyType = b.KeyType
		p.Key = b.Key
		return "payment_info", p, nil
	default:
		return "", p, fmt.Errorf("botão[%d]: tipo '%s' desconhecido", idx, b.Type)
	}
}
