package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"inlinechat/services/chat-service/internal/model"
	"inlinechat/services/chat-service/internal/repository"
)

type fakeConversationRepository struct {
	items  map[uint64]*model.Conversation
	nextID uint64
}

func newFakeConversationRepository(seed map[uint64]*model.Conversation) *fakeConversationRepository {
	items := make(map[uint64]*model.Conversation, len(seed))
	var maxID uint64
	for id, conv := range seed {
		items[id] = cloneConversation(conv)
		if id > maxID {
			maxID = id
		}
	}
	return &fakeConversationRepository{
		items:  items,
		nextID: maxID + 1,
	}
}

func (r *fakeConversationRepository) Create(_ context.Context, conversation *model.Conversation) error {
	if conversation.ID == 0 {
		conversation.ID = r.nextID
		r.nextID++
	}
	r.items[conversation.ID] = cloneConversation(conversation)
	return nil
}

func (r *fakeConversationRepository) GetByID(_ context.Context, id uint64) (*model.Conversation, error) {
	item, ok := r.items[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return cloneConversation(item), nil
}

func (r *fakeConversationRepository) List(_ context.Context, filter repository.ListConversationsFilter) ([]model.Conversation, error) {
	out := make([]model.Conversation, 0, len(r.items))
	for _, item := range r.items {
		if filter.Status != "" && item.Status != filter.Status {
			continue
		}
		if filter.SiteID != "" && item.SiteID != filter.SiteID {
			continue
		}
		if filter.UnassignedOnly && item.AssignedAgentID != nil {
			continue
		}
		if filter.AssignedAgentID != nil {
			if item.AssignedAgentID == nil || *item.AssignedAgentID != *filter.AssignedAgentID {
				continue
			}
		}
		out = append(out, *cloneConversation(item))
	}
	return out, nil
}

func (r *fakeConversationRepository) Mutate(_ context.Context, id uint64, mutation repository.ConversationMutation) (*model.Conversation, error) {
	current, ok := r.items[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	working := cloneConversation(current)

	changed, err := mutation(working)
	if err != nil {
		return nil, err
	}
	if changed {
		r.items[id] = cloneConversation(working)
	}
	return cloneConversation(r.items[id]), nil
}

type fakeMessageRepository struct {
	items  map[uint64][]model.Message
	nextID uint64
}

func newFakeMessageRepository() *fakeMessageRepository {
	return &fakeMessageRepository{
		items:  make(map[uint64][]model.Message),
		nextID: 1,
	}
}

func (r *fakeMessageRepository) Create(_ context.Context, message *model.Message) error {
	if message.ID == 0 {
		message.ID = r.nextID
		r.nextID++
	}
	copyMessage := *message
	r.items[message.ConversationID] = append(r.items[message.ConversationID], copyMessage)
	return nil
}

func (r *fakeMessageRepository) ListByConversation(_ context.Context, conversationID uint64, limit int, beforeID uint64) ([]model.Message, error) {
	messages := r.items[conversationID]
	out := make([]model.Message, 0, len(messages))
	for _, msg := range messages {
		if beforeID > 0 && msg.ID >= beforeID {
			continue
		}
		out = append(out, msg)
	}
	if limit > 0 && len(out) > limit {
		return out[:limit], nil
	}
	return out, nil
}

type fakePublisher struct {
	calls       int
	lastMessage *model.Message
	err         error
}

func (p *fakePublisher) PublishMessageCreated(_ context.Context, message *model.Message) error {
	p.calls++
	copyMessage := *message
	p.lastMessage = &copyMessage
	return p.err
}

func cloneConversation(c *model.Conversation) *model.Conversation {
	if c == nil {
		return nil
	}
	cp := *c
	if c.AssignedAgentID != nil {
		id := *c.AssignedAgentID
		cp.AssignedAgentID = &id
	}
	if c.ClosedAt != nil {
		closedAt := *c.ClosedAt
		cp.ClosedAt = &closedAt
	}
	if c.ClosedByAgentID != nil {
		closedBy := *c.ClosedByAgentID
		cp.ClosedByAgentID = &closedBy
	}
	return &cp
}

func testChatServiceWithConversations(seed map[uint64]*model.Conversation) (*ChatService, *fakeConversationRepository, *fakeMessageRepository, *fakePublisher) {
	conversationRepo := newFakeConversationRepository(seed)
	messageRepo := newFakeMessageRepository()
	publisher := &fakePublisher{}
	svc := New(conversationRepo, messageRepo, zap.NewNop(), publisher)
	return svc, conversationRepo, messageRepo, publisher
}

func TestClaimConversationSuccess(t *testing.T) {
	svc, repo, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open"},
	})

	out, err := svc.ClaimConversation(context.Background(), ClaimConversationInput{
		ConversationID: 1,
		AgentID:        7,
	})
	if err != nil {
		t.Fatalf("ClaimConversation failed: %v", err)
	}
	if out.AssignedAgentID == nil || *out.AssignedAgentID != 7 {
		t.Fatalf("unexpected assigned_agent_id: %+v", out.AssignedAgentID)
	}

	stored := repo.items[1]
	if stored.AssignedAgentID == nil || *stored.AssignedAgentID != 7 {
		t.Fatalf("conversation not persisted: %+v", stored.AssignedAgentID)
	}
}

func TestClaimConversationAlreadyClaimed(t *testing.T) {
	owner := uint64(9)
	svc, _, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open", AssignedAgentID: &owner},
	})

	_, err := svc.ClaimConversation(context.Background(), ClaimConversationInput{
		ConversationID: 1,
		AgentID:        7,
	})
	if !errors.Is(err, ErrConversationAlreadyClaimed) {
		t.Fatalf("expected ErrConversationAlreadyClaimed, got %v", err)
	}
}

func TestTransferConversationForbiddenForNonOwnerAgent(t *testing.T) {
	owner := uint64(9)
	svc, _, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open", AssignedAgentID: &owner},
	})

	_, err := svc.TransferConversation(context.Background(), TransferConversationInput{
		ConversationID: 1,
		ActorAgentID:   7,
		ActorRole:      "agent",
		ToAgentID:      8,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestTransferConversationAllowedForSuperAdmin(t *testing.T) {
	owner := uint64(9)
	svc, repo, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open", AssignedAgentID: &owner},
	})

	out, err := svc.TransferConversation(context.Background(), TransferConversationInput{
		ConversationID: 1,
		ActorAgentID:   7,
		ActorRole:      "super_admin",
		ToAgentID:      8,
	})
	if err != nil {
		t.Fatalf("TransferConversation failed: %v", err)
	}
	if out.AssignedAgentID == nil || *out.AssignedAgentID != 8 {
		t.Fatalf("unexpected assigned_agent_id: %+v", out.AssignedAgentID)
	}
	if repo.items[1].AssignedAgentID == nil || *repo.items[1].AssignedAgentID != 8 {
		t.Fatalf("conversation not persisted")
	}
}

func TestCloseConversationForbiddenForNonOwnerAgent(t *testing.T) {
	owner := uint64(9)
	svc, _, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open", AssignedAgentID: &owner},
	})

	_, err := svc.CloseConversation(context.Background(), CloseConversationInput{
		ConversationID: 1,
		ActorAgentID:   7,
		ActorRole:      "agent",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestCloseConversationByOwnerSuccess(t *testing.T) {
	owner := uint64(7)
	svc, repo, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open", AssignedAgentID: &owner},
	})

	out, err := svc.CloseConversation(context.Background(), CloseConversationInput{
		ConversationID: 1,
		ActorAgentID:   7,
		ActorRole:      "agent",
	})
	if err != nil {
		t.Fatalf("CloseConversation failed: %v", err)
	}
	if out.Status != "closed" {
		t.Fatalf("expected status closed, got %s", out.Status)
	}
	if out.ClosedAt == nil {
		t.Fatalf("closed_at should be set")
	}
	if out.ClosedByAgentID == nil || *out.ClosedByAgentID != 7 {
		t.Fatalf("unexpected closed_by_agent_id: %+v", out.ClosedByAgentID)
	}

	stored := repo.items[1]
	if stored.Status != "closed" || stored.ClosedAt == nil {
		t.Fatalf("close mutation not persisted")
	}
}

func TestCreateMessageVisitorTokenMismatch(t *testing.T) {
	svc, _, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_expected", Status: "open"},
	})

	_, err := svc.CreateMessage(context.Background(), CreateMessageInput{
		ConversationID: 1,
		SenderType:     "visitor",
		SenderID:       "",
		Content:        "hello",
		ClientMsgID:    "c1",
		VisitorToken:   "vt_other",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if err.Error() != "visitor token does not match conversation" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateMessagePublishEvent(t *testing.T) {
	svc, _, messageRepo, publisher := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open"},
	})

	out, err := svc.CreateMessage(context.Background(), CreateMessageInput{
		ConversationID: 1,
		SenderType:     "agent",
		SenderID:       "7",
		Content:        "您好，我来协助您。",
		ClientMsgID:    "c2",
	})
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}
	if out.ID == 0 {
		t.Fatalf("message id should be assigned")
	}
	if publisher.calls != 1 {
		t.Fatalf("expected publisher calls=1, got %d", publisher.calls)
	}
	if publisher.lastMessage == nil || publisher.lastMessage.ID != out.ID {
		t.Fatalf("unexpected published message: %+v", publisher.lastMessage)
	}
	if len(messageRepo.items[1]) != 1 {
		t.Fatalf("message not persisted")
	}
}

func TestCloseConversationIdempotentWhenAlreadyClosed(t *testing.T) {
	owner := uint64(7)
	now := time.Now()
	svc, repo, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {
			ID:              1,
			SiteID:          "site_demo",
			VisitorToken:    "vt_1",
			Status:          "closed",
			AssignedAgentID: &owner,
			ClosedAt:        &now,
			ClosedByAgentID: &owner,
		},
	})

	out, err := svc.CloseConversation(context.Background(), CloseConversationInput{
		ConversationID: 1,
		ActorAgentID:   7,
		ActorRole:      "agent",
	})
	if err != nil {
		t.Fatalf("CloseConversation failed: %v", err)
	}
	if out.Status != "closed" {
		t.Fatalf("unexpected status: %s", out.Status)
	}
	if repo.items[1].Status != "closed" {
		t.Fatalf("stored status should remain closed")
	}
}
