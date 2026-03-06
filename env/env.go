package env

import (
	"fmt"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type E struct {
	Port           string `env:"PORT" envDefault:"8080"`
	DebugMode      bool   `env:"DEBUG_MODE" envDefault:"false"`
	DebugWhatsmeow bool   `env:"DEBUG_WHATSMEOW" envDefault:"false"`

	ServerURL  string `env:"SERVER_URL" envDefault:"http://localhost"`
	ServerPort string `env:"SERVER_PORT" envDefault:""`

	RedisURL      string `env:"REDIS_URL" envDefault:"localhost:6379"`
	RedisPassword string `env:"REDIS_PASSWORD"`
	RedisTLS      bool   `env:"REDIS_TLS" envDefault:"false"`

	ApiKey    string `env:"API_KEY" envDefault:""`
	DBDialect string `env:"DIALECT_DB" envDefault:"sqlite3"`
	DBURL     string `env:"DB_URL" envDefault:"file:data.db?_foreign_keys=on"`

	GCSEnabled bool   `env:"GCS_ENABLED" envDefault:"false"`
	GCSBucket  string `env:"GCS_BUCKET" envDefault:"whatsmiau"`
	GCSURL     string `env:"GCS_URL" envDefault:"https://storage.googleapis.com"`

	GCL          string `json:"GCL_APP_NAME" envDefault:"whatsmiau-br-1"`
	GCLEnabled   bool   `json:"GCL_ENABLED" envDefault:"false"`
	GCLProjectID string `json:"GCL_PROJECT_ID"`

	EmitterBufferSize    int `env:"EMITTER_BUFFER_SIZE" envDefault:"2048"`
	HandlerSemaphoreSize int `env:"HANDLER_SEMAPHORE_SIZE" envDefault:"512"`

	ProxyAddresses []string `env:"PROXY_ADDRESSES" envDefault:""`
	ProxyStrategy  string   `env:"PROXY_STRATEGY" envDefault:"RANDOM"`
	ProxyNoMedia   bool     `env:"PROXY_NO_MEDIA" envDefault:"false"`

	// Usado pelo ChatwootService para verificação de duplicatas via PostgreSQL
	ChatwootImportDatabaseConnectionURI string `env:"CHATWOOT_IMPORT_DATABASE_CONNECTION_URI" envDefault:""`
}

// GetServerURL retorna a URL base do servidor.
// Se SERVER_PORT estiver definida, inclui a porta.
func (e *E) GetServerURL() string {
	if e.ServerPort == "" {
		return e.ServerURL
	}
	return fmt.Sprintf("%s:%s", e.ServerURL, e.ServerPort)
}

var Env E

func Load() error {
	_ = godotenv.Load(".env")
	err := env.Parse(&Env)
	return err
}
