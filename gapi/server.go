package gapi

import (
	"fmt"

	db "github.com/Diego-Pimenta/simple-bank/db/sqlc"
	"github.com/Diego-Pimenta/simple-bank/pb"
	"github.com/Diego-Pimenta/simple-bank/token"
	"github.com/Diego-Pimenta/simple-bank/util"
	"github.com/Diego-Pimenta/simple-bank/worker"
)

// Server serves HTTP requests for our banking service
type Server struct {
	pb.UnimplementedServiceServer
	config          util.Config
	store           db.Store
	tokenMaker      token.Maker
	taskDistributor worker.TaskDistributor
}

// NewServer creates a new gRPC server
func NewServer(config util.Config, store db.Store, taskDistributor worker.TaskDistributor) (*Server, error) {
	// tokenMaker, err := token.NewJWTMaker(config.TokenSymmetricKey)
	tokenMaker, err := token.NewPasetoMaker(config.TokenSymmetricKey)
	if err != nil {
		return nil, fmt.Errorf("cannot create token maker: %w", err)
	}

	server := &Server{
		config:          config,
		store:           store,
		tokenMaker:      tokenMaker,
		taskDistributor: taskDistributor,
	}

	return server, nil
}
