//go:build wireinject
// +build wireinject

package main

import (
	"github.com/google/wire"
	"github.com/nacl-org/nacl-cloud-go/internal/config"
	"github.com/nacl-org/nacl-cloud-go/internal/engineclient"
	"github.com/nacl-org/nacl-cloud-go/internal/handler"
	"github.com/nacl-org/nacl-cloud-go/internal/middleware"
	"github.com/nacl-org/nacl-cloud-go/internal/repository"
	"github.com/nacl-org/nacl-cloud-go/internal/service"
)

// InitializeApp resolves all service dependencies at compile-time and returns the bootstrapped App.
func InitializeApp() (*App, func(), error) {
	wire.Build(
		config.NewConfig,
		NewFiberApp,
		repository.NewPostgresDatabase,
		engineclient.NewEngineClient,
		repository.NewUserRepository,
		repository.NewWorkspaceRepository,
		repository.NewTelemetryRepository,
		service.NewUserService,
		service.NewWorkspaceService,
		service.NewTelemetryService,
		handler.NewUserHandler,
		handler.NewWorkspaceHandler,
		handler.NewTelemetryHandler,
		middleware.NewClerkAuthMiddleware,
		NewApp,
	)
	return nil, nil, nil
}
