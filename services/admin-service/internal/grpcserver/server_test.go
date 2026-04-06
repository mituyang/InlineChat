package grpcserver

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "inlinechat/services/admin-service/internal/gen/adminv1"
	"inlinechat/services/admin-service/internal/service"
)

func TestParseBearerToken(t *testing.T) {
	if got := parseBearerToken("Bearer token_abc"); got != "token_abc" {
		t.Fatalf("unexpected token: %q", got)
	}
	if got := parseBearerToken("bearer token_abc"); got != "token_abc" {
		t.Fatalf("unexpected token for lowercase bearer: %q", got)
	}
	if got := parseBearerToken("invalid"); got != "" {
		t.Fatalf("expected empty token, got: %q", got)
	}
}

func TestMapError(t *testing.T) {
	assertCode := func(t *testing.T, err error, expected codes.Code) {
		t.Helper()
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("expected grpc status error, got: %v", err)
		}
		if st.Code() != expected {
			t.Fatalf("expected code %s, got %s", expected, st.Code())
		}
	}

	assertCode(t, mapError(service.ErrConflict), codes.AlreadyExists)
	assertCode(t, mapError(service.ErrNotFound), codes.NotFound)
	assertCode(t, mapError(errors.New("domain is required")), codes.InvalidArgument)
	assertCode(t, mapError(errors.New("db timeout")), codes.Internal)
}

func TestGetSiteByDomainValidateRequest(t *testing.T) {
	s := New(nil, "secret", "", "issuer")
	_, err := s.GetSiteByDomain(context.Background(), &adminv1.GetSiteByDomainRequest{})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got: %v", err)
	}
}

func TestGetSiteBySiteIDValidateRequest(t *testing.T) {
	s := New(nil, "secret", "", "issuer")
	_, err := s.GetSiteBySiteID(context.Background(), &adminv1.GetSiteBySiteIDRequest{})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got: %v", err)
	}
}

func TestUpdateSiteRequireAuthorization(t *testing.T) {
	s := New(nil, "secret", "", "issuer")
	_, err := s.UpdateSite(context.Background(), &adminv1.UpdateSiteRequest{})
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated, got: %v", err)
	}
}
