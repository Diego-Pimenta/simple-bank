package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Diego-Pimenta/simple-bank/api"
	db "github.com/Diego-Pimenta/simple-bank/db/sqlc"
	_ "github.com/Diego-Pimenta/simple-bank/doc/statik"
	"github.com/Diego-Pimenta/simple-bank/gapi"
	"github.com/Diego-Pimenta/simple-bank/mail"
	"github.com/Diego-Pimenta/simple-bank/pb"
	"github.com/Diego-Pimenta/simple-bank/util"
	"github.com/Diego-Pimenta/simple-bank/worker"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rakyll/statik/fs"
	"github.com/rs/cors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/encoding/protojson"
)

var interruptSignals = []os.Signal{
	os.Interrupt,
	syscall.SIGTERM,
	syscall.SIGINT,
}

func main() {
	config, err := util.LoadConfig(".")
	if err != nil {
		log.Fatal().Err(err).Msg("cannot load config")
	}

	if config.Environment == "development" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	ctx, stop := signal.NotifyContext(context.Background(), interruptSignals...)
	defer stop()

	connPool, err := pgxpool.New(ctx, config.DBSource)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot connect to db")
	}

	runDBMigration(config.MigrationURL, config.DBSource)

	store := db.NewStore(connPool)

	redisOpt := asynq.RedisClientOpt{
		Addr: config.RedisAddress,
	}

	taskDistributor := worker.NewRedisTaskDistributor(redisOpt)

	waitGroup, ctx := errgroup.WithContext(ctx)

	runTaskProcessor(ctx, waitGroup, config, redisOpt, store)
	runGatewayServer(ctx, waitGroup, config, store, taskDistributor)
	runGrpcServer(ctx, waitGroup, config, store, taskDistributor)

	err = waitGroup.Wait()
	if err != nil {
		log.Fatal().Err(err).Msg("error from wait group")
	}
}

func runDBMigration(migrationURL, dbSource string) {
	migration, err := migrate.New(migrationURL, dbSource)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot create a new migrate instance")
	}

	if err = migration.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatal().Err(err).Msg("failed to run migrate up")
	}

	log.Info().Msgf("db migrated successfully")
}

func runTaskProcessor(
	ctx context.Context,
	waitGroup *errgroup.Group,
	config util.Config,
	redisOpt asynq.RedisClientOpt,
	store db.Store,
) {
	mailer := mail.NewGmailSender(config.EmailSenderName, config.EmailSenderAddress, config.EmailSenderPassword)
	taskProcessor := worker.NewRedisTaskProcessor(redisOpt, store, mailer)

	log.Info().Msgf("start task processor")

	err := taskProcessor.Start()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to start task processor")
	}

	waitGroup.Go(func() error {
		<-ctx.Done()
		log.Info().Msg("graceful shutdown task processor")

		taskProcessor.Shutdown()
		log.Info().Msg("task processor is stopped")

		return nil
	})
}

func runGrpcServer(
	ctx context.Context,
	waitGroup *errgroup.Group,
	config util.Config,
	store db.Store,
	taskDistributor worker.TaskDistributor,
) {
	server, err := gapi.NewServer(config, store, taskDistributor)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot create server")
	}

	grpcLogger := grpc.UnaryInterceptor(gapi.GrpcLogger)
	grpcServer := grpc.NewServer(grpcLogger)
	pb.RegisterServiceServer(grpcServer, server)
	reflection.Register(grpcServer)

	listener, err := net.Listen("tcp", config.GRPCServerAddress)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot create listener")
	}

	waitGroup.Go(func() error {
		log.Info().Msgf("start gRPC server at %s", listener.Addr().String())

		err = grpcServer.Serve(listener)
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			log.Error().Err(err).Msg("gRPC server failed to serve")
			return err
		}

		return nil
	})

	waitGroup.Go(func() error {
		<-ctx.Done()
		log.Info().Msg("graceful shutdown gRPC server")

		grpcServer.GracefulStop()
		log.Info().Msg("gRPC server is stopped")

		return nil
	})
}

func runGatewayServer(
	ctx context.Context,
	waitGroup *errgroup.Group,
	config util.Config,
	store db.Store,
	taskDistributor worker.TaskDistributor,
) {
	server, err := gapi.NewServer(config, store, taskDistributor)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot create server: ")
	}

	jsonOption := runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
		MarshalOptions: protojson.MarshalOptions{
			UseProtoNames: true,
		},
		UnmarshalOptions: protojson.UnmarshalOptions{
			DiscardUnknown: true,
		},
	})

	grpcMux := runtime.NewServeMux(jsonOption)

	err = pb.RegisterServiceHandlerServer(ctx, grpcMux, server)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot register handler server: ")
	}

	mux := http.NewServeMux()
	mux.Handle("/", grpcMux)

	statikFS, err := fs.New()
	if err != nil {
		log.Fatal().Err(err).Msg("cannot create statik fs")
	}

	swaggerHandler := http.StripPrefix("/swagger/", http.FileServer(statikFS))
	mux.Handle("/swagger/", swaggerHandler)

	c := cors.New(cors.Options{
		AllowedOrigins: config.AllowedOrigins,
		AllowedMethods: []string{
			http.MethodHead,
			http.MethodOptions,
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
		},
		AllowedHeaders: []string{
			"Content-Type",
			"Authorization",
		},
		AllowCredentials: true,
	})
	handler := c.Handler(gapi.HttpLogger(mux))
	httpServer := &http.Server{
		Handler: handler,
		Addr:    config.HTTPServerAddress,
	}

	waitGroup.Go(func() error {
		log.Info().Msgf("start HTTP gateway server at %s", httpServer.Addr)

		err = httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("HTTP gateway server failed to serve")
			return err
		}

		return nil
	})

	waitGroup.Go(func() error {
		<-ctx.Done()
		log.Info().Msg("graceful shutdown HTTP gateway server")

		err := httpServer.Shutdown(context.Background())
		if err != nil {
			log.Error().Err(err).Msg("failed to shutdown HTTP gateway server")
			return err
		}

		log.Info().Msg("HTTP gateway server is stopped")
		return nil
	})
}

func runGinServer(config util.Config, store db.Store) {
	server, err := api.NewServer(config, store)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot create server")
	}

	err = server.Start(config.HTTPServerAddress)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot start HTTP server")
	}
}
