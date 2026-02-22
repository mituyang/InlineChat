package service

import (
	"context"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"inlinechat/services/chat-service/internal/repository"
)

type OutboxEventTransport interface {
	PublishConversationEvent(ctx context.Context, conversationID uint64, payload []byte) error
}

type OutboxWakeupSubscriber interface {
	ConsumeOutboxWakeup(ctx context.Context, onWakeup func()) error
}

type OutboxDispatcherConfig struct {
	PollInterval      time.Duration
	BatchSize         int
	MaxAttempts       int
	RetryBaseInterval time.Duration
	RetryMaxInterval  time.Duration
	ProcessingTimeout time.Duration
}

func (c OutboxDispatcherConfig) normalized() OutboxDispatcherConfig {
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Second
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 100
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 8
	}
	if c.RetryBaseInterval <= 0 {
		c.RetryBaseInterval = 500 * time.Millisecond
	}
	if c.RetryMaxInterval <= 0 {
		c.RetryMaxInterval = 15 * time.Second
	}
	if c.ProcessingTimeout <= 0 {
		c.ProcessingTimeout = 30 * time.Second
	}
	return c
}

type OutboxDispatcher struct {
	repo       repository.EventOutboxRepository
	transport  OutboxEventTransport
	subscriber OutboxWakeupSubscriber
	logger     *zap.Logger
	cfg        OutboxDispatcherConfig

	wakeCh chan struct{}
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewOutboxDispatcher(
	repo repository.EventOutboxRepository,
	transport OutboxEventTransport,
	subscriber OutboxWakeupSubscriber,
	logger *zap.Logger,
	cfg OutboxDispatcherConfig,
) *OutboxDispatcher {
	return &OutboxDispatcher{
		repo:       repo,
		transport:  transport,
		subscriber: subscriber,
		logger:     logger,
		cfg:        cfg.normalized(),
		wakeCh:     make(chan struct{}, 1),
	}
}

func (d *OutboxDispatcher) Start(ctx context.Context) {
	if d == nil || d.repo == nil || d.transport == nil {
		return
	}
	if d.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.loop(runCtx)
	}()
	if d.subscriber != nil {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.consumeWakeup(runCtx)
		}()
	}
}

func (d *OutboxDispatcher) Stop() {
	if d == nil {
		return
	}
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	d.wg.Wait()
}

func (d *OutboxDispatcher) loop(ctx context.Context) {
	ticker := time.NewTicker(d.cfg.PollInterval)
	defer ticker.Stop()

	d.dispatchOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.dispatchOnce(ctx)
		case <-d.wakeCh:
			d.dispatchOnce(ctx)
		}
	}
}

func (d *OutboxDispatcher) consumeWakeup(ctx context.Context) {
	retryDelay := time.Second
	for {
		err := d.subscriber.ConsumeOutboxWakeup(ctx, func() {
			d.Trigger()
		})
		if ctx.Err() != nil {
			return
		}
		d.logger.Warn("consume outbox wakeup failed",
			zap.Error(err),
			zap.Duration("retry_after", retryDelay),
		)
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if retryDelay < 5*time.Second {
			retryDelay *= 2
		}
	}
}

func (d *OutboxDispatcher) Trigger() {
	if d == nil || d.wakeCh == nil {
		return
	}
	select {
	case d.wakeCh <- struct{}{}:
	default:
	}
}

func (d *OutboxDispatcher) ReplayDead(ctx context.Context, limit int) (int64, error) {
	if d == nil || d.repo == nil {
		return 0, nil
	}
	rows, err := d.repo.ReplayDead(ctx, limit)
	if err != nil {
		return 0, err
	}
	if rows > 0 {
		d.Trigger()
	}
	return rows, nil
}

func (d *OutboxDispatcher) dispatchOnce(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	staleBefore := time.Now().Add(-d.cfg.ProcessingTimeout)
	if _, err := d.repo.RequeueStaleProcessing(ctx, staleBefore); err != nil {
		d.logger.Warn("requeue stale outbox events failed", zap.Error(err))
	}

	items, err := d.repo.FetchPendingForUpdate(ctx, d.cfg.BatchSize, time.Now())
	if err != nil {
		d.logger.Warn("fetch pending outbox events failed", zap.Error(err))
		return
	}
	if len(items) == 0 {
		return
	}

	for i := range items {
		item := items[i]
		if err := d.transport.PublishConversationEvent(ctx, item.ConversationID, []byte(item.Payload)); err != nil {
			if item.Attempts >= d.cfg.MaxAttempts {
				deadErr := d.repo.MarkDead(ctx, item.ID, trimError(err))
				if deadErr != nil {
					d.logger.Warn("mark outbox event dead failed",
						zap.Error(deadErr),
						zap.Uint64("event_id", item.ID),
					)
				}
				d.logger.Warn("outbox event moved to dead letter",
					zap.Error(err),
					zap.Uint64("event_id", item.ID),
					zap.Uint64("conversation_id", item.ConversationID),
					zap.String("event_type", item.EventType),
					zap.Int("attempts", item.Attempts),
				)
				continue
			}

			nextRetryAt := time.Now().Add(d.retryDelay(item.Attempts))
			retryErr := d.repo.MarkForRetry(ctx, item.ID, nextRetryAt, trimError(err))
			if retryErr != nil {
				d.logger.Warn("mark outbox event retry failed",
					zap.Error(retryErr),
					zap.Uint64("event_id", item.ID),
				)
			}
			d.logger.Warn("publish outbox event failed",
				zap.Error(err),
				zap.Uint64("event_id", item.ID),
				zap.Uint64("conversation_id", item.ConversationID),
				zap.String("event_type", item.EventType),
			)
			continue
		}

		if err := d.repo.MarkPublished(ctx, item.ID, time.Now()); err != nil {
			d.logger.Warn("mark outbox event published failed",
				zap.Error(err),
				zap.Uint64("event_id", item.ID),
			)
			continue
		}
	}
}

func (d *OutboxDispatcher) retryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := d.cfg.RetryBaseInterval
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= d.cfg.RetryMaxInterval {
			return d.cfg.RetryMaxInterval
		}
	}
	if delay > d.cfg.RetryMaxInterval {
		return d.cfg.RetryMaxInterval
	}
	return delay
}

func trimError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if len(msg) <= 1024 {
		return msg
	}
	return msg[:1024]
}
