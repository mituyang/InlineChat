package grpcserver

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "inlinechat/services/admin-service/internal/gen/adminv1"
	"inlinechat/services/admin-service/internal/model"
	"inlinechat/services/admin-service/internal/security"
	"inlinechat/services/admin-service/internal/service"
)

type AdminGatewayServer struct {
	adminv1.UnimplementedAdminGatewayServiceServer
	adminService *service.AdminService
	jwtSecret    []byte
	jwtIssuer    string
}

func New(adminService *service.AdminService, jwtSecret string, jwtIssuer string) *AdminGatewayServer {
	return &AdminGatewayServer{
		adminService: adminService,
		jwtSecret:    []byte(jwtSecret),
		jwtIssuer:    jwtIssuer,
	}
}

func (s *AdminGatewayServer) CreateSite(ctx context.Context, req *adminv1.CreateSiteRequest) (*adminv1.Site, error) {
	if _, err := s.requireAdmin(req.GetAuthorization()); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetName()) == "" || strings.TrimSpace(req.GetDomain()) == "" {
		return nil, status.Error(codes.InvalidArgument, "name and domain are required")
	}

	site, err := s.adminService.CreateSite(ctx, service.CreateSiteInput{
		Name:   req.GetName(),
		Domain: req.GetDomain(),
	})
	if err != nil {
		return nil, mapError(err)
	}

	return toSitePB(site), nil
}

func (s *AdminGatewayServer) ListSites(ctx context.Context, req *adminv1.ListSitesRequest) (*adminv1.ListSitesResponse, error) {
	if _, err := s.requireAdmin(req.GetAuthorization()); err != nil {
		return nil, err
	}

	limit := int(req.GetLimit())
	offset := int(req.GetOffset())
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		return nil, status.Error(codes.InvalidArgument, "invalid limit")
	}
	if offset < 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid offset")
	}

	items, err := s.adminService.ListSites(ctx, limit, offset)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}

	resp := &adminv1.ListSitesResponse{
		Items: make([]*adminv1.Site, 0, len(items)),
	}
	for i := range items {
		item := items[i]
		resp.Items = append(resp.Items, toSitePB(&item))
	}
	return resp, nil
}

func (s *AdminGatewayServer) CreateAgent(ctx context.Context, req *adminv1.CreateAgentRequest) (*adminv1.Agent, error) {
	claims, err := s.requireAdmin(req.GetAuthorization())
	if err != nil {
		return nil, err
	}
	if claims.Role != "super_admin" {
		return nil, status.Error(codes.PermissionDenied, "super_admin role required")
	}

	agent, svcErr := s.adminService.CreateAgent(ctx, service.CreateAgentInput{
		Email:       req.GetEmail(),
		Password:    req.GetPassword(),
		DisplayName: req.GetDisplayName(),
		Role:        req.GetRole(),
	})
	if svcErr != nil {
		return nil, mapError(svcErr)
	}

	return toAgentPB(agent), nil
}

func (s *AdminGatewayServer) ListAgents(ctx context.Context, req *adminv1.ListAgentsRequest) (*adminv1.ListAgentsResponse, error) {
	if _, err := s.requireAdmin(req.GetAuthorization()); err != nil {
		return nil, err
	}

	limit := int(req.GetLimit())
	offset := int(req.GetOffset())
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		return nil, status.Error(codes.InvalidArgument, "invalid limit")
	}
	if offset < 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid offset")
	}

	items, err := s.adminService.ListAgents(ctx, limit, offset)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}

	resp := &adminv1.ListAgentsResponse{
		Items: make([]*adminv1.Agent, 0, len(items)),
	}
	for i := range items {
		item := items[i]
		resp.Items = append(resp.Items, toAgentPB(&item))
	}
	return resp, nil
}

func (s *AdminGatewayServer) requireAdmin(authorization string) (*security.Claims, error) {
	token := parseBearerToken(authorization)
	if token == "" {
		return nil, status.Error(codes.Unauthenticated, "missing bearer token")
	}

	claims, err := security.ParseToken(s.jwtSecret, s.jwtIssuer, token)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	if claims.Role != "admin" && claims.Role != "super_admin" {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	return claims, nil
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

func mapError(err error) error {
	switch err {
	case service.ErrConflict:
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "required") || strings.Contains(msg, "invalid") {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		return status.Error(codes.Internal, "internal error")
	}
}

func toSitePB(site *model.Site) *adminv1.Site {
	if site == nil {
		return &adminv1.Site{}
	}
	return &adminv1.Site{
		Id:        site.ID,
		SiteId:    site.SiteID,
		Name:      site.Name,
		Domain:    site.Domain,
		WidgetKey: site.WidgetKey,
		Status:    site.Status,
		CreatedAt: site.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: site.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func toAgentPB(agent *model.Agent) *adminv1.Agent {
	if agent == nil {
		return &adminv1.Agent{}
	}
	return &adminv1.Agent{
		Id:          agent.ID,
		Email:       agent.Email,
		DisplayName: agent.DisplayName,
		Role:        agent.Role,
		Status:      agent.Status,
		CreatedAt:   agent.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   agent.UpdatedAt.Format(time.RFC3339Nano),
	}
}
