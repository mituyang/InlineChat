package service

import (
	"context"
	"errors"
	"sort"
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
	filtered := make([]*model.Conversation, 0, len(r.items))
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
		filtered = append(filtered, cloneConversation(item))
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].ID > filtered[j].ID
	})

	start := filter.Offset
	if start < 0 {
		start = 0
	}
	if start >= len(filtered) {
		return []model.Conversation{}, nil
	}

	end := len(filtered)
	if filter.Limit > 0 && start+filter.Limit < end {
		end = start + filter.Limit
	}

	out := make([]model.Conversation, 0, end-start)
	for _, item := range filtered[start:end] {
		out = append(out, *item)
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
	if message.Status == "" {
		message.Status = MessageStatusSent
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now()
	}
	copyMessage := *message
	r.items[message.ConversationID] = append(r.items[message.ConversationID], copyMessage)
	return nil
}

func (r *fakeMessageRepository) GetByID(_ context.Context, conversationID uint64, messageID uint64) (*model.Message, error) {
	for _, msg := range r.items[conversationID] {
		if msg.ID == messageID {
			cp := msg
			return &cp, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (r *fakeMessageRepository) GetByClientMsgID(_ context.Context, conversationID uint64, clientMsgID string) (*model.Message, error) {
	for _, msg := range r.items[conversationID] {
		if msg.ClientMsgID == clientMsgID {
			cp := msg
			return &cp, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (r *fakeMessageRepository) GetLatestByConversation(_ context.Context, conversationID uint64) (*model.Message, error) {
	items := r.items[conversationID]
	if len(items) == 0 {
		return nil, repository.ErrNotFound
	}

	latest := items[0]
	for i := 1; i < len(items); i++ {
		if items[i].ID > latest.ID {
			latest = items[i]
		}
	}
	cp := latest
	return &cp, nil
}

func (r *fakeMessageRepository) GetLatestByConversationExcludingSystem(_ context.Context, conversationID uint64) (*model.Message, error) {
	items := r.items[conversationID]
	var (
		latest model.Message
		found  bool
	)
	for _, msg := range items {
		if msg.SenderType == "system" {
			continue
		}
		if !found || msg.ID > latest.ID {
			latest = msg
			found = true
		}
	}
	if !found {
		return nil, repository.ErrNotFound
	}
	cp := latest
	return &cp, nil
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

func (r *fakeMessageRepository) MarkDelivered(_ context.Context, conversationID uint64, messageID uint64) (bool, error) {
	items := r.items[conversationID]
	for i := range items {
		if items[i].ID != messageID {
			continue
		}
		if items[i].Status != MessageStatusSent {
			return false, nil
		}
		items[i].Status = MessageStatusDelivered
		r.items[conversationID] = items
		return true, nil
	}
	return false, nil
}

func (r *fakeMessageRepository) MarkReadByConversationAndSender(_ context.Context, conversationID uint64, senderType string, lastReadMessageID uint64) (int64, error) {
	items := r.items[conversationID]
	var updated int64
	for i := range items {
		if items[i].ID > lastReadMessageID {
			continue
		}
		if items[i].SenderType != senderType {
			continue
		}
		if items[i].Status == MessageStatusRead {
			continue
		}
		items[i].Status = MessageStatusRead
		updated++
	}
	r.items[conversationID] = items
	return updated, nil
}

type fakePublisher struct {
	calls                int
	lastMessage          *model.Message
	statusCalls          int
	lastStatusMessageID  uint64
	lastStatus           string
	rangeStatusCalls     int
	lastRangeSenderType  string
	lastRangeUpToMessage uint64
	lastRangeStatus      string
	closeCalls           int
	lastClosedID         uint64
	err                  error
}

func (p *fakePublisher) PublishMessageCreated(_ context.Context, message *model.Message) error {
	p.calls++
	copyMessage := *message
	p.lastMessage = &copyMessage
	return p.err
}

func (p *fakePublisher) PublishMessageStatus(_ context.Context, _ uint64, messageID uint64, status string) error {
	p.statusCalls++
	p.lastStatusMessageID = messageID
	p.lastStatus = status
	return p.err
}

func (p *fakePublisher) PublishMessageStatusRange(_ context.Context, _ uint64, senderType string, upToMessageID uint64, status string) error {
	p.rangeStatusCalls++
	p.lastRangeSenderType = senderType
	p.lastRangeUpToMessage = upToMessageID
	p.lastRangeStatus = status
	return p.err
}

func (p *fakePublisher) PublishConversationClosed(_ context.Context, conversationID uint64) error {
	p.closeCalls++
	p.lastClosedID = conversationID
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
	if c.PendingTransferToAgentID != nil {
		id := *c.PendingTransferToAgentID
		cp.PendingTransferToAgentID = &id
	}
	if c.PendingTransferFromAgentID != nil {
		id := *c.PendingTransferFromAgentID
		cp.PendingTransferFromAgentID = &id
	}
	if c.PendingTransferRequestedAt != nil {
		at := *c.PendingTransferRequestedAt
		cp.PendingTransferRequestedAt = &at
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
	svc := New(conversationRepo, messageRepo, zap.NewNop(), publisher, 0)
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

func TestTransferConversationAllowedForSuperAdminStartsPending(t *testing.T) {
	owner := uint64(9)
	svc, repo, messageRepo, publisher := testChatServiceWithConversations(map[uint64]*model.Conversation{
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
	if out.AssignedAgentID == nil || *out.AssignedAgentID != 9 {
		t.Fatalf("unexpected assigned_agent_id: %+v", out.AssignedAgentID)
	}
	if out.PendingTransferToAgentID == nil || *out.PendingTransferToAgentID != 8 {
		t.Fatalf("unexpected pending_transfer_to_agent_id: %+v", out.PendingTransferToAgentID)
	}
	if out.PendingTransferFromAgentID == nil || *out.PendingTransferFromAgentID != 9 {
		t.Fatalf("unexpected pending_transfer_from_agent_id: %+v", out.PendingTransferFromAgentID)
	}
	if out.PendingTransferRequestedAt == nil {
		t.Fatalf("pending_transfer_requested_at should be set")
	}

	stored := repo.items[1]
	if stored.AssignedAgentID == nil || *stored.AssignedAgentID != 9 {
		t.Fatalf("conversation not persisted")
	}
	if stored.PendingTransferToAgentID == nil || *stored.PendingTransferToAgentID != 8 {
		t.Fatalf("pending transfer not persisted")
	}
	if len(messageRepo.items[1]) != 1 {
		t.Fatalf("transfer system message should be persisted")
	}
	if publisher.calls != 1 || publisher.lastMessage == nil {
		t.Fatalf("transfer system message should be published, got %+v", publisher)
	}
	if publisher.lastMessage.SenderType != "system" {
		t.Fatalf("expected system sender type, got %s", publisher.lastMessage.SenderType)
	}
	if publisher.lastMessage.Content != "正在转接客服0008，等待对方确认" {
		t.Fatalf("unexpected transfer message content: %s", publisher.lastMessage.Content)
	}
}

func TestTransferConversationRejectsWhenPendingExists(t *testing.T) {
	owner := uint64(9)
	pendingTo := uint64(8)
	pendingFrom := uint64(9)
	requestedAt := time.Now()
	svc, _, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {
			ID:                         1,
			SiteID:                     "site_demo",
			VisitorToken:               "vt_1",
			Status:                     "open",
			AssignedAgentID:            &owner,
			PendingTransferToAgentID:   &pendingTo,
			PendingTransferFromAgentID: &pendingFrom,
			PendingTransferRequestedAt: &requestedAt,
		},
	})

	_, err := svc.TransferConversation(context.Background(), TransferConversationInput{
		ConversationID: 1,
		ActorAgentID:   9,
		ActorRole:      "agent",
		ToAgentID:      7,
	})
	if !errors.Is(err, ErrConversationTransferPending) {
		t.Fatalf("expected ErrConversationTransferPending, got %v", err)
	}
}

func TestConfirmTransferConversationByTargetAgentSuccess(t *testing.T) {
	owner := uint64(9)
	pendingTo := uint64(8)
	pendingFrom := uint64(9)
	requestedAt := time.Now()
	svc, repo, messageRepo, publisher := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {
			ID:                         1,
			SiteID:                     "site_demo",
			VisitorToken:               "vt_1",
			Status:                     "open",
			AssignedAgentID:            &owner,
			PendingTransferToAgentID:   &pendingTo,
			PendingTransferFromAgentID: &pendingFrom,
			PendingTransferRequestedAt: &requestedAt,
		},
	})

	out, err := svc.ConfirmTransferConversation(context.Background(), ConfirmTransferConversationInput{
		ConversationID: 1,
		ActorAgentID:   8,
		ActorRole:      "agent",
	})
	if err != nil {
		t.Fatalf("ConfirmTransferConversation failed: %v", err)
	}
	if out.AssignedAgentID == nil || *out.AssignedAgentID != 8 {
		t.Fatalf("unexpected assigned_agent_id: %+v", out.AssignedAgentID)
	}
	if out.PendingTransferToAgentID != nil || out.PendingTransferFromAgentID != nil || out.PendingTransferRequestedAt != nil {
		t.Fatalf("pending transfer fields should be cleared: %+v", out)
	}

	stored := repo.items[1]
	if stored.AssignedAgentID == nil || *stored.AssignedAgentID != 8 {
		t.Fatalf("assigned agent should switch to pending target")
	}
	if stored.PendingTransferToAgentID != nil || stored.PendingTransferFromAgentID != nil || stored.PendingTransferRequestedAt != nil {
		t.Fatalf("stored pending transfer fields should be cleared")
	}
	if len(messageRepo.items[1]) != 1 {
		t.Fatalf("confirm transfer system message should be persisted")
	}
	if publisher.calls != 1 || publisher.lastMessage == nil {
		t.Fatalf("confirm transfer system message should be published, got %+v", publisher)
	}
	if publisher.lastMessage.Content != "成功转接客服0008" {
		t.Fatalf("unexpected confirm transfer message content: %s", publisher.lastMessage.Content)
	}
}

func TestConfirmTransferConversationForbiddenForNonTargetAgent(t *testing.T) {
	owner := uint64(9)
	pendingTo := uint64(8)
	pendingFrom := uint64(9)
	requestedAt := time.Now()
	svc, _, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {
			ID:                         1,
			SiteID:                     "site_demo",
			VisitorToken:               "vt_1",
			Status:                     "open",
			AssignedAgentID:            &owner,
			PendingTransferToAgentID:   &pendingTo,
			PendingTransferFromAgentID: &pendingFrom,
			PendingTransferRequestedAt: &requestedAt,
		},
	})

	_, err := svc.ConfirmTransferConversation(context.Background(), ConfirmTransferConversationInput{
		ConversationID: 1,
		ActorAgentID:   7,
		ActorRole:      "agent",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestConfirmTransferConversationRequiresPending(t *testing.T) {
	owner := uint64(9)
	svc, _, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open", AssignedAgentID: &owner},
	})

	_, err := svc.ConfirmTransferConversation(context.Background(), ConfirmTransferConversationInput{
		ConversationID: 1,
		ActorAgentID:   8,
		ActorRole:      "agent",
	})
	if !errors.Is(err, ErrConversationTransferNotPending) {
		t.Fatalf("expected ErrConversationTransferNotPending, got %v", err)
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
	svc, repo, _, publisher := testChatServiceWithConversations(map[uint64]*model.Conversation{
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
	if publisher.closeCalls != 1 || publisher.lastClosedID != 1 {
		t.Fatalf("expected publish conversation.closed once, got %+v", publisher)
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

func TestCreateMessageVisitorTokenRequired(t *testing.T) {
	svc, _, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_expected", Status: "open"},
	})

	_, err := svc.CreateMessage(context.Background(), CreateMessageInput{
		ConversationID: 1,
		SenderType:     "visitor",
		SenderID:       "",
		Content:        "hello",
		ClientMsgID:    "c2",
		VisitorToken:   "",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if err.Error() != "visitor_token is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateMessageAgentRequiresClaimedConversation(t *testing.T) {
	svc, _, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open"},
	})

	_, err := svc.CreateMessage(context.Background(), CreateMessageInput{
		ConversationID: 1,
		SenderType:     "agent",
		SenderID:       "7",
		Content:        "agent message",
		ClientMsgID:    "agent_unclaimed_1",
	})
	if !errors.Is(err, ErrConversationUnassigned) {
		t.Fatalf("expected ErrConversationUnassigned, got %v", err)
	}
}

func TestCreateMessageAgentForbiddenForNonOwner(t *testing.T) {
	owner := uint64(9)
	svc, _, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open", AssignedAgentID: &owner},
	})

	_, err := svc.CreateMessage(context.Background(), CreateMessageInput{
		ConversationID: 1,
		SenderType:     "agent",
		SenderID:       "7",
		Content:        "agent message",
		ClientMsgID:    "agent_forbidden_1",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestCreateMessagePublishEvent(t *testing.T) {
	owner := uint64(7)
	svc, _, messageRepo, publisher := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open", AssignedAgentID: &owner},
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
	if out.Status != MessageStatusSent {
		t.Fatalf("expected status sent, got %s", out.Status)
	}
	if publisher.lastMessage == nil || publisher.lastMessage.Status != MessageStatusSent {
		t.Fatalf("expected published status sent, got %+v", publisher.lastMessage)
	}
}

func TestCreateMessageIdempotentByClientMsgID(t *testing.T) {
	svc, _, messageRepo, publisher := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open"},
	})

	first, err := svc.CreateMessage(context.Background(), CreateMessageInput{
		ConversationID: 1,
		SenderType:     "visitor",
		Content:        "hello",
		ClientMsgID:    "dup_1",
		VisitorToken:   "vt_1",
	})
	if err != nil {
		t.Fatalf("first CreateMessage failed: %v", err)
	}

	second, err := svc.CreateMessage(context.Background(), CreateMessageInput{
		ConversationID: 1,
		SenderType:     "visitor",
		Content:        "hello again",
		ClientMsgID:    "dup_1",
		VisitorToken:   "vt_1",
	})
	if err != nil {
		t.Fatalf("second CreateMessage failed: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("expected idempotent same message id, got %d and %d", first.ID, second.ID)
	}
	if len(messageRepo.items[1]) != 1 {
		t.Fatalf("expected only one message persisted, got %d", len(messageRepo.items[1]))
	}
	if publisher.calls != 1 {
		t.Fatalf("expected publish only once, got %d", publisher.calls)
	}
}

func TestMarkMessageDeliveredOnlySentToDelivered(t *testing.T) {
	svc, _, messageRepo, publisher := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open"},
	})
	if err := messageRepo.Create(context.Background(), &model.Message{
		ConversationID: 1,
		SenderType:     "visitor",
		Content:        "hello",
		ClientMsgID:    "m_1",
		Status:         MessageStatusSent,
	}); err != nil {
		t.Fatalf("seed message failed: %v", err)
	}

	out, err := svc.MarkMessageDelivered(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("MarkMessageDelivered failed: %v", err)
	}
	if !out.Updated || out.Status != MessageStatusDelivered {
		t.Fatalf("unexpected first delivered result: %+v", out)
	}
	if publisher.statusCalls != 1 || publisher.lastStatusMessageID != 1 || publisher.lastStatus != MessageStatusDelivered {
		t.Fatalf("expected publish delivered status once, got %+v", publisher)
	}

	out, err = svc.MarkMessageDelivered(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("MarkMessageDelivered second call failed: %v", err)
	}
	if out.Updated || out.Status != MessageStatusDelivered {
		t.Fatalf("expected idempotent delivered status, got %+v", out)
	}
	if publisher.statusCalls != 1 {
		t.Fatalf("idempotent delivered should not republish, got calls=%d", publisher.statusCalls)
	}
}

func TestMarkMessagesReadSuccess(t *testing.T) {
	svc, _, messageRepo, publisher := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open"},
	})
	_ = messageRepo.Create(context.Background(), &model.Message{
		ConversationID: 1,
		SenderType:     "visitor",
		Content:        "v1",
		ClientMsgID:    "m_1",
		Status:         MessageStatusSent,
	})
	_ = messageRepo.Create(context.Background(), &model.Message{
		ConversationID: 1,
		SenderType:     "visitor",
		Content:        "v2",
		ClientMsgID:    "m_2",
		Status:         MessageStatusDelivered,
	})
	_ = messageRepo.Create(context.Background(), &model.Message{
		ConversationID: 1,
		SenderType:     "agent",
		Content:        "a1",
		ClientMsgID:    "m_3",
		Status:         MessageStatusSent,
	})

	updatedCount, err := svc.MarkMessagesRead(context.Background(), MarkMessagesReadInput{
		ConversationID:    1,
		LastReadMessageID: 3,
		ActorType:         "agent",
		ActorAgentID:      7,
	})
	if err != nil {
		t.Fatalf("MarkMessagesRead failed: %v", err)
	}
	if updatedCount != 2 {
		t.Fatalf("expected updated_count=2, got %d", updatedCount)
	}

	msg1, _ := messageRepo.GetByID(context.Background(), 1, 1)
	msg2, _ := messageRepo.GetByID(context.Background(), 1, 2)
	msg3, _ := messageRepo.GetByID(context.Background(), 1, 3)
	if msg1.Status != MessageStatusRead || msg2.Status != MessageStatusRead {
		t.Fatalf("visitor messages should be read, got %#v %#v", msg1.Status, msg2.Status)
	}
	if msg3.Status != MessageStatusSent {
		t.Fatalf("agent message should remain sent, got %s", msg3.Status)
	}
	if publisher.rangeStatusCalls != 1 {
		t.Fatalf("expected one read-range publish, got %d", publisher.rangeStatusCalls)
	}
	if publisher.lastRangeSenderType != "visitor" || publisher.lastRangeUpToMessage != 3 || publisher.lastRangeStatus != MessageStatusRead {
		t.Fatalf("unexpected read-range payload: %+v", publisher)
	}
}

func TestMarkMessagesReadVisitorTokenMismatch(t *testing.T) {
	svc, _, _, _ := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_expected", Status: "open"},
	})

	_, err := svc.MarkMessagesRead(context.Background(), MarkMessagesReadInput{
		ConversationID:    1,
		LastReadMessageID: 10,
		ActorType:         "visitor",
		VisitorToken:      "vt_other",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "visitor token does not match conversation" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMarkMessagesReadIdempotentWhenNoRows(t *testing.T) {
	svc, _, messageRepo, publisher := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open"},
	})
	_ = messageRepo.Create(context.Background(), &model.Message{
		ConversationID: 1,
		SenderType:     "visitor",
		Content:        "v1",
		ClientMsgID:    "m_1",
		Status:         MessageStatusRead,
	})

	updatedCount, err := svc.MarkMessagesRead(context.Background(), MarkMessagesReadInput{
		ConversationID:    1,
		LastReadMessageID: 1,
		ActorType:         "agent",
		ActorAgentID:      7,
	})
	if err != nil {
		t.Fatalf("MarkMessagesRead failed: %v", err)
	}
	if updatedCount != 0 {
		t.Fatalf("expected updated_count=0, got %d", updatedCount)
	}
	if publisher.rangeStatusCalls != 0 {
		t.Fatalf("updated_count=0 should not publish read-range event, got %d", publisher.rangeStatusCalls)
	}
}

func TestCloseConversationIdempotentWhenAlreadyClosed(t *testing.T) {
	owner := uint64(7)
	now := time.Now()
	svc, repo, _, publisher := testChatServiceWithConversations(map[uint64]*model.Conversation{
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
	if publisher.closeCalls != 0 {
		t.Fatalf("already closed should not republish conversation.closed, got %+v", publisher)
	}
}

func TestAutoCloseInactiveConversationsClosesExpiredAgentLastMessage(t *testing.T) {
	owner := uint64(7)
	svc, repo, messageRepo, publisher := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open", AssignedAgentID: &owner},
	})

	err := messageRepo.Create(context.Background(), &model.Message{
		ConversationID: 1,
		SenderType:     "agent",
		SenderID:       "7",
		Content:        "请问还有其他问题吗？",
		ClientMsgID:    "agent_1",
		CreatedAt:      time.Now().Add(-6 * time.Minute),
	})
	if err != nil {
		t.Fatalf("seed message failed: %v", err)
	}

	closedCount, err := svc.AutoCloseInactiveConversations(context.Background(), 5*time.Minute)
	if err != nil {
		t.Fatalf("AutoCloseInactiveConversations failed: %v", err)
	}
	if closedCount != 1 {
		t.Fatalf("expected closed_count=1, got %d", closedCount)
	}

	stored := repo.items[1]
	if stored.Status != "closed" {
		t.Fatalf("expected conversation closed, got %s", stored.Status)
	}
	if stored.ClosedAt == nil {
		t.Fatalf("closed_at should be set")
	}
	if stored.ClosedByAgentID == nil || *stored.ClosedByAgentID != owner {
		t.Fatalf("unexpected closed_by_agent_id: %+v", stored.ClosedByAgentID)
	}
	if publisher.closeCalls != 1 || publisher.lastClosedID != 1 {
		t.Fatalf("expected publish conversation.closed once, got %+v", publisher)
	}
}

func TestAutoCloseInactiveConversationsSkipsWhenVisitorAlreadyReplied(t *testing.T) {
	owner := uint64(7)
	svc, repo, messageRepo, publisher := testChatServiceWithConversations(map[uint64]*model.Conversation{
		1: {ID: 1, SiteID: "site_demo", VisitorToken: "vt_1", Status: "open", AssignedAgentID: &owner},
	})

	_ = messageRepo.Create(context.Background(), &model.Message{
		ConversationID: 1,
		SenderType:     "agent",
		SenderID:       "7",
		Content:        "请问还有其他问题吗？",
		ClientMsgID:    "agent_1",
		CreatedAt:      time.Now().Add(-10 * time.Minute),
	})
	_ = messageRepo.Create(context.Background(), &model.Message{
		ConversationID: 1,
		SenderType:     "visitor",
		Content:        "没有了，谢谢",
		ClientMsgID:    "visitor_1",
		CreatedAt:      time.Now().Add(-9 * time.Minute),
	})

	closedCount, err := svc.AutoCloseInactiveConversations(context.Background(), 5*time.Minute)
	if err != nil {
		t.Fatalf("AutoCloseInactiveConversations failed: %v", err)
	}
	if closedCount != 0 {
		t.Fatalf("expected closed_count=0, got %d", closedCount)
	}
	if repo.items[1].Status != "open" {
		t.Fatalf("conversation should stay open, got %s", repo.items[1].Status)
	}
	if publisher.closeCalls != 0 {
		t.Fatalf("should not publish conversation.closed when not closed, got %+v", publisher)
	}
}
