package api

import (
	"net/http"
	"null-core/internal/api/middleware"
	"null-core/internal/gen/null/v1/nullv1connect"
	"null-core/internal/service"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"connectrpc.com/grpcreflect"
	"github.com/charmbracelet/log"
)

type Server struct {
	services    *service.Services
	log         *log.Logger
	healthCheck grpchealth.Checker
}

func NewServer(services *service.Services, logger *log.Logger) *Server {
	healthCheck := grpchealth.NewStaticChecker(
		"null.v1.UserService",
		"null.v1.AccountService",
		"null.v1.TransactionService",
		"null.v1.CategoryService",
		"null.v1.RuleService",
		"null.v1.DashboardService",
		"null.v1.ReceiptService",
	)

	return &Server{
		services:    services,
		log:         logger,
		healthCheck: healthCheck,
	}
}

func (s *Server) SetServingStatus(service string, healthy bool) {
	if checker, ok := s.healthCheck.(*grpchealth.StaticChecker); ok {
		if healthy {
			checker.SetStatus(service, grpchealth.StatusServing)
		} else {
			checker.SetStatus(service, grpchealth.StatusNotServing)
		}
	}
}

func (s *Server) GetHandler(authConfig *middleware.AuthConfig) http.Handler {
	if authConfig == nil {
		s.log.Fatal("auth configuration is required")
	}

	mux := http.NewServeMux()
	s.registerServices(mux)

	stack := middleware.CreateStack(
		middleware.CORS(),
		middleware.Auth(authConfig, s.log),
		middleware.UserContext(),
	)

	return stack(mux)
}

func (s *Server) registerServices(mux *http.ServeMux) {
	healthPath, healthHandler := grpchealth.NewHandler(s.healthCheck)
	mux.Handle(healthPath, healthHandler)

	reflector := grpcreflect.NewStaticReflector(
		"null.v1.UserService",
		"null.v1.AccountService",
		"null.v1.TransactionService",
		"null.v1.CategoryService",
		"null.v1.RuleService",
		"null.v1.DashboardService",
		"null.v1.ReceiptService",
	)
	reflectPath, reflectHandler := grpcreflect.NewHandlerV1(reflector)
	mux.Handle(reflectPath, reflectHandler)
	reflectPathAlpha, reflectHandlerAlpha := grpcreflect.NewHandlerV1Alpha(reflector)
	mux.Handle(reflectPathAlpha, reflectHandlerAlpha)

	interceptors := connect.WithInterceptors(
		middleware.ConnectLoggingInterceptor(s.log),
		middleware.EnsureUserInterceptor(s.services.Users, s.log),
		middleware.UserIDExtractor(),
	)

	path, handler := nullv1connect.NewUserServiceHandler(s, interceptors)
	mux.Handle(path, handler)

	path, handler = nullv1connect.NewAccountServiceHandler(s, interceptors)
	mux.Handle(path, handler)

	path, handler = nullv1connect.NewTransactionServiceHandler(s, interceptors)
	mux.Handle(path, handler)

	path, handler = nullv1connect.NewCategoryServiceHandler(s, interceptors)
	mux.Handle(path, handler)

	path, handler = nullv1connect.NewRuleServiceHandler(s, interceptors)
	mux.Handle(path, handler)

	path, handler = nullv1connect.NewDashboardServiceHandler(s, interceptors)
	mux.Handle(path, handler)

	path, handler = nullv1connect.NewReceiptServiceHandler(s, interceptors)
	mux.Handle(path, handler)

	s.log.Info("all connect-go services registered",
		"health_endpoint", healthPath,
	)
}
