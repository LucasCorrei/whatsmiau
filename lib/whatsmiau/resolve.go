package whatsmiau

import (
	"context"
	"regexp"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.uber.org/zap"
)

const JIDCacheTTL = 30 * 24 * time.Hour

var cleanPhoneRe = regexp.MustCompile(`\D`)

// ResolveJID converts a phone number (or existing JID string) to a WhatsApp JID.
//
// For Brazilian numbers where the DDD+9 prefix is ambiguous (55+DDD+9+8 digits vs
// 55+DDD+8 digits), it calls IsOnWhatsApp — the same /usync query every WhatsApp
// client uses when opening a new chat — to find which variant is actually registered.
//
// Results are cached in Redis for 30 days, so the network lookup happens at most
// once per number per instance. Subsequent sends to the same number are instant.
func (s *Whatsmiau) ResolveJID(ctx context.Context, instanceID, number string) (*types.JID, error) {
	// Already a full JID — parse directly, no lookup needed
	if strings.Contains(number, "@") {
		jid, err := types.ParseJID(number)
		if err != nil {
			return nil, err
		}
		return &jid, nil
	}

	clean := cleanPhoneRe.ReplaceAllString(number, "")

	// Check Redis cache
	cacheKey := "wm_jid:" + instanceID + ":" + clean
	if cached, err := s.redis.Get(ctx, cacheKey).Result(); err == nil && cached != "" {
		jid, err := types.ParseJID(cached + "@s.whatsapp.net")
		if err == nil {
			return &jid, nil
		}
	}

	// Brazilian DDD+9 ambiguity:
	//   13 digits = 55 + 2-DDD + 9 + 8 digits → may be stored without the 9
	//   12 digits = 55 + 2-DDD + 8 digits     → may be stored with the 9
	var candidates []string
	if len(clean) == 13 && strings.HasPrefix(clean, "55") && clean[4] == '9' {
		candidates = []string{clean, clean[:4] + clean[5:]} // with-9 first
	} else if len(clean) == 12 && strings.HasPrefix(clean, "55") {
		candidates = []string{clean[:4] + "9" + clean[4:], clean} // with-9 first
	}

	if len(candidates) > 0 {
		if client, ok := s.clients.Load(instanceID); ok {
			results, err := client.IsOnWhatsApp(ctx, candidates)
			if err == nil {
				for _, r := range results {
					if r.IsIn {
						s.redis.Set(ctx, cacheKey, r.JID.User, JIDCacheTTL)
						return &r.JID, nil
					}
				}
			} else {
				zap.L().Warn("ResolveJID: IsOnWhatsApp failed, using original number",
					zap.String("instanceID", instanceID),
					zap.String("phone", clean),
					zap.Error(err))
			}
		}
	}

	// Fallback: use number as-is and cache to avoid repeated lookups
	s.redis.Set(ctx, cacheKey, clean, JIDCacheTTL)
	jid, err := types.ParseJID(clean + "@s.whatsapp.net")
	if err != nil {
		return nil, err
	}
	return &jid, nil
}
