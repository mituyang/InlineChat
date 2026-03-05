package grpcserver

import (
	"context"
	"errors"
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
	// gRPC 层负责协议转换与鉴权门面，业务校验下沉到 adminService。
	adminService *service.AdminService
	jwtSecrets   [][]byte
	jwtIssuer    string
}

func New(adminService *service.AdminService, jwtSecret string, jwtPreviousSecret string, jwtIssuer string) *AdminGatewayServer {
	return &AdminGatewayServer{
		adminService: adminService,
		jwtSecrets:   buildJWTSecrets(jwtSecret, jwtPreviousSecret),
		jwtIssuer:    jwtIssuer,
	}
}

// CreateSite 仅允许管理员调用，创建站点并返回 widget_key。
func (s *AdminGatewayServer) CreateSite(ctx context.Context, req *adminv1.CreateSiteRequest) (*adminv1.Site, error) {
	claims, err := s.requireAdmin(ctx, req.GetAuthorization())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetSiteId()) == "" || strings.TrimSpace(req.GetName()) == "" || strings.TrimSpace(req.GetDomain()) == "" {
		return nil, status.Error(codes.InvalidArgument, "site_id name and domain are required")
	}

	site, err := s.adminService.CreateSiteWithActor(ctx, service.CreateSiteInput{
		SiteID: req.GetSiteId(),
		Name:   req.GetName(),
		Domain: req.GetDomain(),
	}, toActorContext(claims))
	if err != nil {
		return nil, mapError(err)
	}

	return toSitePB(site), nil
}

// UpdateSiteStatus 仅 super_admin 可执行，控制站点接入启停。
func (s *AdminGatewayServer) UpdateSiteStatus(ctx context.Context, req *adminv1.UpdateSiteStatusRequest) (*adminv1.Site, error) {
	claims, err := s.requireAdmin(ctx, req.GetAuthorization())
	if err != nil {
		return nil, err
	}
	if claims.Role != "super_admin" {
		return nil, status.Error(codes.PermissionDenied, "super_admin role required")
	}

	site, svcErr := s.adminService.UpdateSiteStatus(ctx, service.UpdateSiteStatusInput{
		SiteID: req.GetSiteId(),
		Status: req.GetStatus(),
	}, toActorContext(claims))
	if svcErr != nil {
		return nil, mapError(svcErr)
	}
	return toSitePB(site), nil
}

func (s *AdminGatewayServer) RotateSiteWidgetKey(ctx context.Context, req *adminv1.RotateSiteWidgetKeyRequest) (*adminv1.Site, error) {
	claims, err := s.requireAdmin(ctx, req.GetAuthorization())
	if err != nil {
		return nil, err
	}
	if claims.Role != "super_admin" {
		return nil, status.Error(codes.PermissionDenied, "super_admin role required")
	}

	site, svcErr := s.adminService.RotateSiteWidgetKey(ctx, service.RotateSiteWidgetKeyInput{
		SiteID: req.GetSiteId(),
	}, toActorContext(claims))
	if svcErr != nil {
		return nil, mapError(svcErr)
	}

	return toSitePB(site), nil
}

func (s *AdminGatewayServer) ListSites(ctx context.Context, req *adminv1.ListSitesRequest) (*adminv1.ListSitesResponse, error) {
	if _, err := s.requireAdmin(ctx, req.GetAuthorization()); err != nil {
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

func (s *AdminGatewayServer) GetSiteBySiteID(ctx context.Context, req *adminv1.GetSiteBySiteIDRequest) (*adminv1.Site, error) {
	siteID := strings.TrimSpace(req.GetSiteId())
	if siteID == "" {
		return nil, status.Error(codes.InvalidArgument, "site_id is required")
	}

	site, err := s.adminService.GetSiteBySiteID(ctx, siteID)
	if err != nil {
		return nil, mapError(err)
	}

	return toSitePB(site), nil
}

func (s *AdminGatewayServer) GetSiteByDomain(ctx context.Context, req *adminv1.GetSiteByDomainRequest) (*adminv1.Site, error) {
	domain := strings.TrimSpace(req.GetDomain())
	if domain == "" {
		return nil, status.Error(codes.InvalidArgument, "domain is required")
	}

	site, err := s.adminService.GetSiteByDomain(ctx, domain)
	if err != nil {
		return nil, mapError(err)
	}

	return toSitePB(site), nil
}

func (s *AdminGatewayServer) CreateAgent(ctx context.Context, req *adminv1.CreateAgentRequest) (*adminv1.Agent, error) {
	claims, err := s.requireAdmin(ctx, req.GetAuthorization())
	if err != nil {
		return nil, err
	}
	if claims.Role != "super_admin" {
		return nil, status.Error(codes.PermissionDenied, "super_admin role required")
	}

	agent, svcErr := s.adminService.CreateAgentWithActor(ctx, service.CreateAgentInput{
		AgentID:     req.GetAgentId(),
		Email:       req.GetEmail(),
		Password:    req.GetPassword(),
		DisplayName: req.GetDisplayName(),
		Role:        req.GetRole(),
	}, toActorContext(claims))
	if svcErr != nil {
		return nil, mapError(svcErr)
	}

	return toAgentPB(agent), nil
}

func (s *AdminGatewayServer) ListAgents(ctx context.Context, req *adminv1.ListAgentsRequest) (*adminv1.ListAgentsResponse, error) {
	if _, err := s.requireAdmin(ctx, req.GetAuthorization()); err != nil {
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

func (s *AdminGatewayServer) UpdateAgentStatus(ctx context.Context, req *adminv1.UpdateAgentStatusRequest) (*adminv1.Agent, error) {
	claims, err := s.requireAdmin(ctx, req.GetAuthorization())
	if err != nil {
		return nil, err
	}
	if claims.Role != "super_admin" {
		return nil, status.Error(codes.PermissionDenied, "super_admin role required")
	}

	agent, svcErr := s.adminService.UpdateAgentStatus(ctx, service.UpdateAgentStatusInput{
		AgentID: req.GetAgentId(),
		Status:  req.GetStatus(),
	}, toActorContext(claims))
	if svcErr != nil {
		return nil, mapError(svcErr)
	}
	return toAgentPB(agent), nil
}

func (s *AdminGatewayServer) ResetAgentPassword(ctx context.Context, req *adminv1.ResetAgentPasswordRequest) (*adminv1.Agent, error) {
	claims, err := s.requireAdmin(ctx, req.GetAuthorization())
	if err != nil {
		return nil, err
	}
	if claims.Role != "super_admin" {
		return nil, status.Error(codes.PermissionDenied, "super_admin role required")
	}

	agent, svcErr := s.adminService.ResetAgentPassword(ctx, service.ResetAgentPasswordInput{
		AgentID:     req.GetAgentId(),
		NewPassword: req.GetNewPassword(),
	}, toActorContext(claims))
	if svcErr != nil {
		return nil, mapError(svcErr)
	}
	return toAgentPB(agent), nil
}

func (s *AdminGatewayServer) ForceAgentLogout(ctx context.Context, req *adminv1.ForceAgentLogoutRequest) (*adminv1.Agent, error) {
	claims, err := s.requireAdmin(ctx, req.GetAuthorization())
	if err != nil {
		return nil, err
	}
	if claims.Role != "super_admin" {
		return nil, status.Error(codes.PermissionDenied, "super_admin role required")
	}

	agent, svcErr := s.adminService.ForceAgentLogout(ctx, service.ForceAgentLogoutInput{
		AgentID: req.GetAgentId(),
	}, toActorContext(claims))
	if svcErr != nil {
		return nil, mapError(svcErr)
	}
	return toAgentPB(agent), nil
}

func (s *AdminGatewayServer) ListAuditLogs(ctx context.Context, req *adminv1.ListAuditLogsRequest) (*adminv1.ListAuditLogsResponse, error) {
	if _, err := s.requireAdmin(ctx, req.GetAuthorization()); err != nil {
		return nil, err
	}

	items, svcErr := s.adminService.ListAuditLogs(ctx, service.ListAuditLogsInput{
		Limit:        int(req.GetLimit()),
		Offset:       int(req.GetOffset()),
		ActorAgentID: req.GetActorAgentId(),
		Action:       req.GetAction(),
		ResourceType: req.GetResourceType(),
	})
	if svcErr != nil {
		return nil, mapError(svcErr)
	}

	resp := &adminv1.ListAuditLogsResponse{
		Items: make([]*adminv1.AuditLog, 0, len(items)),
	}
	for i := range items {
		item := items[i]
		resp.Items = append(resp.Items, toAuditLogPB(&item))
	}
	return resp, nil
}

// requireAdmin 校验 bearer token、角色、以及数据库中的会话有效性。
func (s *AdminGatewayServer) requireAdmin(ctx context.Context, authorization string) (*security.Claims, error) {
	token := parseBearerToken(authorization)
	if token == "" {
		return nil, status.Error(codes.Unauthenticated, "missing bearer token")
	}

	claims, err := security.ParseTokenAny(s.jwtSecrets, s.jwtIssuer, token)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	if claims.Role != "admin" && claims.Role != "super_admin" {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	if s.adminService != nil {
		if err := s.adminService.ValidateAdminSession(ctx, claims.Role, claims.AgentID, claims.TokenVersion); err != nil {
			if errors.Is(err, service.ErrInvalidSession) {
				return nil, status.Error(codes.Unauthenticated, "invalid token")
			}
			return nil, status.Error(codes.Internal, "internal error")
		}
	}
	return claims, nil
}

// parseBearerToken 解析标准 Authorization: Bearer <token>。
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

// buildJWTSecrets 支持主/旧密钥并行验签，避免密钥轮转窗口期故障。
func buildJWTSecrets(primary string, previous string) [][]byte {
	out := make([][]byte, 0, 2)
	primaryText := strings.TrimSpace(primary)
	if primaryText != "" {
		out = append(out, []byte(primaryText))
	}
	previousText := strings.TrimSpace(previous)
	if previousText != "" && previousText != primaryText {
		out = append(out, []byte(previousText))
	}
	return out
}

// mapError 将领域错误映射到稳定 gRPC code，方便上游按状态处理。
func mapError(err error) error {
	switch err {
	case service.ErrConflict:
		return status.Error(codes.AlreadyExists, err.Error())
	case service.ErrNotFound:
		return status.Error(codes.NotFound, err.Error())
	default:
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "required") ||
			strings.Contains(msg, "invalid") ||
			strings.Contains(msg, "password") ||
			strings.Contains(msg, "status") ||
			strings.Contains(msg, "format") ||
			strings.Contains(msg, "length") {
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

func toAuditLogPB(auditLog *model.AuditLog) *adminv1.AuditLog {
	if auditLog == nil {
		return &adminv1.AuditLog{}
	}
	return &adminv1.AuditLog{
		Id:           auditLog.ID,
		ActorAgentId: auditLog.ActorAgentID,
		ActorEmail:   auditLog.ActorEmail,
		ActorRole:    auditLog.ActorRole,
		Action:       auditLog.Action,
		ResourceType: auditLog.ResourceType,
		ResourceId:   auditLog.ResourceID,
		Summary:      auditLog.Summary,
		Ip:           auditLog.IP,
		UserAgent:    auditLog.UserAgent,
		CreatedAt:    auditLog.CreatedAt.Format(time.RFC3339Nano),
	}
}

func toActorContext(claims *security.Claims) service.ActorContext {
	if claims == nil {
		return service.ActorContext{}
	}
	return service.ActorContext{
		AgentID: claims.AgentID,
		Email:   claims.Email,
		Role:    claims.Role,
	}
}
