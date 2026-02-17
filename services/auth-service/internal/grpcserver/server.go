package grpcserver

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "inlinechat/services/auth-service/internal/gen/authv1"
	"inlinechat/services/auth-service/internal/model"
	"inlinechat/services/auth-service/internal/service"
)

type AuthGatewayServer struct {
	authv1.UnimplementedAuthGatewayServiceServer
	authService *service.AuthService
}

func New(authService *service.AuthService) *AuthGatewayServer {
	return &AuthGatewayServer{authService: authService}
}

func (s *AuthGatewayServer) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.AuthResult, error) {
	if strings.TrimSpace(req.GetEmail()) == "" || strings.TrimSpace(req.GetPassword()) == "" {
		return nil, status.Error(codes.InvalidArgument, "email and password are required")
	}

	result, err := s.authService.Login(ctx, service.LoginInput{
		Email:    req.GetEmail(),
		Password: req.GetPassword(),
	})
	if err != nil {
		return nil, mapError(err)
	}

	return toAuthResultPB(result), nil
}

func (s *AuthGatewayServer) Me(_ context.Context, req *authv1.MeRequest) (*authv1.MeResponse, error) {
	token := parseBearerToken(req.GetAuthorization())
	if token == "" {
		return nil, status.Error(codes.Unauthenticated, "missing bearer token")
	}

	claims, err := s.authService.ParseToken(token)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}

	var exp int64
	if claims.ExpiresAt != nil {
		exp = claims.ExpiresAt.Time.Unix()
	}

	return &authv1.MeResponse{
		AgentId: claims.AgentID,
		Email:   claims.Email,
		Role:    claims.Role,
		Exp:     exp,
	}, nil
}

func mapError(err error) error {
	switch err {
	case service.ErrForbidden:
		return status.Error(codes.PermissionDenied, err.Error())
	case service.ErrConflict:
		return status.Error(codes.AlreadyExists, err.Error())
	case service.ErrInvalidCredential:
		return status.Error(codes.Unauthenticated, err.Error())
	case service.ErrUnauthorized:
		return status.Error(codes.PermissionDenied, err.Error())
	default:
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "required") || strings.Contains(msg, "invalid") {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		return status.Error(codes.Internal, "internal error")
	}
}

func toAuthResultPB(result *service.AuthResult) *authv1.AuthResult {
	if result == nil {
		return &authv1.AuthResult{}
	}
	return &authv1.AuthResult{
		Token: result.Token,
		Agent: toAgentPB(&result.Agent),
	}
}

func toAgentPB(agent *model.Agent) *authv1.Agent {
	if agent == nil {
		return &authv1.Agent{}
	}
	return &authv1.Agent{
		Id:          agent.ID,
		Email:       agent.Email,
		DisplayName: agent.DisplayName,
		Role:        agent.Role,
		Status:      agent.Status,
		CreatedAt:   agent.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   agent.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func parseBearerToken(header string) string {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
