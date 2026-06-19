package config

import (
	"log"
	"github.com/spf13/viper"
)

// Config holds all configuration properties for our cloud service.
type Config struct {
	Port          string
	DatabaseURL   string
	EngineAddr    string
	InboundSubnet string
	InboundPort   string
	SQLTemplate   string
}

// NewConfig compiles defaults and reads environment overrides via Viper.
func NewConfig() (*Config, error) {
	viper.SetDefault("PORT", "8080")
	viper.SetDefault("DATABASE_URL", "postgres://nacl_engine_user:nacl_password@127.0.0.1:5432/nacl_telemetry?sslmode=disable")
	viper.SetDefault("ENGINE_ADDR", "127.0.0.1:50051")
	viper.SetDefault("NACL_INBOUND_SUBNET", "192.0.2.0/24")
	viper.SetDefault("NACL_INBOUND_PORT", "5432")
	viper.SetDefault("NACL_SQL_TEMPLATE", "-- 1. Create dedicated migration user\nCREATE USER {user} WITH PASSWORD 'strong_password';\n\n-- 2. Grant DDL permissions\nGRANT CONNECT ON DATABASE {db} TO {user};\nGRANT USAGE, CREATE ON SCHEMA public TO {user};")

	viper.AutomaticEnv()

	cfg := &Config{
		Port:          viper.GetString("PORT"),
		DatabaseURL:   viper.GetString("DATABASE_URL"),
		EngineAddr:    viper.GetString("ENGINE_ADDR"),
		InboundSubnet: viper.GetString("NACL_INBOUND_SUBNET"),
		InboundPort:   viper.GetString("NACL_INBOUND_PORT"),
		SQLTemplate:   viper.GetString("NACL_SQL_TEMPLATE"),
	}

	log.Printf("Configuration loaded successfully. System PORT: %s, Engine Target: %s, Subnet: %s, Port: %s", cfg.Port, cfg.EngineAddr, cfg.InboundSubnet, cfg.InboundPort)
	return cfg, nil
}

