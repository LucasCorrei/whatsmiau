package env

import (
	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type E struct {
	Port           string `env:"PORT" envDefault:"8080"`
	DebugMode      bool   `env:"DEBUG_MODE" envDefault:"false"`
	DebugWhatsmeow bool   `env:"DEBUG_WHATSMEOW" envDefault:"false"`
	
	RedisURL      string `env:"REDIS_URL" envDefault:"localhost:6379"`
	RedisPassword string `env:"REDIS_PASSWORD"`
	RedisTLS      bool   `env:"REDIS_TLS" envDefault:"false"`
	
	ApiKey    string `env:"API_KEY" envDefault:""`
	DBDialect string `env:"DIALECT_DB" envDefault:"sqlite3"`                   // sqlite3 or postgres
	DBURL     string `env:"DB_URL" envDefault:"file:data.db?_foreign_keys=on"` // "postgres://<user>:<pass>@<host>:<port>/<DB>?sslmode=disable
	
	GCSEnabled bool   `env:"GCS_ENABLED" envDefault:"false"`
	GCSBucket  string `env:"GCS_BUCKET" envDefault:"whatsmiau"`
	GCSURL     string `env:"GCS_URL" envDefault:"https://storage.googleapis.com"`
	
	GCL          string `json:"GCL_APP_NAME" envDefault:"whatsmiau-br-1"`
	GCLEnabled   bool   `json:"GCL_ENABLED" envDefault:"false"`
	GCLProjectID string `json:"GCL_PROJECT_ID"`
	
	EmitterBufferSize    int `env:"EMITTER_BUFFER_SIZE" envDefault:"2048"`
	HandlerSemaphoreSize int `env:"HANDLER_SEMAPHORE_SIZE" envDefault:"512"`
	
	ProxyAddresses []string `env:"PROXY_ADDRESSES" envDefault:""`      // random choices proxies ex: <SOCKS5|HTTP|HTTPS>://<username>:<password>@<host>:<port>
	ProxyStrategy  string   `env:"PROXY_STRATEGY" envDefault:"RANDOM"` // todo: implement BALANCED
	ProxyNoMedia   bool     `env:"PROXY_NO_MEDIA" envDefault:"false"`
	
	// Chatwoot Configuration
	ChatwootEnabled                      bool   `env:"CHATWOOT_ENABLED" envDefault:"false"`
	ChatwootMessageRead                  bool   `env:"CHATWOOT_MESSAGE_READ" envDefault:"true"`
	ChatwootMessageDelete                bool   `env:"CHATWOOT_MESSAGE_DELETE" envDefault:"false"`
	ChatwootBotContact                   bool   `env:"CHATWOOT_BOT_CONTACT" envDefault:"true"`
	ChatwootImportDatabaseConnectionURI  string `env:"CHATWOOT_IMPORT_DATABASE_CONNECTION_URI" envDefault:""`
	ChatwootImportPlaceholderMediaMessage bool   `env:"CHATWOOT_IMPORT_PLACEHOLDER_MEDIA_MESSAGE" envDefault:"false"`
	
	// Chatwoot Instance Config (podem vir de outra fonte também)
	ChatwootURL       string `env:"CHATWOOT_URL" envDefault:""`
	ChatwootAccountID string `env:"CHATWOOT_ACCOUNT_ID" envDefault:""`
	ChatwootToken     string `env:"CHATWOOT_TOKEN" envDefault:""`
	ChatwootInboxID   int    `env:"CHATWOOT_INBOX_ID" envDefault:"0"`
}

var Env E

func Load() error {
	_ = godotenv.Load(".env")
	err := env.Parse(&Env)
	return err
}
