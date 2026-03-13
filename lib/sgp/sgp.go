package sgp

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	goredis "github.com/go-redis/redis/v8"
	"github.com/skip2/go-qrcode"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"go.mau.fi/whatsmeow/types"
	"go.uber.org/zap"
)

const pendingKey = "sgp_pending_messages"

var splitTagRe = regexp.MustCompile(
	`(\[dividir_mensagem\]` +
		`|\[boleto=[^\]]+\]` +
		`|\[link=[^\]]+\]` +
		`|\[linkcurto=[^\]]+\]` +
		`|\[qrcode=[^\]]+\]` +
		`|\[sendimg=[^\[]+` +
		`|\[lista\]` +
		`|\[template:[^\]]+\]` +
		`|\[botoes=[^\]]+\])`,
)

var (
	boletoRe    = regexp.MustCompile(`\[boleto=(.+?),\s*(.+?)(?:,\s*(.+?))?\]`)
	sendimgRe   = regexp.MustCompile(`\[sendimg=(.+?)\](.*)`)
	linkRe      = regexp.MustCompile(`\[link=(.+?)(?:,\s*(.+?))?\]`)
	linkcurtoRe = regexp.MustCompile(`\[linkcurto=(.+?)\]`)
	qrcodeRe    = regexp.MustCompile(`\[qrcode=(.+?)\]`)
	templateRe  = regexp.MustCompile(`\[template:([^;\]]+)((?:;[^\]]*)*)\]`)
	botoesRe    = regexp.MustCompile(`\[botoes=([^\]]+)\]`)
)

// PendingMessage is stored in Redis sorted set for scheduled delivery.
type PendingMessage struct {
	Message    string `json:"message"`
	InstanceID string `json:"instanceId"`
	ToNumber   string `json:"toNumber"`
	SendAt     int64  `json:"sendAt"`
}

// SGPService processes SGP message requests and schedules deferred messages.
type SGPService struct {
	wm         *whatsmiau.Whatsmiau
	redis      *goredis.Client
	httpClient *http.Client
	holidays   map[string]bool
}

// New creates a SGPService and starts the background scheduler.
func New(wm *whatsmiau.Whatsmiau, redis *goredis.Client) *SGPService {
	s := &SGPService{
		wm:         wm,
		redis:      redis,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		holidays:   loadHolidays(),
	}
	go s.startScheduler()
	return s
}

var globalSGP *SGPService
var sgpOnce sync.Once

func Init(wm *whatsmiau.Whatsmiau, redis *goredis.Client) {
	sgpOnce.Do(func() {
		globalSGP = New(wm, redis)
	})
}

func GetService() *SGPService {
	return globalSGP
}

func loadHolidays() map[string]bool {
	type holidayFile struct {
		Holidays []string `json:"holidays"`
	}
	out := map[string]bool{}
	data, err := os.ReadFile("holidays.json")
	if err != nil {
		return out
	}
	var hf holidayFile
	if err := json.Unmarshal(data, &hf); err != nil {
		return out
	}
	for _, h := range hf.Holidays {
		out[h] = true
	}
	return out
}

func (s *SGPService) isWeekend() bool {
	wd := time.Now().Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

func (s *SGPService) isHoliday(t time.Time) bool {
	return s.holidays[t.Format("2006-01-02")]
}

func (s *SGPService) getNextWeekday() time.Time {
	next := time.Now().AddDate(0, 0, 1)
	for next.Weekday() >= time.Saturday || s.isHoliday(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// HandleRequest is the HTTP entry point. Handles [fim] scheduling or sends immediately.
func (s *SGPService) HandleRequest(instanceID, toNumber, message string) error {
	message = strings.TrimSpace(message)

	if strings.Contains(message, "[fim]") {
		message = strings.ReplaceAll(message, "[fim]", "")
		message = strings.TrimSpace(message)

		if s.isWeekend() || s.isHoliday(time.Now()) {
			next := s.getNextWeekday()
			zap.L().Info("sgp: [fim] — agendando para próximo dia útil",
				zap.String("instanceId", instanceID),
				zap.Time("sendAt", next))
			return s.savePending(instanceID, toNumber, message, next.Unix())
		}
		return s.savePending(instanceID, toNumber, message, time.Now().Add(5*time.Second).Unix())
	}

	return s.sendMessage(context.Background(), instanceID, toNumber, message)
}

func (s *SGPService) savePending(instanceID, toNumber, message string, sendAt int64) error {
	msg := PendingMessage{
		Message:    message,
		InstanceID: instanceID,
		ToNumber:   toNumber,
		SendAt:     sendAt,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return s.redis.ZAdd(context.Background(), pendingKey, &goredis.Z{
		Score:  float64(sendAt),
		Member: string(data),
	}).Err()
}

func (s *SGPService) startScheduler() {
	ticker := time.NewTicker(15 * time.Second)
	for range ticker.C {
		s.processPending()
	}
}

func (s *SGPService) processPending() {
	ctx := context.Background()
	now := strconv.FormatInt(time.Now().Unix(), 10)

	results, err := s.redis.ZRangeByScore(ctx, pendingKey, &goredis.ZRangeBy{
		Min: "0",
		Max: now,
	}).Result()
	if err != nil || len(results) == 0 {
		return
	}

	for _, raw := range results {
		var msg PendingMessage
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			zap.L().Warn("sgp: mensagem pendente inválida", zap.Error(err))
			s.redis.ZRem(ctx, pendingKey, raw)
			continue
		}

		zap.L().Info("sgp: processando mensagem pendente",
			zap.String("instanceId", msg.InstanceID),
			zap.String("to", msg.ToNumber))

		if err := s.sendMessage(ctx, msg.InstanceID, msg.ToNumber, msg.Message); err != nil {
			zap.L().Error("sgp: erro ao enviar mensagem pendente", zap.Error(err))
		}
		s.redis.ZRem(ctx, pendingKey, raw)
	}
}

// sendMessage parses message tags and dispatches each part.
func (s *SGPService) sendMessage(ctx context.Context, instanceID, toNumber, message string) error {
	// Split preserving delimiters
	parts := splitTagRe.Split(message, -1)
	tags := splitTagRe.FindAllString(message, -1)

	var ordered []string
	for i, p := range parts {
		if p != "" {
			ordered = append(ordered, p)
		}
		if i < len(tags) {
			ordered = append(ordered, tags[i])
		}
	}

	for _, part := range ordered {
		part = strings.TrimSpace(part)
		if part == "" || part == "[dividir_mensagem]" {
			continue
		}

		var err error
		switch {
		case strings.HasPrefix(part, "[template:"):
			err = s.processTemplate(ctx, instanceID, toNumber, part)
		case strings.HasPrefix(part, "[boleto="):
			err = s.processBoleto(ctx, instanceID, toNumber, part)
		case strings.HasPrefix(part, "[sendimg="):
			err = s.processSendImg(ctx, instanceID, toNumber, part)
		case strings.HasPrefix(part, "[link="):
			err = s.processLink(ctx, instanceID, toNumber, part)
		case strings.HasPrefix(part, "[linkcurto="):
			err = s.processLinkCurto(ctx, instanceID, toNumber, part)
		case strings.HasPrefix(part, "[qrcode="):
			err = s.processQRCode(ctx, instanceID, toNumber, part)
		case strings.HasPrefix(part, "[lista]"):
			err = s.processLista(ctx, instanceID, toNumber)
		case strings.HasPrefix(part, "[botoes="):
			err = s.processBotoes(ctx, instanceID, toNumber, part)
		default:
			err = s.processText(ctx, instanceID, toNumber, part)
		}

		if err != nil {
			zap.L().Error("sgp: erro ao processar parte",
				zap.String("instanceId", instanceID),
				zap.String("part", part[:min(len(part), 60)]),
				zap.Error(err))
		}

		// Delay aleatório entre partes (1.5–5s como no original Python)
		time.Sleep(time.Duration(1500+rand.Intn(3500)) * time.Millisecond)
	}
	return nil
}

// resolveJID delegates to Whatsmiau.ResolveJID which handles DDD+9 resolution
// with Redis caching. Single source of truth for phone → JID conversion.
func (s *SGPService) resolveJID(ctx context.Context, instanceID, phone string) (types.JID, error) {
	jid, err := s.wm.ResolveJID(ctx, instanceID, phone)
	if err != nil {
		return types.JID{}, err
	}
	return *jid, nil
}

func (s *SGPService) processText(ctx context.Context, instanceID, toNumber, text string) error {
	jid, err := s.resolveJID(ctx, instanceID, toNumber)
	if err != nil {
		return fmt.Errorf("sgp: jid inválido %q: %w", toNumber, err)
	}
	res, err := s.wm.SendText(ctx, &whatsmiau.SendText{
		InstanceID: instanceID,
		RemoteJID:  &jid,
		Text:       text,
	})
	if err != nil {
		return err
	}
	s.wm.NotifySGPMessage(ctx, instanceID, jid.User, text, res.ID)
	return nil
}

func (s *SGPService) processBoleto(ctx context.Context, instanceID, toNumber, part string) error {
	m := boletoRe.FindStringSubmatch(part)
	if m == nil {
		return s.processText(ctx, instanceID, toNumber, part)
	}
	code := strings.TrimSpace(m[1])
	valor := strings.TrimSpace(m[2])
	tipo := "Boleto"
	if len(m) > 3 && strings.TrimSpace(m[3]) != "" {
		tipo = strings.TrimSpace(m[3])
	}
	// Formato: título + código na segunda mensagem para facilitar cópia
	text := fmt.Sprintf("📋 *Código do %s*\n💰 Valor: *%s*\n\n*Linha digitável:*\n%s", tipo, valor, code)
	return s.processText(ctx, instanceID, toNumber, text)
}

func (s *SGPService) processSendImg(ctx context.Context, instanceID, toNumber, part string) error {
	m := sendimgRe.FindStringSubmatch(part)
	if m == nil {
		return nil
	}
	imageURL := strings.TrimSpace(m[1])
	caption := ""
	if len(m) > 2 {
		caption = strings.TrimSpace(m[2])
	}
	jid, err := s.resolveJID(ctx, instanceID, toNumber)
	if err != nil {
		return err
	}
	res, err := s.wm.SendImage(ctx, &whatsmiau.SendImageRequest{
		InstanceID: instanceID,
		RemoteJID:  &jid,
		MediaURL:   imageURL,
		Caption:    caption,
		Mimetype:   "image/png",
	})
	if err != nil {
		return err
	}
	s.wm.NotifySGPMediaURL(ctx, instanceID, jid.User, imageURL, "image/png", "", caption, res.ID, "imageMessage")
	return nil
}

func (s *SGPService) processLink(ctx context.Context, instanceID, toNumber, part string) error {
	m := linkRe.FindStringSubmatch(part)
	if m == nil {
		return nil
	}
	linkURL := strings.TrimSpace(m[1])
	fileName := fmt.Sprintf("fatura-%d.pdf", rand.Intn(90000000)+10000000)
	if len(m) > 2 && strings.TrimSpace(m[2]) != "" {
		fn := strings.TrimSpace(m[2])
		fn = strings.ReplaceAll(fn, "/", "-")
		fn = strings.ReplaceAll(fn, " ", "")
		fileName = fn + ".pdf"
	}

	// Verificar se a URL realmente serve um PDF antes de tentar enviar como documento
	isPDF := false
	if resp, err := s.httpClient.Head(linkURL); err == nil {
		ct := resp.Header.Get("Content-Type")
		isPDF = strings.Contains(ct, "pdf")
		resp.Body.Close()
	}

	if isPDF {
		jid, err := s.resolveJID(ctx, instanceID, toNumber)
		if err != nil {
			return err
		}
		res, err := s.wm.SendDocument(ctx, &whatsmiau.SendDocumentRequest{
			InstanceID: instanceID,
			RemoteJID:  &jid,
			MediaURL:   linkURL,
			FileName:   fileName,
			Mimetype:   "application/pdf",
		})
		if err == nil {
			s.wm.NotifySGPMediaURL(ctx, instanceID, jid.User, linkURL, "application/pdf", fileName, "", res.ID, "documentMessage")
			return nil
		}
		zap.L().Warn("sgp: [link] PDF falhou no upload, enviando como texto", zap.Error(err))
	}

	// URL não é PDF direto (página HTML do boleto, etc.) — envia como texto
	// WhatsApp exibe preview do link automaticamente
	return s.processText(ctx, instanceID, toNumber, linkURL)
}

func (s *SGPService) processLinkCurto(ctx context.Context, instanceID, toNumber, part string) error {
	m := linkcurtoRe.FindStringSubmatch(part)
	if m == nil {
		return nil
	}
	original := strings.TrimSpace(m[1])
	shortened := s.encurtarLink(original)
	return s.processText(ctx, instanceID, toNumber, shortened)
}

func (s *SGPService) processQRCode(ctx context.Context, instanceID, toNumber, part string) error {
	m := qrcodeRe.FindStringSubmatch(part)
	if m == nil {
		return nil
	}
	content := strings.TrimSpace(m[1])

	png, err := qrcode.Encode(content, qrcode.Medium, 512)
	if err != nil {
		return fmt.Errorf("sgp: erro ao gerar qrcode: %w", err)
	}

	jid, err := s.resolveJID(ctx, instanceID, toNumber)
	if err != nil {
		return err
	}
	res, err := s.wm.SendImageBytes(ctx, instanceID, &jid, png, "image/png", "")
	if err != nil {
		return err
	}
	s.wm.NotifySGPMediaBytes(ctx, instanceID, jid.User, png, "image/png", "qrcode.png", "", res.ID, "imageMessage")
	return nil
}

func (s *SGPService) processLista(ctx context.Context, instanceID, toNumber string) error {
	text := "Como você prefere receber seu boleto?\n\n" +
		"🌱 Ajude a preservar o meio ambiente:\n\n" +
		"1️⃣  Via Aplicativo 📱\n" +
		"2️⃣  Via WhatsApp 💬\n" +
		"3️⃣  Boleto Físico 📄\n\n" +
		"Responda com o número da opção desejada."
	return s.processText(ctx, instanceID, toNumber, text)
}

func (s *SGPService) processTemplate(ctx context.Context, instanceID, toNumber, part string) error {
	m := templateRe.FindStringSubmatch(part)
	if m == nil {
		return nil
	}
	name := strings.TrimSpace(m[1])
	var params []string
	for _, p := range strings.Split(m[2], ";") {
		if t := strings.TrimSpace(p); t != "" {
			params = append(params, t)
		}
	}
	text := fmt.Sprintf("[Template: %s] %s", name, strings.Join(params, " | "))
	return s.processText(ctx, instanceID, toNumber, text)
}

// processBotoes sends a WhatsApp reply-button message.
// Format: [botoes=corpo|Botão1;Botão2;Botão3]
// With title: [botoes=título|corpo|Botão1;Botão2;Botão3]
func (s *SGPService) processBotoes(ctx context.Context, instanceID, toNumber, part string) error {
	m := botoesRe.FindStringSubmatch(part)
	if m == nil {
		return nil
	}
	content := m[1]

	segments := strings.Split(content, "|")
	var title, body, btnsStr string
	switch len(segments) {
	case 2:
		body = strings.TrimSpace(segments[0])
		btnsStr = strings.TrimSpace(segments[1])
	case 3:
		title = strings.TrimSpace(segments[0])
		body = strings.TrimSpace(segments[1])
		btnsStr = strings.TrimSpace(segments[2])
	default:
		return s.processText(ctx, instanceID, toNumber, body)
	}

	jid, err := s.resolveJID(ctx, instanceID, toNumber)
	if err != nil {
		return fmt.Errorf("sgp: jid inválido %q: %w", toNumber, err)
	}

	var buttons []whatsmiau.ButtonItem
	for i, bt := range strings.Split(btnsStr, ";") {
		bt = strings.TrimSpace(bt)
		if bt == "" || len(buttons) >= 3 {
			break
		}
		buttons = append(buttons, whatsmiau.ButtonItem{
			Type:        "reply",
			DisplayText: bt,
			ID:          fmt.Sprintf("btn_%d", i+1),
		})
	}

	if len(buttons) == 0 {
		return s.processText(ctx, instanceID, toNumber, body)
	}

	res, err := s.wm.SendButtons(ctx, &whatsmiau.SendButtonsRequest{
		InstanceID:  instanceID,
		RemoteJID:   &jid,
		Title:       title,
		Description: body,
		Buttons:     buttons,
	})
	if err != nil {
		return err
	}
	// Notifica Chatwoot com representação textual dos botões
	var notifyParts []string
	if title != "" {
		notifyParts = append(notifyParts, "*"+title+"*")
	}
	notifyParts = append(notifyParts, body)
	var btnLabels []string
	for _, b := range buttons {
		btnLabels = append(btnLabels, "[ "+b.DisplayText+" ]")
	}
	notifyParts = append(notifyParts, strings.Join(btnLabels, "  "))
	s.wm.NotifySGPMessage(ctx, instanceID, jid.User, strings.Join(notifyParts, "\n"), res.ID)
	return nil
}

func (s *SGPService) encurtarLink(originalURL string) string {
	resp, err := s.httpClient.Get("http://copiar.grbnet.com.br/shorten?codigo=" + originalURL)
	if err != nil {
		return originalURL
	}
	defer resp.Body.Close()
	var result struct {
		ShortURL string `json:"short_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.ShortURL == "" {
		return originalURL
	}
	return result.ShortURL
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
