package whatsmiau

import (
	"context"
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

	// Monta os botões proto
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
			NativeFlowInfo: &waE2E.ButtonsMessage_Button_NativeFlowInfo{}, // obrigatório!
		})
	}

	if len(protoButtons) == 0 {
		return nil, fmt.Errorf("buttons: nenhum botão válido")
	}
	if len(protoButtons) > 3 {
		return nil, fmt.Errorf("buttons: máximo 3 botões, recebido %d", len(protoButtons))
	}

	// Monta ButtonsMessage
	buttonsMsg := &waE2E.ButtonsMessage{
		ContentText: proto.String(data.Description),
		FooterText:  proto.String(data.Footer),
		HeaderType:  waE2E.ButtonsMessage_EMPTY.Enum(),
		Buttons:     protoButtons,
		ContextInfo: contextInfo,
	}

	// Header de texto (title) — usa o oneof corretamente
	if strings.TrimSpace(data.Title) != "" {
		buttonsMsg.HeaderType = waE2E.ButtonsMessage_TEXT.Enum()
		buttonsMsg.Header = &waE2E.ButtonsMessage_Text{Text: data.Title}
	}

	// ✅ CRÍTICO: wrapper FutureProofMessage — sem isso não renderiza no celular
	msg := &waE2E.Message{
		DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				ButtonsMessage: buttonsMsg,
			},
		},
	}

	// ✅ CRÍTICO: AdditionalNodes — sem isso o servidor WhatsApp não processa
	extraNodes := []waBinary.Node{{
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
