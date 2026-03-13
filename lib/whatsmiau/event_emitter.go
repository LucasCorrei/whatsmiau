package whatsmiau

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	goredis "github.com/go-redis/redis/v8"
	"github.com/emersion/go-vcard"
	"github.com/google/uuid"
	"github.com/verbeux-ai/whatsmiau/models"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.uber.org/zap"
	"golang.org/x/net/context"
)

// chatwootDefaultEvents são os eventos sempre escutados quando Chatwoot está configurado
var chatwootDefaultEvents = map[string]bool{
	"MESSAGES_UPSERT": true,
	"MESSAGES_UPDATE": true,
	"CONTACTS_UPSERT": true,
}

type emitter struct {
	url  string
	data any
}

func (s *Whatsmiau) getInstance(id string) *models.Instance {
	ctx, c := context.WithTimeout(context.Background(), time.Second*5)
	defer c()

	res, err := s.repo.List(ctx, id)
	if err != nil {
		zap.L().Panic("failed to get instanceCached by instance", zap.Error(err))
	}

	if len(res) == 0 {
		zap.L().Warn("no instanceCached found by instance", zap.String("instance", id))
		return nil
	}

	s.instanceCache.Store(id, res[0])

	return &res[0]
}

func (s *Whatsmiau) getInstanceCached(id string) *models.Instance {
	instanceCached, ok := s.instanceCache.Load(id)
	if ok {
		return &instanceCached
	}

	ctx, c := context.WithTimeout(context.Background(), time.Second*5)
	defer c()

	res, err := s.repo.List(ctx, id)
	if err != nil {
		zap.L().Panic("failed to get instanceCached by instance", zap.Error(err))
	}

	if len(res) == 0 {
		zap.L().Warn("no instanceCached found by instance", zap.String("instance", id))
		return nil
	}

	s.instanceCache.Store(id, res[0])
	go func() {
		// expires in 10sec
		time.Sleep(time.Second * 10)
		s.instanceCache.Delete(id)
	}()

	return &res[0]
}

func (s *Whatsmiau) startEmitter() {
	for event := range s.emitter {
		data, err := json.Marshal(event.data)
		if err != nil {
			zap.L().Error("failed to marshal event", zap.Error(err))
			return
		}

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, event.url, bytes.NewReader(data))
		if err != nil {
			zap.L().Error("failed to create request", zap.Error(err))
			return
		}

		req.Header.Set("Content-Type", "application/json")
		resp, err := s.httpClient.Do(req)
		if err != nil {
			zap.L().Error("failed to send request", zap.Error(err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			res, err := io.ReadAll(resp.Body)
			if err != nil {
				zap.L().Error("failed to read response body", zap.Error(err))
			} else {
				zap.L().Error("error doing request", zap.Any("response", string(res)), zap.String("url", event.url))
			}
		}
	}
}

func (s *Whatsmiau) emit(body any, url string) {
	s.emitter <- emitter{url, body}
}

// hasChatwoot retorna true se a instância tem Chatwoot configurado
func hasChatwoot(instance *models.Instance) bool {
	return instance.ChatwootAccountID != "" &&
		instance.ChatwootToken != "" &&
		instance.ChatwootURL != ""
}

// buildWebhookEventMap constrói o mapa de eventos do webhook.
// Se o webhook for nil ou não tiver eventos, retorna mapa vazio.
func buildWebhookEventMap(instance *models.Instance) map[string]bool {
	eventMap := make(map[string]bool)
	if instance.Webhook == nil {
		return eventMap
	}
	for _, event := range instance.Webhook.Events {
		eventMap[event] = true
	}
	return eventMap
}

func (s *Whatsmiau) Handle(id string) whatsmeow.EventHandler {
	return func(evt any) {
		s.handlerSemaphore <- struct{}{}
		go func() {
			defer func() { <-s.handlerSemaphore }()
			instance := s.getInstanceCached(id)
			if instance == nil {
				zap.L().Warn("no instance found for event", zap.String("instance", id))
				return
			}

			// Mapa de eventos do webhook (vazio se webhook não configurado)
			webhookEventMap := buildWebhookEventMap(instance)

			// Mapa de eventos do Chatwoot (fixo quando configurado)
			chatwootEventMap := make(map[string]bool)
			if hasChatwoot(instance) {
				chatwootEventMap = chatwootDefaultEvents
			}

			switch e := evt.(type) {
			case *events.LoggedOut:
				s.handleLoggedOut(id)
			case *events.Connected:
				s.handleConnectedEvent(id, instance)
			case *events.Message:
				s.handleMessageEvent(id, instance, e, webhookEventMap, chatwootEventMap)
			case *events.Receipt:
				s.handleReceiptEvent(id, instance, e, webhookEventMap, chatwootEventMap)
			case *events.BusinessName:
				s.handleBusinessNameEvent(id, instance, e, webhookEventMap, chatwootEventMap)
			case *events.Contact:
				s.handleContactEvent(id, instance, e, webhookEventMap, chatwootEventMap)
			case *events.Picture:
				s.handlePictureEvent(id, instance, e, webhookEventMap, chatwootEventMap)
			case *events.HistorySync:
				s.handleHistorySyncEvent(id, instance, e, webhookEventMap, chatwootEventMap)
			case *events.GroupInfo:
				s.handleGroupInfoEvent(id, instance, e, webhookEventMap, chatwootEventMap)
			case *events.PushName:
				s.handlePushNameEvent(id, instance, e, webhookEventMap, chatwootEventMap)
			case *events.CallOffer:
				s.handleCallOffer(id, instance, e.BasicCallMeta)
			case *events.CallOfferNotice:
				s.handleCallOffer(id, instance, e.BasicCallMeta)
			default:
				zap.L().Debug("unknown event", zap.String("type", fmt.Sprintf("%T", evt)), zap.Any("raw", evt))
			}
		}()
	}
}

func (s *Whatsmiau) handleLoggedOut(id string) {
	client, ok := s.clients.Load(id)
	if ok {
		if err := s.deleteDeviceIfExists(context.Background(), client); err != nil {
			zap.L().Error("failed to delete device for instance", zap.String("instance", id), zap.Error(err))
			return
		}
	}

	s.clients.Delete(id)
}

func (s *Whatsmiau) handleMessageEvent(id string, instance *models.Instance, e *events.Message, webhookEventMap map[string]bool, chatwootEventMap map[string]bool) {
	shouldEmitWebhook := webhookEventMap["MESSAGES_UPSERT"]
	shouldEmitChatwoot := chatwootEventMap["MESSAGES_UPSERT"]

	if !shouldEmitWebhook && !shouldEmitChatwoot {
		return
	}

	// ✅ DEVE SER O PRIMEIRO CHECK — antes de canIgnoreGroup e canIgnoreMessage
	if e.Message != nil {
		if proto := e.Message.GetProtocolMessage(); proto != nil {
			switch proto.GetType() {
			case waE2E.ProtocolMessage_REVOKE:
				deletedID := proto.GetKey().GetID()
				zap.L().Info("🗑️ message deleted",
					zap.String("instance", id),
					zap.String("deleted_message_id", deletedID),
					zap.String("chat", e.Info.Chat.String()),
					zap.String("by", e.Info.Sender.String()),
				)
				if shouldEmitChatwoot && hasChatwoot(instance) {
					// Resolve LID JID to phone JID so Chatwoot can find the contact
					chatJID, _ := s.GetJidLid(context.Background(), id, e.Info.Chat)
					go s.handleChatwootDelete(id, instance, deletedID, chatJID)
				}
				return

			case waE2E.ProtocolMessage_MESSAGE_EDIT:
				newText := proto.GetEditedMessage().GetConversation()
				if newText == "" {
					if et := proto.GetEditedMessage().GetExtendedTextMessage(); et != nil {
						newText = et.GetText()
					}
				}
				editedID := proto.GetKey().GetID()
				fromMe := proto.GetKey().GetFromMe()
				zap.L().Info("✏️ message edited",
					zap.String("instance", id),
					zap.String("message_id", editedID),
					zap.String("chat", e.Info.Chat.String()),
					zap.String("new_text", newText),
				)
				if shouldEmitChatwoot && hasChatwoot(instance) && newText != "" {
					// Resolve LID JID to phone JID so Chatwoot can find the contact
					chatJID, _ := s.GetJidLid(context.Background(), id, e.Info.Chat)
					go s.handleChatwootEdit(id, instance, editedID, newText, fromMe, chatJID)
				}
				return

			default:
				// outros ProtocolMessages (ephemeral, etc) — ignora silenciosamente
				zap.L().Debug("protocol message ignored",
					zap.String("type", proto.GetType().String()),
					zap.String("instance", id),
				)
				return
			}
		}
	}

	if canIgnoreGroup(e, instance) {
		return
	}

	messageData := s.convertEventMessage(id, instance, e)
	if messageData == nil {
		zap.L().Error("failed to convert event", zap.String("id", id), zap.String("type", fmt.Sprintf("%T", e)), zap.Any("raw", e))
		return
	}

	messageData.InstanceId = instance.ID

	dateTime := time.Unix(int64(messageData.MessageTimestamp), 0)
	wookMessage := &WookEvent[WookMessageData]{
		Instance: instance.ID,
		Data:     messageData,
		DateTime: dateTime,
		Event:    WookMessagesUpsert,
	}

	if wookMessage.Data.Message != nil && len(wookMessage.Data.Message.Base64) > 0 {
		b64Temp := wookMessage.Data.Message.Base64
		wookMessage.Data.Message.Base64 = ""
		zap.L().Debug("message event", zap.String("instance", id), zap.Any("data", wookMessage.Data))
		wookMessage.Data.Message.Base64 = b64Temp
	} else if wookMessage.Data.Message != nil {
		zap.L().Debug("message event", zap.String("instance", id), zap.Any("data", wookMessage.Data))
	}

	if shouldEmitWebhook && instance.Webhook != nil && instance.Webhook.Url != "" {
		s.emit(wookMessage, instance.Webhook.Url)
	}

	if shouldEmitChatwoot && hasChatwoot(instance) {
		go s.handleChatwootMessage(id, instance, messageData)
	}

	// readMessages: marca como lida no WhatsApp assim que a mensagem chega
	if instance.ReadMessages && !e.Info.IsFromMe {
		go func() {
			client, ok := s.clients.Load(id)
			if !ok {
				return
			}
			sender := e.Info.Chat
			if e.Info.IsGroup {
				sender = e.Info.Sender
			}
			if err := client.MarkRead(context.TODO(), []string{e.Info.ID}, time.Now(), e.Info.Chat, sender); err != nil {
				zap.L().Warn("readMessages: erro ao marcar mensagem como lida", zap.Error(err))
			}
		}()
	}
}

func (s *Whatsmiau) handleReceiptEvent(id string, instance *models.Instance, e *events.Receipt, webhookEventMap map[string]bool, chatwootEventMap map[string]bool) {
	shouldEmitWebhook := webhookEventMap["MESSAGES_UPDATE"]
	shouldEmitChatwoot := chatwootEventMap["MESSAGES_UPDATE"]

	if !shouldEmitWebhook && !shouldEmitChatwoot {
		return
	}

	if canIgnoreGroup(e, instance) {
		return
	}

	data := s.convertEventReceipt(id, e)
	if data == nil {
		return
	}

	for _, event := range data {
		wookData := &WookEvent[WookMessageUpdateData]{
			Instance: instance.ID,
			Data:     &event,
			DateTime: e.Timestamp,
			Event:    WookMessagesUpdate,
		}

		if shouldEmitWebhook && instance.Webhook != nil && instance.Webhook.Url != "" {
			s.emit(wookData, instance.Webhook.Url)
		}
	}
}

func (s *Whatsmiau) handleBusinessNameEvent(id string, instance *models.Instance, e *events.BusinessName, webhookEventMap map[string]bool, chatwootEventMap map[string]bool) {
	if !webhookEventMap["CONTACTS_UPSERT"] && !chatwootEventMap["CONTACTS_UPSERT"] {
		return
	}

	data := s.convertBusinessName(id, e)
	if data == nil {
		zap.L().Error("failed to convert business name", zap.String("id", id), zap.String("type", fmt.Sprintf("%T", e)), zap.Any("raw", e))
		return
	}

	wookData := &WookEvent[WookContactUpsertData]{
		Instance: instance.ID,
		Data:     &WookContactUpsertData{*data},
		DateTime: time.Now(),
		Event:    WookContactsUpsert,
	}

	if webhookEventMap["CONTACTS_UPSERT"] && instance.Webhook != nil && instance.Webhook.Url != "" {
		s.emit(wookData, instance.Webhook.Url)
	}
}

func (s *Whatsmiau) handleContactEvent(id string, instance *models.Instance, e *events.Contact, webhookEventMap map[string]bool, chatwootEventMap map[string]bool) {
	if !webhookEventMap["CONTACTS_UPSERT"] && !chatwootEventMap["CONTACTS_UPSERT"] {
		return
	}

	if canIgnoreGroup(e, instance) {
		return
	}

	data := s.convertContact(id, e)
	if data == nil {
		zap.L().Error("failed to convert contact", zap.String("id", id), zap.String("type", fmt.Sprintf("%T", e)), zap.Any("raw", e))
		return
	}

	wookData := &WookEvent[WookContactUpsertData]{
		Instance: instance.ID,
		Data:     &WookContactUpsertData{*data},
		DateTime: time.Now(),
		Event:    WookContactsUpsert,
	}

	if webhookEventMap["CONTACTS_UPSERT"] && instance.Webhook != nil && instance.Webhook.Url != "" {
		s.emit(wookData, instance.Webhook.Url)
	}
}

func (s *Whatsmiau) handlePictureEvent(id string, instance *models.Instance, e *events.Picture, webhookEventMap map[string]bool, chatwootEventMap map[string]bool) {
	if !webhookEventMap["CONTACTS_UPSERT"] && !chatwootEventMap["CONTACTS_UPSERT"] {
		return
	}

	data := s.convertPicture(id, e)
	if data == nil {
		return
	}

	wookData := &WookEvent[WookContactUpsertData]{
		Instance: instance.ID,
		Data:     &WookContactUpsertData{*data},
		DateTime: e.Timestamp,
		Event:    WookContactsUpsert,
	}

	if webhookEventMap["CONTACTS_UPSERT"] && instance.Webhook != nil && instance.Webhook.Url != "" {
		s.emit(wookData, instance.Webhook.Url)
	}
}

func (s *Whatsmiau) handleHistorySyncEvent(id string, instance *models.Instance, e *events.HistorySync, webhookEventMap map[string]bool, chatwootEventMap map[string]bool) {
	if !webhookEventMap["CONTACTS_UPSERT"] && !chatwootEventMap["CONTACTS_UPSERT"] {
		return
	}

	data := s.convertContactHistorySync(id, e.Data.GetPushnames(), e.Data.Conversations)
	if data == nil {
		return
	}

	wookData := &WookEvent[WookContactUpsertData]{
		Instance: instance.ID,
		Data:     &data,
		DateTime: time.Now(),
		Event:    WookContactsUpsert,
	}

	if webhookEventMap["CONTACTS_UPSERT"] && instance.Webhook != nil && instance.Webhook.Url != "" {
		s.emit(wookData, instance.Webhook.Url)
	}
}

func (s *Whatsmiau) handleGroupInfoEvent(id string, instance *models.Instance, e *events.GroupInfo, webhookEventMap map[string]bool, chatwootEventMap map[string]bool) {
	if !webhookEventMap["CONTACTS_UPSERT"] && !chatwootEventMap["CONTACTS_UPSERT"] {
		return
	}

	if instance.GroupsIgnore {
		return
	}

	data := s.convertGroupInfo(id, e)
	if data == nil {
		zap.L().Debug("failed to convert group info", zap.String("id", id), zap.String("type", fmt.Sprintf("%T", e)), zap.Any("raw", e))
		return
	}

	wookData := &WookEvent[WookContactUpsertData]{
		Instance: instance.ID,
		Data:     &WookContactUpsertData{*data},
		DateTime: time.Now(),
		Event:    WookContactsUpsert,
	}

	if webhookEventMap["CONTACTS_UPSERT"] && instance.Webhook != nil && instance.Webhook.Url != "" {
		s.emit(wookData, instance.Webhook.Url)
	}
}

func (s *Whatsmiau) handlePushNameEvent(id string, instance *models.Instance, e *events.PushName, webhookEventMap map[string]bool, chatwootEventMap map[string]bool) {
	if !webhookEventMap["CONTACTS_UPSERT"] && !chatwootEventMap["CONTACTS_UPSERT"] {
		return
	}

	if canIgnoreGroup(e, instance) {
		return
	}

	data := s.convertPushName(id, e)
	if data == nil {
		zap.L().Error("failed to convert pushname", zap.String("id", id), zap.String("type", fmt.Sprintf("%T", e)), zap.Any("raw", e))
		return
	}

	wookData := &WookEvent[WookContactUpsertData]{
		Instance: instance.ID,
		Data:     &WookContactUpsertData{*data},
		DateTime: time.Now(),
		Event:    WookContactsUpsert,
	}

	if webhookEventMap["CONTACTS_UPSERT"] && instance.Webhook != nil && instance.Webhook.Url != "" {
		s.emit(wookData, instance.Webhook.Url)
	}
}

// parseWAMessage converts a raw waE2E.Message into our internal representation.
func (s *Whatsmiau) parseWAMessage(m *waE2E.Message) (string, *WookMessageRaw, *waE2E.ContextInfo) {
	var messageType string
	raw := &WookMessageRaw{}
	var ci *waE2E.ContextInfo

	if r := m.GetReactionMessage(); r != nil {
		messageType = "reactionMessage"
		reactionKey := &WookKey{}
		if rk := r.GetKey(); rk != nil {
			reactionKey.RemoteJid = rk.GetRemoteJID()
			reactionKey.FromMe = rk.GetFromMe()
			reactionKey.Id = rk.GetID()
			reactionKey.Participant = rk.GetParticipant()
		}
		raw.ReactionMessage = &ReactionMessageRaw{
			Text:              r.GetText(),
			SenderTimestampMs: i64(r.GetSenderTimestampMS()),
			Key:               reactionKey,
		}
	} else if lr := m.GetListResponseMessage(); lr != nil {
		messageType = "listResponseMessage"
		listType := lr.GetListType().String()
		var selectedRowID string
		if ssr := lr.GetSingleSelectReply(); ssr != nil {
			selectedRowID = ssr.GetSelectedRowID()
		}
		raw.ListResponseMessage = &WookListMessageRaw{
			ListType: listType,
			SingleSelectReply: &WookListMessageRawListSingleSelectReply{
				SelectedRowId: selectedRowID,
			},
		}
	} else if img := m.GetImageMessage(); img != nil {
		messageType = "imageMessage"
		ci = img.GetContextInfo()
		raw.ImageMessage = &WookImageMessageRaw{
			Url:               img.GetURL(),
			Mimetype:          img.GetMimetype(),
			FileSha256:        b64(img.GetFileSHA256()),
			FileLength:        u64(img.GetFileLength()),
			Height:            int(img.GetHeight()),
			Width:             int(img.GetWidth()),
			Caption:           img.GetCaption(),
			MediaKey:          b64(img.GetMediaKey()),
			FileEncSha256:     b64(img.GetFileEncSHA256()),
			DirectPath:        img.GetDirectPath(),
			MediaKeyTimestamp: i64(img.GetMediaKeyTimestamp()),
			JpegThumbnail:     b64(img.GetJPEGThumbnail()),
			ViewOnce:          img.GetViewOnce(),
		}
	} else if sticker := m.GetStickerMessage(); sticker != nil {
		messageType = "stickerMessage"
		raw.StickerMessage = &WookStickerMessageRaw{
			Url:           sticker.GetURL(),
			Mimetype:      sticker.GetMimetype(),
			FileSha256:    base64.StdEncoding.EncodeToString(sticker.GetFileSHA256()),
			FileEncSha256: base64.StdEncoding.EncodeToString(sticker.GetFileEncSHA256()),
			MediaKey:      base64.StdEncoding.EncodeToString(sticker.GetMediaKey()),
			DirectPath:    sticker.GetDirectPath(),
			IsAnimated:    sticker.GetIsAnimated(),
		}
		ci = sticker.GetContextInfo()
	} else if aud := m.GetAudioMessage(); aud != nil {
		messageType = "audioMessage"
		ci = aud.GetContextInfo()
		raw.AudioMessage = &WookAudioMessageRaw{
			Url:               aud.GetURL(),
			Mimetype:          aud.GetMimetype(),
			FileSha256:        b64(aud.GetFileSHA256()),
			FileLength:        u64(aud.GetFileLength()),
			Seconds:           int(aud.GetSeconds()),
			Ptt:               aud.GetPTT(),
			MediaKey:          b64(aud.GetMediaKey()),
			FileEncSha256:     b64(aud.GetFileEncSHA256()),
			DirectPath:        aud.GetDirectPath(),
			MediaKeyTimestamp: i64(aud.GetMediaKeyTimestamp()),
			Waveform:          b64(aud.GetWaveform()),
			ViewOnce:          aud.GetViewOnce(),
		}
	} else if doc := m.GetDocumentMessage(); doc != nil {
		messageType = "documentMessage"
		ci = doc.GetContextInfo()
		raw.DocumentMessage = &WookDocumentMessageRaw{
			Url:               doc.GetURL(),
			Mimetype:          doc.GetMimetype(),
			Title:             doc.GetTitle(),
			FileSha256:        b64(doc.GetFileSHA256()),
			FileLength:        u64(doc.GetFileLength()),
			PageCount:         int(doc.GetPageCount()),
			MediaKey:          b64(doc.GetMediaKey()),
			FileName:          doc.GetFileName(),
			FileEncSha256:     b64(doc.GetFileEncSHA256()),
			DirectPath:        doc.GetDirectPath(),
			MediaKeyTimestamp: i64(doc.GetMediaKeyTimestamp()),
			ContactVcard:      doc.GetContactVcard(),
			JpegThumbnail:     b64(doc.GetJPEGThumbnail()),
			Caption:           doc.GetCaption(),
		}
	} else if video := m.GetVideoMessage(); video != nil {
		messageType = "videoMessage"
		raw.VideoMessage = &WookVideoMessageRaw{
			Url:           video.GetURL(),
			Mimetype:      video.GetMimetype(),
			Caption:       video.GetCaption(),
			FileSha256:    b64(video.GetFileSHA256()),
			FileLength:    u64(video.GetFileLength()),
			Seconds:       video.GetSeconds(),
			MediaKey:      b64(video.GetMediaKey()),
			FileEncSha256: b64(video.GetFileEncSHA256()),
			JPEGThumbnail: b64(video.GetJPEGThumbnail()),
			GIFPlayback:   video.GetGifPlayback(),
		}
		ci = video.GetContextInfo()
	} else if contact := m.GetContactMessage(); contact != nil {
		card, err := vcard.NewDecoder(strings.NewReader(contact.GetVcard())).Decode()
		if err != nil {
			zap.L().Error("decode card error", zap.Error(err))
		}
		messageType = "contactMessage"
		raw.ContactMessage = &ContactMessageRaw{
			VCard:        contact.GetVcard(),
			DisplayName:  contact.GetDisplayName(),
			DecodedVcard: card,
		}
		ci = contact.GetContextInfo()
	} else if contactArray := m.GetContactsArrayMessage(); contactArray != nil {
		messageType = "contactsArrayMessage"
		var contacts []ContactMessageRaw
		for _, contact := range contactArray.Contacts {
			card, err := vcard.NewDecoder(strings.NewReader(contact.GetVcard())).Decode()
			if err != nil {
				zap.L().Error("decode card error", zap.Error(err))
			}
			contacts = append(contacts, ContactMessageRaw{
				VCard:        contact.GetVcard(),
				DisplayName:  contact.GetDisplayName(),
				DecodedVcard: card,
			})
		}
		raw.ContactsArrayMessage = &ContactsArrayMessageRaw{
			DisplayName: contactArray.GetDisplayName(),
			Contacts:    contacts,
		}
		ci = contactArray.GetContextInfo()
	} else if loc := m.GetLocationMessage(); loc != nil {
		messageType = "locationMessage"
		raw.LocationMessage = &WookLocationMessageRaw{
			DegreesLatitude:  loc.GetDegreesLatitude(),
			DegreesLongitude: loc.GetDegreesLongitude(),
			Name:             loc.GetName(),
			Address:          loc.GetAddress(),
			Url:              loc.GetURL(),
		}
		ci = loc.GetContextInfo()
	} else if conv := strings.TrimSpace(m.GetConversation()); conv != "" {
		messageType = "conversation"
		raw.Conversation = conv
	} else if et := m.GetExtendedTextMessage(); et != nil && len(et.GetText()) > 0 {
		messageType = "conversation"
		raw.Conversation = et.GetText()
		ci = et.GetContextInfo()
	} else {
		messageType = "unknown"
	}

	return messageType, raw, ci
}

func (s *Whatsmiau) convertContactHistorySync(id string, event []*waHistorySync.Pushname, conversations []*waHistorySync.Conversation) WookContactUpsertData {
	resultMap := make(map[string]WookContact)
	for _, pushName := range event {
		if len(pushName.GetPushname()) == 0 {
			continue
		}
		if dt := strings.Split(pushName.GetPushname(), "@"); len(dt) == 2 && (dt[1] == "g.us" || dt[1] == "s.whatsapp.net") {
			return nil
		}
		jid, err := types.ParseJID(pushName.GetID())
		if err != nil {
			zap.L().Error("failed to parse jid", zap.String("pushname", pushName.GetPushname()))
			return nil
		}
		jidParsed, lid := s.GetJidLid(context.Background(), id, jid)
		resultMap[jidParsed] = WookContact{
			RemoteJid:  jidParsed,
			PushName:   pushName.GetPushname(),
			InstanceId: id,
			RemoteLid:  lid,
		}
	}

	for _, conversation := range conversations {
		name := conversation.GetName()
		if len(name) == 0 {
			name = conversation.GetDisplayName()
		}
		if len(name) == 0 {
			name = conversation.GetUsername()
		}
		if len(name) == 0 {
			continue
		}
		if dt := strings.Split(name, "@"); len(dt) == 2 && (dt[1] == "g.us" || dt[1] == "s.whatsapp.net") {
			return nil
		}
		jid, err := types.ParseJID(conversation.GetID())
		if err != nil {
			zap.L().Error("failed to parse jid", zap.String("name", conversation.GetName()))
			return nil
		}
		jidParsed, lid := s.GetJidLid(context.Background(), id, jid)
		resultMap[conversation.GetID()] = WookContact{
			RemoteJid:  jidParsed,
			PushName:   name,
			InstanceId: id,
			RemoteLid:  lid,
		}
	}

	var result []WookContact
	for _, c := range resultMap {
		jid, err := types.ParseJID(c.RemoteJid)
		if err != nil {
			continue
		}
		url, _, err := s.getPic(id, jid)
		if err != nil {
			zap.L().Error("failed to get pic", zap.Error(err))
		}
		c.ProfilePicUrl = url
		result = append(result, c)
	}

	return result
}

func (s *Whatsmiau) convertEventMessage(id string, instance *models.Instance, evt *events.Message) *WookMessageData {
	ctx, c := context.WithTimeout(context.Background(), time.Second*60)
	defer c()

	client, ok := s.clients.Load(id)
	if !ok {
		zap.L().Warn("no client for event", zap.String("id", id))
		return nil
	}

	if evt == nil || evt.Message == nil {
		return nil
	}

	jid, lid := s.GetJidLid(ctx, id, evt.Info.Chat)
	senderJid, _ := s.GetJidLid(ctx, id, evt.Info.Sender)

	e := evt.UnwrapRaw()
	m := e.Message

	key := &WookKey{
		RemoteJid:   jid,
		RemoteLid:   lid,
		FromMe:      e.Info.IsFromMe,
		Id:          e.Info.ID,
		Participant: senderJid,
	}

	status := "received"
	if e.Info.IsFromMe {
		status = "sent"
	}

	ts := e.Info.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	messageType, raw, ci := s.parseWAMessage(m)

	// Upload media (URL / Base64) when needed
	switch messageType {
	case "imageMessage":
		if img := m.GetImageMessage(); img != nil {
			raw.MediaURL, raw.Base64 = s.uploadMessageFile(ctx, instance, client, img, img.GetMimetype(), "")
		}
	case "stickerMessage":
		if st := m.GetStickerMessage(); st != nil {
			messageType = "imageMessage"
			raw.MediaURL, raw.Base64 = s.uploadMessageFile(ctx, instance, client, st, "image/webp", "")
		}
	case "audioMessage":
		if aud := m.GetAudioMessage(); aud != nil {
			raw.MediaURL, raw.Base64 = s.uploadMessageFile(ctx, instance, client, aud, aud.GetMimetype(), "")
		}
	case "documentMessage":
		if doc := m.GetDocumentMessage(); doc != nil {
			raw.MediaURL, raw.Base64 = s.uploadMessageFile(ctx, instance, client, doc, doc.GetMimetype(), doc.GetFileName())
		}
	case "videoMessage":
		if vid := m.GetVideoMessage(); vid != nil {
			raw.MediaURL, raw.Base64 = s.uploadMessageFile(ctx, instance, client, vid, vid.GetMimetype(), "")
		}
	}

	var messageContext WookMessageContextInfo
	if ci != nil {
		messageContext.EphemeralSettingTimestamp = i64(ci.GetEphemeralSettingTimestamp())
		messageContext.StanzaId = ci.GetStanzaID()
		messageContext.Participant = ci.GetParticipant()
		messageContext.Expiration = int(ci.GetExpiration())
		messageContext.MentionedJid = ci.GetMentionedJID()
		messageContext.ConversionSource = ci.GetConversionSource()
		messageContext.ConversionData = b64(ci.GetConversionData())
		messageContext.ConversionDelaySeconds = int(ci.GetConversionDelaySeconds())
		messageContext.EntryPointConversionSource = ci.GetEntryPointConversionSource()
		messageContext.EntryPointConversionApp = ci.GetEntryPointConversionApp()
		messageContext.EntryPointConversionDelaySeconds = int(ci.GetEntryPointConversionDelaySeconds())
		messageContext.TrustBannerAction = ci.GetTrustBannerAction()

		if dm := ci.GetDisappearingMode(); dm != nil {
			messageContext.DisappearingMode = &ContextInfoDisappearingMode{
				Initiator:     dm.GetInitiator().String(),
				Trigger:       dm.GetTrigger().String(),
				InitiatedByMe: dm.GetInitiatedByMe(),
			}
		}

		if ear := ci.GetExternalAdReply(); ear != nil {
			messageType = "conversation"
			messageContext.ExternalAdReply = &WookMessageContextInfoExternalAdReply{
				Title:                 ear.GetTitle(),
				Body:                  ear.GetBody(),
				MediaType:             ear.GetMediaType().String(),
				ThumbnailUrl:          ear.GetThumbnailURL(),
				Thumbnail:             b64(ear.GetThumbnail()),
				SourceType:            ear.GetSourceType(),
				SourceId:              ear.GetSourceID(),
				SourceUrl:             ear.GetSourceURL(),
				ContainsAutoReply:     ear.GetContainsAutoReply(),
				RenderLargerThumbnail: ear.GetRenderLargerThumbnail(),
				ShowAdAttribution:     ear.GetShowAdAttribution(),
				CtwaClid:              ear.GetCtwaClid(),
			}
		}

		if qm := ci.GetQuotedMessage(); qm != nil {
			_, qmRaw, _ := s.parseWAMessage(qm)
			messageContext.QuotedMessage = qmRaw
		}
	}

	// Quando fromMe=true, pushName é o operador, não o destinatário.
	// Tenta buscar o nome do destinatário no store local do whatsmeow.
	var contactName string
	if e.Info.IsFromMe {
		if info, err := client.Store.Contacts.GetContact(ctx, evt.Info.Chat); err == nil {
			if info.PushName != "" {
				contactName = info.PushName
			} else if info.BusinessName != "" {
				contactName = info.BusinessName
			}
		}
	}

	return &WookMessageData{
		Key:              key,
		PushName:         strings.TrimSpace(e.Info.PushName),
		ContactName:      contactName,
		Status:           status,
		Message:          raw,
		ContextInfo:      &messageContext,
		MessageType:      messageType,
		MessageTimestamp: int(ts.Unix()),
		InstanceId:       id,
		Source:           "whatsapp",
	}
}

func (s *Whatsmiau) convertEventReceipt(id string, evt *events.Receipt) []WookMessageUpdateData {
	var status WookMessageUpdateStatus
	switch evt.Type {
	case types.ReceiptTypeRead:
		status = MessageStatusRead
	case types.ReceiptTypeDelivered:
		status = MessageStatusDeliveryAck
	default:
		return nil
	}

	chatJid, chatLid := s.GetJidLid(context.Background(), id, evt.Chat)
	participantJid, _ := s.GetJidLid(context.Background(), id, evt.Sender)

	var result []WookMessageUpdateData
	for _, messageID := range evt.MessageIDs {
		result = append(result, WookMessageUpdateData{
			MessageId:   messageID,
			KeyId:       messageID,
			RemoteJid:   chatJid,
			RemoteLid:   chatLid,
			FromMe:      evt.IsFromMe,
			Participant: participantJid,
			Status:      status,
			InstanceId:  id,
		})
	}

	return result
}

func (s *Whatsmiau) uploadMessageFile(ctx context.Context, instance *models.Instance, client *whatsmeow.Client, fileMessage whatsmeow.DownloadableMessage, mimetype, fileName string) (string, string) {
	var (
		b64Result string
		urlResult string
		ext       string
	)

	tmpFile, err := os.CreateTemp("", "file-*")
	if err != nil {
		panic(err)
	}
	defer os.Remove(tmpFile.Name())

	if err := client.DownloadToFile(ctx, fileMessage, tmpFile); err != nil {
		zap.L().Error("failed to download media", zap.Error(err))
		return "", ""
	}

	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		zap.L().Error("failed to seek media file", zap.Error(err))
		return "", ""
	}

	ext = extractExtFromFile(fileName, mimetype, tmpFile)

	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		zap.L().Error("failed to seek media file before base64", zap.Error(err))
		return "", ""
	}

	// Gera Base64 se habilitado na instância
	if instance.Webhook != nil && instance.Webhook.Base64 != nil && *instance.Webhook.Base64 {
		data, err := io.ReadAll(tmpFile)
		if err != nil {
			zap.L().Error("failed to read media for base64", zap.Error(err))
		} else {
			b64Result = base64.StdEncoding.EncodeToString(data)
			zap.L().Debug("base64 generated",
				zap.String("mimetype", mimetype),
				zap.String("ext", ext),
				zap.Int("bytes", len(data)),
				zap.Int("b64Len", len(b64Result)),
			)
		}
	}

	if s.fileStorage != nil {
		if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
			zap.L().Error("failed to seek media file before upload", zap.Error(err))
		} else {
			urlResult, _, err = s.fileStorage.Upload(ctx, uuid.NewString()+"."+ext, mimetype, tmpFile)
			if err != nil {
				zap.L().Error("failed to upload media to storage", zap.Error(err))
			}
		}
	}

	return urlResult, b64Result
}

func (s *Whatsmiau) convertContact(id string, evt *events.Contact) *WookContact {
	url, _, err := s.getPic(id, evt.JID)
	if err != nil {
		zap.L().Error("failed to get pic", zap.Error(err))
	}

	name := evt.Action.GetFirstName()
	if name == "" {
		name = evt.Action.GetFullName()
	}
	if name == "" {
		name = evt.Action.GetUsername()
	}
	if name == "" {
		return nil
	}

	if dt := strings.Split(name, "@"); len(dt) == 2 && (dt[1] == "g.us" || dt[1] == "s.whatsapp.net") {
		return nil
	}

	jid, lid := s.GetJidLid(context.Background(), id, evt.JID)
	return &WookContact{
		RemoteJid:     jid,
		RemoteLid:     lid,
		PushName:      name,
		ProfilePicUrl: url,
		InstanceId:    id,
	}
}

func (s *Whatsmiau) convertGroupInfo(id string, evt *events.GroupInfo) *WookContact {
	url, _, err := s.getPic(id, evt.JID)
	if err != nil {
		zap.L().Error("failed to get pic", zap.Error(err))
	}

	if evt.Name == nil || len(evt.Name.Name) == 0 {
		return nil
	}

	if dt := strings.Split(evt.Name.Name, "@"); len(dt) == 2 && (dt[1] == "g.us" || dt[1] == "s.whatsapp.net") {
		return nil
	}

	jid, lid := s.GetJidLid(context.Background(), id, evt.JID)

	return &WookContact{
		RemoteJid:     jid,
		PushName:      evt.Name.Name,
		ProfilePicUrl: url,
		InstanceId:    id,
		RemoteLid:     lid,
	}
}

func (s *Whatsmiau) convertPushName(id string, evt *events.PushName) *WookContact {
	url, _, err := s.getPic(id, evt.JID)
	if err != nil {
		zap.L().Error("failed to get pic", zap.Error(err))
	}

	name := evt.NewPushName
	if len(name) == 0 {
		name = evt.OldPushName
	}

	if name == "" {
		return nil
	}

	if dt := strings.Split(name, "@"); len(dt) == 2 && (dt[1] == "g.us" || dt[1] == "s.whatsapp.net") {
		return nil
	}

	jid, lid := s.GetJidLid(context.Background(), id, evt.JID)

	return &WookContact{
		RemoteJid:     jid,
		PushName:      evt.NewPushName,
		InstanceId:    id,
		ProfilePicUrl: url,
		RemoteLid:     lid,
	}
}

func (s *Whatsmiau) convertPicture(id string, evt *events.Picture) *WookContact {
	url, b64, err := s.getPic(id, evt.JID)
	if err != nil {
		zap.L().Error("failed to get pic", zap.Error(err))
	}

	if len(url) <= 0 {
		return nil
	}

	jid, lid := s.GetJidLid(context.Background(), id, evt.JID)

	return &WookContact{
		RemoteJid:     jid,
		InstanceId:    id,
		Base64Pic:     b64,
		ProfilePicUrl: url,
		RemoteLid:     lid,
	}
}

func (s *Whatsmiau) convertBusinessName(id string, evt *events.BusinessName) *WookContact {
	url, b64, err := s.getPic(id, evt.JID)
	if err != nil {
		zap.L().Error("failed to get pic", zap.Error(err))
	}

	name := evt.NewBusinessName
	if name == "" {
		name = evt.OldBusinessName
	}
	if name == "" && evt.Message != nil {
		name = evt.Message.PushName
	}
	if name == "" && evt.Message != nil && evt.Message.VerifiedName != nil && evt.Message.VerifiedName.Details != nil {
		name = evt.Message.VerifiedName.Details.GetVerifiedName()
	}

	if dt := strings.Split(name, "@"); len(dt) == 2 && (dt[1] == "g.us" || dt[1] == "s.whatsapp.net") {
		return nil
	}

	jid, lid := s.GetJidLid(context.Background(), id, evt.JID)

	return &WookContact{
		RemoteJid:     jid,
		InstanceId:    id,
		Base64Pic:     b64,
		ProfilePicUrl: url,
		PushName:      name,
		RemoteLid:     lid,
	}
}

func (s *Whatsmiau) getPic(id string, jid types.JID) (string, string, error) {
	client, ok := s.clients.Load(id)
	if !ok || client == nil {
		zap.L().Warn("no client for event", zap.String("id", id))
		return "", "", fmt.Errorf("no client for event %s", id)
	}

	pic, err := client.GetProfilePictureInfo(context.TODO(), jid, &whatsmeow.GetProfilePictureParams{
		Preview:     true,
		IsCommunity: false,
	})
	if err != nil {
		return "", "", nil
	}

	if pic == nil {
		return "", "", err
	}

	res, err := s.httpClient.Get(pic.URL)
	if err != nil {
		zap.L().Error("get profile picture error", zap.String("id", id), zap.Error(err))
		return "", "", err
	}

	picRaw, err := io.ReadAll(res.Body)
	if err != nil {
		zap.L().Error("get profile picture error", zap.String("id", id), zap.Error(err))
		return "", "", err
	}

	return pic.URL, base64.StdEncoding.EncodeToString(picRaw), nil
}

// ── Chatwoot helpers ──────────────────────────────────────────────────────────

func (s *Whatsmiau) handleChatwootMessage(id string, instance *models.Instance, messageData *WookMessageData) {
	if messageData == nil || messageData.Key == nil {
		return
	}
	svc := NewChatwootService(ChatwootConfig{
		URL:       instance.ChatwootURL,
		AccountID: instance.ChatwootAccountID,
		Token:     instance.ChatwootToken,
	})

	opts := HandleMessageOptions{}

	if client, ok := s.clients.Load(id); ok && client != nil {
		if client.Store.ID != nil {
			opts.InstanceJID = client.Store.ID.String()
		}

		if messageData.Key != nil {
			if jid, err := types.ParseJID(messageData.Key.RemoteJid); err == nil {
				if url, _, err := s.getPic(id, jid); err == nil {
					opts.ProfilePicURL = url
				}

				if jid.Server == "g.us" {
					if groupInfo, err := client.GetGroupInfo(context.Background(), jid); err == nil && groupInfo != nil {
						opts.GroupName = groupInfo.Name
					}
				}
			}
		}
	}

	svc.HandleMessage(id, messageData, opts)
}

// NotifySGPMessage sends an outgoing text message to Chatwoot on behalf of the SGP
// integration, without changing the conversation status. Only runs if the
// instance has both Chatwoot configured and SGPSyncChatwoot enabled.
func (s *Whatsmiau) NotifySGPMessage(ctx context.Context, instanceID, toPhone, text, msgID string) {
	instance := s.getInstanceCached(instanceID)
	if instance == nil || !instance.SGPSyncChatwoot || !hasChatwoot(instance) {
		return
	}

	messageData := &WookMessageData{
		Key: &WookKey{
			RemoteJid: toPhone + "@s.whatsapp.net",
			FromMe:    true,
			Id:        msgID,
		},
		Status:           "DELIVERY_ACK",
		Message:          &WookMessageRaw{Conversation: text},
		MessageType:      "conversation",
		MessageTimestamp: int(time.Now().Unix()),
		InstanceId:       instanceID,
	}

	go s.handleChatwootMessageKeepStatus(instanceID, instance, messageData)
}

// NotifySGPMediaURL downloads media from mediaURL and sends it as an attachment
// to Chatwoot without changing the conversation status.
// msgType must be "imageMessage" or "documentMessage".
func (s *Whatsmiau) NotifySGPMediaURL(ctx context.Context, instanceID, toPhone, mediaURL, mimetype, filename, caption, msgID, msgType string) {
	instance := s.getInstanceCached(instanceID)
	if instance == nil || !instance.SGPSyncChatwoot || !hasChatwoot(instance) {
		return
	}

	go func() {
		httpClient := &http.Client{Timeout: 30 * time.Second}
		resp, err := httpClient.Get(mediaURL)
		if err != nil {
			zap.L().Error("sgp: falha ao baixar mídia para Chatwoot", zap.String("url", mediaURL), zap.Error(err))
			return
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			zap.L().Error("sgp: falha ao ler mídia para Chatwoot", zap.Error(err))
			return
		}

		b64 := base64.StdEncoding.EncodeToString(data)

		var msg *WookMessageRaw
		switch msgType {
		case "imageMessage":
			msg = &WookMessageRaw{
				Base64: b64,
				ImageMessage: &WookImageMessageRaw{
					Mimetype: mimetype,
					Caption:  caption,
				},
			}
		default: // documentMessage
			msg = &WookMessageRaw{
				Base64: b64,
				DocumentMessage: &WookDocumentMessageRaw{
					Mimetype: mimetype,
					FileName: filename,
					Caption:  caption,
				},
			}
		}

		messageData := &WookMessageData{
			Key: &WookKey{
				RemoteJid: toPhone + "@s.whatsapp.net",
				FromMe:    true,
				Id:        msgID,
			},
			Status:           "DELIVERY_ACK",
			Message:          msg,
			MessageType:      msgType,
			MessageTimestamp: int(time.Now().Unix()),
			InstanceId:       instanceID,
		}

		s.handleChatwootMessageKeepStatus(instanceID, instance, messageData)
	}()
}

// NotifySGPMediaBytes sends raw media bytes as an attachment to Chatwoot
// without changing the conversation status. Used for in-memory media like QR codes.
func (s *Whatsmiau) NotifySGPMediaBytes(ctx context.Context, instanceID, toPhone string, data []byte, mimetype, filename, caption, msgID, msgType string) {
	instance := s.getInstanceCached(instanceID)
	if instance == nil || !instance.SGPSyncChatwoot || !hasChatwoot(instance) {
		return
	}

	b64 := base64.StdEncoding.EncodeToString(data)

	var msg *WookMessageRaw
	switch msgType {
	case "imageMessage":
		msg = &WookMessageRaw{
			Base64: b64,
			ImageMessage: &WookImageMessageRaw{
				Mimetype: mimetype,
				Caption:  caption,
			},
		}
	default:
		msg = &WookMessageRaw{
			Base64: b64,
			DocumentMessage: &WookDocumentMessageRaw{
				Mimetype: mimetype,
				FileName: filename,
				Caption:  caption,
			},
		}
	}

	messageData := &WookMessageData{
		Key: &WookKey{
			RemoteJid: toPhone + "@s.whatsapp.net",
			FromMe:    true,
			Id:        msgID,
		},
		Status:           "DELIVERY_ACK",
		Message:          msg,
		MessageType:      msgType,
		MessageTimestamp: int(time.Now().Unix()),
		InstanceId:       instanceID,
	}

	go s.handleChatwootMessageKeepStatus(instanceID, instance, messageData)
}

func (s *Whatsmiau) handleChatwootMessageKeepStatus(id string, instance *models.Instance, messageData *WookMessageData) {
	if messageData == nil || messageData.Key == nil {
		return
	}
	svc := NewChatwootService(ChatwootConfig{
		URL:       instance.ChatwootURL,
		AccountID: instance.ChatwootAccountID,
		Token:     instance.ChatwootToken,
	})

	opts := HandleMessageOptions{KeepStatus: true}

	if client, ok := s.clients.Load(id); ok && client != nil {
		if client.Store.ID != nil {
			opts.InstanceJID = client.Store.ID.String()
		}
	}

	svc.HandleMessage(id, messageData, opts)
}

// chatwootOutgoingKey retorna a chave Redis para o mapeamento chatwootMsgID → WAID.
func chatwootOutgoingKey(chatwootMsgID int) string {
	return fmt.Sprintf("chatwoot:outgoing:%d", chatwootMsgID)
}

// UpdateChatwootMessageSourceID salva o WAID no Redis (TTL = 60h) e tenta persistir
// no banco do Chatwoot via SQL direto. O Redis é a fonte primária para editar/apagar.
func (s *Whatsmiau) UpdateChatwootMessageSourceID(instance *models.Instance, chatwootMsgID int, waMessageID string) {
	if instance == nil || !hasChatwoot(instance) {
		return
	}
	sourceID := fmt.Sprintf("WAID:%s", waMessageID)

	// Persiste no Redis com TTL de 60h — expira automaticamente junto com o prazo do WhatsApp.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.redis.Set(ctx, chatwootOutgoingKey(chatwootMsgID), sourceID, WhatsAppRevokeTTL).Err(); err != nil {
		zap.L().Warn("chatwoot: erro ao salvar source_id no Redis",
			zap.Int("chatwootMsgID", chatwootMsgID),
			zap.String("sourceID", sourceID),
			zap.Error(err))
	}

	// Tenta também atualizar direto no banco do Chatwoot (se configurado).
	svc := NewChatwootService(ChatwootConfig{
		URL:       instance.ChatwootURL,
		AccountID: instance.ChatwootAccountID,
		Token:     instance.ChatwootToken,
	})
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if err := svc.UpdateMessageSourceID(ctx2, chatwootMsgID, sourceID); err != nil {
		zap.L().Warn("chatwoot: source_id salvo apenas no Redis (banco indisponível)",
			zap.Int("chatwootMsgID", chatwootMsgID),
			zap.String("sourceID", sourceID),
			zap.Error(err))
	} else {
		zap.L().Info("chatwoot: 🔗 source_id atualizado",
			zap.Int("chatwootMsgID", chatwootMsgID),
			zap.String("sourceID", sourceID))
	}
}

// GetChatwootOutgoingWAID retorna o WAID de uma mensagem outgoing pelo ID do Chatwoot.
// Retorna ("", false) se a chave não existir (expirou os 60h ou nunca foi salva).
func (s *Whatsmiau) GetChatwootOutgoingWAID(chatwootMsgID int) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	val, err := s.redis.Get(ctx, chatwootOutgoingKey(chatwootMsgID)).Result()
	if err == goredis.Nil || err != nil {
		return "", false
	}
	return val, true
}

func (s *Whatsmiau) handleChatwootDelete(id string, instance *models.Instance, messageID, chatJID string) {
	// Marca que ESTE messageID foi revogado pelo WhatsApp (não pelo operador no Chatwoot).
	// O webhook de message_deleted que o Chatwoot vai disparar após deletarmos via API
	// deve ignorar — a exclusão já aconteceu no WhatsApp.
	ctx := context.Background()
	markerKey := fmt.Sprintf("chatwoot:wa_revoked:%s", messageID)
	s.redis.Set(ctx, markerKey, "1", 90*time.Second)

	svc := NewChatwootService(ChatwootConfig{
		URL:       instance.ChatwootURL,
		AccountID: instance.ChatwootAccountID,
		Token:     instance.ChatwootToken,
	})
	svc.HandleMessageDelete(id, messageID, chatJID)
}

// IsWARevokedMessage retorna true se o messageID foi revogado pelo WhatsApp
// (e não pelo operador via Chatwoot UI). Usado para evitar loop de deleção.
func (s *Whatsmiau) IsWARevokedMessage(waMessageID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	key := fmt.Sprintf("chatwoot:wa_revoked:%s", waMessageID)
	err := s.redis.Get(ctx, key).Err()
	return err == nil
}

// PostChatwootPrivateNote posta uma nota privada em uma conversa do Chatwoot.
func (s *Whatsmiau) PostChatwootPrivateNote(instance *models.Instance, conversationID int, content string) {
	if instance == nil || !hasChatwoot(instance) {
		return
	}
	svc := NewChatwootService(ChatwootConfig{
		URL:       instance.ChatwootURL,
		AccountID: instance.ChatwootAccountID,
		Token:     instance.ChatwootToken,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.PostPrivateNote(ctx, conversationID, content); err != nil {
		zap.L().Warn("chatwoot: erro ao postar nota privada",
			zap.Int("conversationID", conversationID),
			zap.Error(err))
	}
}

func (s *Whatsmiau) handleChatwootEdit(id string, instance *models.Instance, messageID, newText string, fromMe bool, chatJID string) {
	svc := NewChatwootService(ChatwootConfig{
		URL:       instance.ChatwootURL,
		AccountID: instance.ChatwootAccountID,
		Token:     instance.ChatwootToken,
	})
	svc.HandleMessageEdit(id, messageID, newText, fromMe, chatJID)
}

// handleConnectedEvent é chamado quando a conexão com o WhatsApp é estabelecida.
// Se alwaysOnline estiver habilitado, envia presença disponível.
func (s *Whatsmiau) handleConnectedEvent(id string, instance *models.Instance) {
	if !instance.AlwaysOnline {
		return
	}
	client, ok := s.clients.Load(id)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.SendPresence(ctx, types.PresenceAvailable); err != nil {
		zap.L().Warn("alwaysOnline: erro ao enviar presença disponível", zap.String("instance", id), zap.Error(err))
	} else {
		zap.L().Info("alwaysOnline: ✅ presença disponível enviada", zap.String("instance", id))
	}
}

// handleCallOffer rejeita ligações recebidas se rejectCall estiver habilitado
// e envia a mensagem msgCall configurada (se houver).
// Também notifica o Chatwoot sobre a tentativa de ligação.
func (s *Whatsmiau) handleCallOffer(id string, instance *models.Instance, meta types.BasicCallMeta) {
	// Notifica Chatwoot independente de rejeitar ou não
	if hasChatwoot(instance) {
		go func() {
			ctx := context.Background()

			// Resolve LID → JID de telefone real.
			// meta.From pode ser um LID (ex: 222724236542150@lid) em vez de um JID normal.
			// extractJidLid converte para o JID de telefone usando a store local.
			callerJIDStr, _ := s.extractJidLid(ctx, id, meta.From)

			// callerJIDStr pode ser "5511987654321@s.whatsapp.net" ou ainda o LID se não resolvido.
			// Garantimos o formato correto para o HandleMessage.
			remoteJid := callerJIDStr
			if !strings.Contains(remoteJid, "@") {
				remoteJid = remoteJid + "@s.whatsapp.net"
			} else if strings.Contains(remoteJid, "@lid") {
				// Não foi possível resolver o LID — não adianta criar contato errado
				zap.L().Warn("chatwoot: 📵 ligação de LID não resolvido, ignorando notificação",
					zap.String("instance", id),
					zap.String("from", meta.From.String()))
				return
			}

			zap.L().Info("chatwoot: 📵 ligação recebida",
				zap.String("instance", id),
				zap.String("from", meta.From.String()),
				zap.String("resolvedJid", remoteJid))

			msgID := fmt.Sprintf("call-%s", meta.CallID)
			callText := "📵 *Tentativa de ligação via WhatsApp*\n_(o cliente tentou iniciar uma chamada de voz ou vídeo)_"
			svc := NewChatwootService(ChatwootConfig{
				URL:       instance.ChatwootURL,
				AccountID: instance.ChatwootAccountID,
				Token:     instance.ChatwootToken,
			})

			pushName := ""
			opts := HandleMessageOptions{}
			if client, ok := s.clients.Load(id); ok && client != nil {
				if client.Store.ID != nil {
					opts.InstanceJID = client.Store.ID.String()
				}
				// Tenta obter nome do contato (usa o JID resolvido se possível)
				resolvedJID := meta.From
				if pn, err := types.ParseJID(remoteJid); err == nil {
					resolvedJID = pn
				}
				if info, err := client.Store.Contacts.GetContact(ctx, resolvedJID); err == nil {
					if info.PushName != "" {
						pushName = info.PushName
					} else if info.BusinessName != "" {
						pushName = info.BusinessName
					}
				}
				if url, _, err := s.getPic(id, resolvedJID); err == nil {
					opts.ProfilePicURL = url
				}
			}

			messageData := &WookMessageData{
				Key: &WookKey{
					RemoteJid: remoteJid,
					FromMe:    false,
					Id:        msgID,
				},
				PushName:         pushName,
				Status:           "DELIVERY_ACK",
				Message:          &WookMessageRaw{Conversation: callText},
				MessageType:      "conversation",
				MessageTimestamp: int(time.Now().Unix()),
				InstanceId:       id,
			}
			svc.HandleMessage(id, messageData, opts)
		}()
	}

	if !instance.RejectCall {
		return
	}
	client, ok := s.clients.Load(id)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.RejectCall(ctx, meta.From, meta.CallID); err != nil {
		zap.L().Warn("rejectCall: erro ao rejeitar ligação",
			zap.String("instance", id),
			zap.String("from", meta.From.String()),
			zap.Error(err))
	} else {
		zap.L().Info("rejectCall: 📵 ligação rejeitada",
			zap.String("instance", id),
			zap.String("from", meta.From.String()))
	}

	if instance.MsgCall == "" {
		return
	}

	// Envia mensagem configurada após rejeitar
	msgCtx, msgCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer msgCancel()
	if _, err := s.SendText(msgCtx, &SendText{
		InstanceID: id,
		RemoteJID:  &meta.From,
		Text:       instance.MsgCall,
	}); err != nil {
		zap.L().Warn("rejectCall: erro ao enviar msgCall",
			zap.String("instance", id),
			zap.String("from", meta.From.String()),
			zap.Error(err))
	}
}
