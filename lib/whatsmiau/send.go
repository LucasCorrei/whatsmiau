package whatsmiau

import (
	"context"
	"fmt"
	"io"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	//"go.mau.fi/whatsmeow/proto/waCommon"
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

	// 🎯 USAR O HELPER
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
	QuotedMessage  *waE2E.Message `json:"quoted_message,omitempty"` // Opcional: mensagem quotada completa
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

	// Baixar o áudio da URL
	resAudio, err := s.getCtx(ctx, data.AudioURL)
	if err != nil {
		return nil, err
	}

	dataBytes, err := io.ReadAll(resAudio.Body)
	if err != nil {
		return nil, err
	}

	// Converter o áudio
	audioData, waveForm, secs, err := convertAudio(dataBytes, 64)
	if err != nil {
		return nil, err
	}

	// Upload do áudio
	uploaded, err := client.Upload(ctx, audioData, whatsmeow.MediaAudio)
	if err != nil {
		return nil, err
	}

	// Criar a mensagem de áudio
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

	// Adicionar suporte a quoted message usando o helper
	contextInfo := BuildContextInfoWithQuoted(QuotedMessageParams{
		QuoteMessageID: data.QuoteMessageID,
		QuoteMessage:   data.QuoteMessage,
		RemoteJID:      data.RemoteJID,
		QuotedMessage:  data.QuotedMessage,
	})

	if contextInfo != nil {
		audio.ContextInfo = contextInfo
	}

	// Enviar a mensagem
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

	// Adicionar suporte a quoted message
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

	// Adicionar suporte a quoted message
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

// =============================================================================
// PAYMENT (cobrança com "Revisar e pagar")
// =============================================================================

// PaymentItem representa um item dentro de uma cobrança.
type PaymentItem struct {
	Name     string `json:"name"`     // ex: "Teste"
	Quantity uint32 `json:"quantity"` // ex: 1
	Amount   int64  `json:"amount"`   // preço unitário em centavos, ex: 100 = R$1,00
}

// SendPaymentRequest envia uma cobrança no formato "Revisar e pagar" (OrderMessage).
// Use quando o JSON recebido tiver OrderID, TotalAmount e Items — caso contrário
// use o fluxo Pix simples (chave + "Copiar chave Pix").
type SendPaymentRequest struct {
	InstanceID     string         `json:"instance_id"`
	RemoteJID      *types.JID     `json:"remote_jid"`
	QuoteMessageID string         `json:"quote_message_id"`
	QuoteMessage   string         `json:"quote_message"`
	QuotedMessage  *waE2E.Message `json:"quoted_message,omitempty"`

	// Dados da cobrança
	OrderID     string         `json:"order_id"`     // ex: "RAP1773090286620"
	Currency    string         `json:"currency"`     // ex: "BRL" (padrão se vazio)
	TotalAmount int64          `json:"total_amount"` // em centavos, ex: 100 = R$1,00
	Items       []*PaymentItem `json:"items"`

	// Dados do vendedor (exibidos no cabeçalho do card)
	SellerName  string `json:"seller_name"`  // ex: "Joseph Fernandes"
	SellerPhone string `json:"seller_phone"` // ex: "+55 87 8114-8453"
}

// SendPaymentResponse retorna o ID e timestamp da mensagem enviada.
type SendPaymentResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// SendPayment envia uma cobrança que renderiza o card "Revisar e pagar" no WhatsApp.
// Internamente usa waE2E.OrderMessage — o mesmo tipo utilizado pelo WhatsApp Business.
//
// Auto-detecção sugerida no handler:
//
//	if IsPaymentRequest(req.OrderID, req.TotalAmount, req.Items) {
//	    wm.SendPayment(ctx, req)
//	} else {
//	    wm.SendPix(ctx, pixReq)
//	}
func (s *Whatsmiau) SendPayment(ctx context.Context, data *SendPaymentRequest) (*SendPaymentResponse, error) {
	client, ok := s.clients.Load(data.InstanceID)
	if !ok {
		return nil, whatsmeow.ErrClientIsNil
	}

	if data.Currency == "" {
		data.Currency = "BRL"
	}

	if data.OrderID == "" {
		return nil, fmt.Errorf("order_id is required for payment messages")
	}

	if data.TotalAmount <= 0 {
		return nil, fmt.Errorf("total_amount must be greater than zero")
	}

	// Montar itens do pedido
	orderItems := make([]*waE2E.OrderMessage_Item, 0, len(data.Items))
	for i, item := range data.Items {
		orderItems = append(orderItems, &waE2E.OrderMessage_Item{
			CustomItemID: proto.String(fmt.Sprintf("%s_item_%d", data.OrderID, i)),
			Name:         proto.String(item.Name),
			Amount: &waE2E.Money{
				Value:        proto.Int64(item.Amount),
				Offset:       proto.Uint32(100), // 2 casas decimais
				CurrencyCode: proto.String(data.Currency),
			},
			Quantity: proto.Uint32(item.Quantity),
		})
	}

	orderMsg := &waE2E.OrderMessage{
		OrderID: proto.String(data.OrderID),
		Total: &waE2E.Money{
			Value:        proto.Int64(data.TotalAmount),
			Offset:       proto.Uint32(100),
			CurrencyCode: proto.String(data.Currency),
		},
		SellerJID:  proto.String(data.SellerPhone),
		Items:      orderItems,
		Message:    proto.String(fmt.Sprintf("%s %s", data.SellerName, data.SellerPhone)),
		OrderTitle: proto.String(fmt.Sprintf("Nº da cobrança: %s", data.OrderID)),
		// INQUIRY = pagamento pendente (renderiza "Revisar e pagar")
		Status: waE2E.OrderMessage_INQUIRY.Enum(),
	}

	// Suporte a quoted message
	contextInfo := BuildContextInfoWithQuoted(QuotedMessageParams{
		QuoteMessageID: data.QuoteMessageID,
		QuoteMessage:   data.QuoteMessage,
		RemoteJID:      data.RemoteJID,
		QuotedMessage:  data.QuotedMessage,
	})
	if contextInfo != nil {
		orderMsg.ContextInfo = contextInfo
	}

	res, err := client.SendMessage(ctx, *data.RemoteJID, &waE2E.Message{
		OrderMessage: orderMsg,
	})
	if err != nil {
		return nil, err
	}

	return &SendPaymentResponse{
		ID:        res.ID,
		CreatedAt: res.Timestamp,
	}, nil
}

// IsPaymentRequest retorna true quando os dados recebidos têm informação suficiente
// para enviar uma cobrança (Payment) em vez de um Pix simples.
//
// Critérios: OrderID preenchido + TotalAmount > 0 + pelo menos 1 item.
func IsPaymentRequest(orderID string, totalAmount int64, items []*PaymentItem) bool {
	return orderID != "" && totalAmount > 0 && len(items) > 0
}
