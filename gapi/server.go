package gapi

import (
	"fmt"

	db "github.com/Diego-Pimenta/simple-bank/db/sqlc"
	"github.com/Diego-Pimenta/simple-bank/pb"
	"github.com/Diego-Pimenta/simple-bank/token"
	"github.com/Diego-Pimenta/simple-bank/util"
)

// Server serves HTTP requests for our banking service
type Server struct {
	pb.UnimplementedServiceServer
	config     util.Config
	store      db.Store
	tokenMaker token.Maker
}

// NewServer creates a new gRPC server
func NewServer(config util.Config, store db.Store) (*Server, error) {
	// tokenMaker, err := token.NewJWTMaker(config.TokenSymmetricKey)
	tokenMaker, err := token.NewPasetoMaker(config.TokenSymmetricKey)
	if err != nil {
		return nil, fmt.Errorf("cannot create token maker: %w", err)
	}

	server := &Server{
		config:     config,
		store:      store,
		tokenMaker: tokenMaker,
	}

	return server, nil
}
