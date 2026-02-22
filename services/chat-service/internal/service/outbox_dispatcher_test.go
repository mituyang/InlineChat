package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"inlinechat/services/chat-service/internal/model"
)

type fakeDispatchOutboxRepo struct {
	pending         []model.EventOutbox
	markPublishedID []uint64
	markRetryID     []uint64
	markDeadID      []uint64
	replayRows      int64
}

func (r *fakeDispatchOutboxRepo) Create(context.Context, *model.EventOutbox) error {
	return nil
}

func (r *fakeDispatchOutboxRepo) FetchPendingForUpdate(context.Context, int, time.Time) ([]model.EventOutbox, error) {
	items := r.pending
	r.pending = nil
	return items, nil
}

func (r *fakeDispatchOutboxRepo) MarkPublished(_ context.Context, id uint64, _ time.Time) error {
	r.markPublishedID = append(r.markPublishedID, id)
	return nil
}

func (r *fakeDispatchOutboxRepo) MarkForRetry(_ context.Context, id uint64, _ time.Time, _ string) error {
	r.markRetryID = append(r.markRetryID, id)
	return nil
}

func (r *fakeDispatchOutboxRepo) MarkDead(_ context.Context, id uint64, _ string) error {
	r.markDeadID = append(r.markDeadID, id)
	return nil
}

func (r *fakeDispatchOutboxRepo) ReplayDead(context.Context, int) (int64, error) {
	return r.replayRows, nil
}

func (r *fakeDispatchOutboxRepo) RequeueStaleProcessing(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type fakeOutboxTransport struct {
	err   error
	calls int
}

func (t *fakeOutboxTransport) PublishConversationEvent(context.Context, uint64, []byte) error {
	t.calls++
	return t.err
}

func TestOutboxDispatcherMarksDeadAfterMaxAttempts(t *testing.T) {
	repo := &fakeDispatchOutboxRepo{
		pending: []model.EventOutbox{
			{ID: 11, ConversationID: 1, EventType: "message.new", Payload: `{"type":"message.new"}`, Attempts: 3},
		},
	}
	transport := &fakeOutboxTransport{err: errors.New("publish failed")}
	dispatcher := NewOutboxDispatcher(repo, transport, nil, zap.NewNop(), OutboxDispatcherConfig{
		BatchSize:    10,
		MaxAttempts:  3,
		PollInterval: time.Second,
	})

	dispatcher.dispatchOnce(context.Background())

	if len(repo.markDeadID) != 1 || repo.markDeadID[0] != 11 {
		t.Fatalf("expected event moved to dead letter, got %v", repo.markDeadID)
	}
	if len(repo.markRetryID) != 0 {
		t.Fatalf("dead letter event should not retry, got %v", repo.markRetryID)
	}
}

func TestOutboxDispatcherMarksRetryBeforeMaxAttempts(t *testing.T) {
	repo := &fakeDispatchOutboxRepo{
		pending: []model.EventOutbox{
			{ID: 12, ConversationID: 2, EventType: "message.new", Payload: `{"type":"message.new"}`, Attempts: 2},
		},
	}
	transport := &fakeOutboxTransport{err: errors.New("publish failed")}
	dispatcher := NewOutboxDispatcher(repo, transport, nil, zap.NewNop(), OutboxDispatcherConfig{
		BatchSize:    10,
		MaxAttempts:  3,
		PollInterval: time.Second,
	})

	dispatcher.dispatchOnce(context.Background())

	if len(repo.markRetryID) != 1 || repo.markRetryID[0] != 12 {
		t.Fatalf("expected event marked for retry, got %v", repo.markRetryID)
	}
	if len(repo.markDeadID) != 0 {
		t.Fatalf("retry event should not enter dead letter, got %v", repo.markDeadID)
	}
}

func TestOutboxDispatcherReplayDeadTriggersWakeup(t *testing.T) {
	repo := &fakeDispatchOutboxRepo{replayRows: 2}
	dispatcher := NewOutboxDispatcher(repo, &fakeOutboxTransport{}, nil, zap.NewNop(), OutboxDispatcherConfig{
		PollInterval: time.Second,
	})

	rows, err := dispatcher.ReplayDead(context.Background(), 100)
	if err != nil {
		t.Fatalf("ReplayDead returned error: %v", err)
	}
	if rows != 2 {
		t.Fatalf("expected replay rows=2, got %d", rows)
	}

	select {
	case <-dispatcher.wakeCh:
	default:
		t.Fatal("expected wake channel to be triggered after replay")
	}
}
