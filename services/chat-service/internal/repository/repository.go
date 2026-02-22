package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"inlinechat/services/chat-service/internal/model"
)

var ErrNotFound = errors.New("not found")

type ConversationRepository interface {
	Create(ctx context.Context, conversation *model.Conversation) error
	GetByID(ctx context.Context, id uint64) (*model.Conversation, error)
	GetLatestOpenBySiteVisitor(ctx context.Context, siteID string, visitorToken string) (*model.Conversation, error)
	List(ctx context.Context, filter ListConversationsFilter) ([]model.Conversation, error)
	Mutate(ctx context.Context, id uint64, mutation ConversationMutation) (*model.Conversation, error)
}

type TransactionManager interface {
	WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

type MessageRepository interface {
	Create(ctx context.Context, message *model.Message) error
	GetByID(ctx context.Context, conversationID uint64, messageID uint64) (*model.Message, error)
	GetByClientMsgID(ctx context.Context, conversationID uint64, clientMsgID string) (*model.Message, error)
	GetLatestByConversation(ctx context.Context, conversationID uint64) (*model.Message, error)
	GetLatestByConversationExcludingSystem(ctx context.Context, conversationID uint64) (*model.Message, error)
	ListByConversation(ctx context.Context, conversationID uint64, limit int, beforeID uint64) ([]model.Message, error)
	MarkDelivered(ctx context.Context, conversationID uint64, messageID uint64) (bool, error)
	MarkReadByConversationAndSender(ctx context.Context, conversationID uint64, senderType string, lastReadMessageID uint64) (int64, error)
}

type EventOutboxRepository interface {
	Create(ctx context.Context, event *model.EventOutbox) error
	FetchPendingForUpdate(ctx context.Context, limit int, retryableBefore time.Time) ([]model.EventOutbox, error)
	MarkPublished(ctx context.Context, id uint64, publishedAt time.Time) error
	MarkForRetry(ctx context.Context, id uint64, nextRetryAt time.Time, lastError string) error
	MarkDead(ctx context.Context, id uint64, lastError string) error
	ReplayDead(ctx context.Context, limit int) (int64, error)
	RequeueStaleProcessing(ctx context.Context, staleBefore time.Time) (int64, error)
}

type txContextKey struct{}

func withTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

func txFromContext(ctx context.Context) *gorm.DB {
	if ctx == nil {
		return nil
	}
	tx, _ := ctx.Value(txContextKey{}).(*gorm.DB)
	return tx
}

func resolveQueryTimeout(overrides ...time.Duration) time.Duration {
	if len(overrides) > 0 && overrides[0] > 0 {
		return overrides[0]
	}
	return 1500 * time.Millisecond
}

func dbWithContext(db *gorm.DB, ctx context.Context, defaultQueryTimeout time.Duration) (*gorm.DB, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if defaultQueryTimeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			timeoutCtx, cancel := context.WithTimeout(ctx, defaultQueryTimeout)
			if tx := txFromContext(timeoutCtx); tx != nil {
				return tx.WithContext(timeoutCtx), cancel
			}
			return db.WithContext(timeoutCtx), cancel
		}
	}
	if tx := txFromContext(ctx); tx != nil {
		return tx.WithContext(ctx), func() {}
	}
	return db.WithContext(ctx), func() {}
}

type GormTransactionManager struct {
	db                  *gorm.DB
	defaultQueryTimeout time.Duration
}

func NewTransactionManager(db *gorm.DB, defaultQueryTimeout ...time.Duration) *GormTransactionManager {
	return &GormTransactionManager{
		db:                  db,
		defaultQueryTimeout: resolveQueryTimeout(defaultQueryTimeout...),
	}
}

func (m *GormTransactionManager) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	db, cancel := dbWithContext(m.db, ctx, m.defaultQueryTimeout)
	defer cancel()
	return db.Transaction(func(tx *gorm.DB) error {
		return fn(withTx(ctx, tx))
	})
}

type GormConversationRepository struct {
	db                  *gorm.DB
	defaultQueryTimeout time.Duration
}

type ListConversationsFilter struct {
	Status          string
	SiteID          string
	AssignedAgentID *uint64
	UnassignedOnly  bool
	Limit           int
	Offset          int
}

type ConversationMutation func(conversation *model.Conversation) (bool, error)

func NewConversationRepository(db *gorm.DB, defaultQueryTimeout ...time.Duration) *GormConversationRepository {
	return &GormConversationRepository{
		db:                  db,
		defaultQueryTimeout: resolveQueryTimeout(defaultQueryTimeout...),
	}
}

func (r *GormConversationRepository) Create(ctx context.Context, conversation *model.Conversation) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.Create(conversation).Error
}

func (r *GormConversationRepository) GetByID(ctx context.Context, id uint64) (*model.Conversation, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var conversation model.Conversation
	if err := db.First(&conversation, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &conversation, nil
}

func (r *GormConversationRepository) GetLatestOpenBySiteVisitor(ctx context.Context, siteID string, visitorToken string) (*model.Conversation, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var conversation model.Conversation
	if err := db.
		Where("site_id = ? AND visitor_token = ? AND status = ?", siteID, visitorToken, "open").
		Order("id DESC").
		First(&conversation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &conversation, nil
}

func (r *GormConversationRepository) List(ctx context.Context, filter ListConversationsFilter) ([]model.Conversation, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	query := db.Model(&model.Conversation{})

	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.SiteID != "" {
		query = query.Where("site_id = ?", filter.SiteID)
	}
	if filter.AssignedAgentID != nil {
		query = query.Where("assigned_agent_id = ?", *filter.AssignedAgentID)
	}
	if filter.UnassignedOnly {
		query = query.Where("assigned_agent_id IS NULL")
	}

	var conversations []model.Conversation
	if err := query.
		Order("id DESC").
		Limit(filter.Limit).
		Offset(filter.Offset).
		Find(&conversations).Error; err != nil {
		return nil, err
	}

	return conversations, nil
}

func (r *GormConversationRepository) Mutate(ctx context.Context, id uint64, mutation ConversationMutation) (*model.Conversation, error) {
	outerTx := txFromContext(ctx)
	if outerTx != nil {
		return r.mutateInDB(outerTx.WithContext(ctx), id, mutation)
	}

	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	tx := db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}

	defer func() {
		if recoverErr := recover(); recoverErr != nil {
			_ = tx.Rollback()
			panic(recoverErr)
		}
	}()

	conversation, err := r.mutateInDB(tx, id, mutation)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	return conversation, nil
}

func (r *GormConversationRepository) mutateInDB(db *gorm.DB, id uint64, mutation ConversationMutation) (*model.Conversation, error) {
	var conversation model.Conversation
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&conversation, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	changed, err := mutation(&conversation)
	if err != nil {
		return nil, err
	}

	if changed {
		if err := db.Save(&conversation).Error; err != nil {
			return nil, err
		}
	}

	return &conversation, nil
}

type GormMessageRepository struct {
	db                  *gorm.DB
	defaultQueryTimeout time.Duration
}

func NewMessageRepository(db *gorm.DB, defaultQueryTimeout ...time.Duration) *GormMessageRepository {
	return &GormMessageRepository{
		db:                  db,
		defaultQueryTimeout: resolveQueryTimeout(defaultQueryTimeout...),
	}
}

func (r *GormMessageRepository) Create(ctx context.Context, message *model.Message) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.Create(message).Error
}

func (r *GormMessageRepository) GetByID(ctx context.Context, conversationID uint64, messageID uint64) (*model.Message, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var message model.Message
	if err := db.
		Where("conversation_id = ? AND id = ?", conversationID, messageID).
		First(&message).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &message, nil
}

func (r *GormMessageRepository) GetByClientMsgID(ctx context.Context, conversationID uint64, clientMsgID string) (*model.Message, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var message model.Message
	if err := db.
		Where("conversation_id = ? AND client_msg_id = ?", conversationID, clientMsgID).
		First(&message).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &message, nil
}

func (r *GormMessageRepository) GetLatestByConversation(ctx context.Context, conversationID uint64) (*model.Message, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var message model.Message
	if err := db.
		Where("conversation_id = ?", conversationID).
		Order("id DESC").
		First(&message).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &message, nil
}

func (r *GormMessageRepository) GetLatestByConversationExcludingSystem(ctx context.Context, conversationID uint64) (*model.Message, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var message model.Message
	if err := db.
		Where("conversation_id = ? AND sender_type <> ?", conversationID, "system").
		Order("id DESC").
		First(&message).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &message, nil
}

func (r *GormMessageRepository) ListByConversation(ctx context.Context, conversationID uint64, limit int, beforeID uint64) ([]model.Message, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	query := db.
		Where("conversation_id = ?", conversationID).
		Order("id DESC").
		Limit(limit)

	if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}

	var messages []model.Message
	if err := query.Find(&messages).Error; err != nil {
		return nil, err
	}
	return messages, nil
}

func (r *GormMessageRepository) MarkDelivered(ctx context.Context, conversationID uint64, messageID uint64) (bool, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	tx := db.
		Model(&model.Message{}).
		Where("conversation_id = ? AND id = ? AND status = ?", conversationID, messageID, "sent").
		Update("status", "delivered")
	if tx.Error != nil {
		return false, tx.Error
	}
	return tx.RowsAffected > 0, nil
}

func (r *GormMessageRepository) MarkReadByConversationAndSender(ctx context.Context, conversationID uint64, senderType string, lastReadMessageID uint64) (int64, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	tx := db.
		Model(&model.Message{}).
		Where("conversation_id = ? AND sender_type = ? AND id <= ?", conversationID, senderType, lastReadMessageID).
		Where("status IN ?", []string{"sent", "delivered"}).
		Update("status", "read")
	if tx.Error != nil {
		return 0, tx.Error
	}
	return tx.RowsAffected, nil
}

type GormEventOutboxRepository struct {
	db                  *gorm.DB
	defaultQueryTimeout time.Duration
}

func NewEventOutboxRepository(db *gorm.DB, defaultQueryTimeout ...time.Duration) *GormEventOutboxRepository {
	return &GormEventOutboxRepository{
		db:                  db,
		defaultQueryTimeout: resolveQueryTimeout(defaultQueryTimeout...),
	}
}

func (r *GormEventOutboxRepository) Create(ctx context.Context, event *model.EventOutbox) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.Create(event).Error
}

func (r *GormEventOutboxRepository) FetchPendingForUpdate(ctx context.Context, limit int, retryableBefore time.Time) ([]model.EventOutbox, error) {
	if limit <= 0 {
		limit = 100
	}

	items := make([]model.EventOutbox, 0, limit)
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Model(&model.EventOutbox{}).
			Where("status = ?", model.OutboxStatusPending).
			Where("next_retry_at IS NULL OR next_retry_at <= ?", retryableBefore).
			Order("id ASC").
			Limit(limit).
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Find(&items).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}

		ids := make([]uint64, 0, len(items))
		for i := range items {
			ids = append(ids, items[i].ID)
		}

		now := time.Now()
		if err := tx.
			Model(&model.EventOutbox{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":        model.OutboxStatusProcessing,
				"attempts":      gorm.Expr("attempts + 1"),
				"processing_at": now,
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].Status = model.OutboxStatusProcessing
			items[i].Attempts++
			items[i].ProcessingAt = &now
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (r *GormEventOutboxRepository) MarkPublished(ctx context.Context, id uint64, publishedAt time.Time) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.
		Model(&model.EventOutbox{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        model.OutboxStatusPublished,
			"published_at":  publishedAt,
			"processing_at": nil,
			"next_retry_at": nil,
			"last_error":    "",
			"updated_at":    publishedAt,
		}).Error
}

func (r *GormEventOutboxRepository) MarkForRetry(ctx context.Context, id uint64, nextRetryAt time.Time, lastError string) error {
	now := time.Now()
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.
		Model(&model.EventOutbox{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        model.OutboxStatusPending,
			"processing_at": nil,
			"next_retry_at": nextRetryAt,
			"last_error":    lastError,
			"updated_at":    now,
		}).Error
}

func (r *GormEventOutboxRepository) MarkDead(ctx context.Context, id uint64, lastError string) error {
	now := time.Now()
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.
		Model(&model.EventOutbox{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        model.OutboxStatusDead,
			"processing_at": nil,
			"next_retry_at": nil,
			"last_error":    lastError,
			"updated_at":    now,
		}).Error
}

func (r *GormEventOutboxRepository) ReplayDead(ctx context.Context, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	now := time.Now()
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	tx := db.
		Model(&model.EventOutbox{}).
		Where("status = ?", model.OutboxStatusDead).
		Order("id ASC").
		Limit(limit).
		Updates(map[string]any{
			"status":        model.OutboxStatusPending,
			"processing_at": nil,
			"next_retry_at": now,
			"last_error":    "",
			"updated_at":    now,
		})
	if tx.Error != nil {
		return 0, tx.Error
	}
	return tx.RowsAffected, nil
}

func (r *GormEventOutboxRepository) RequeueStaleProcessing(ctx context.Context, staleBefore time.Time) (int64, error) {
	now := time.Now()
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	tx := db.
		Model(&model.EventOutbox{}).
		Where("status = ? AND processing_at IS NOT NULL AND processing_at <= ?", model.OutboxStatusProcessing, staleBefore).
		Updates(map[string]any{
			"status":        model.OutboxStatusPending,
			"processing_at": nil,
			"next_retry_at": now,
			"updated_at":    now,
		})
	if tx.Error != nil {
		return 0, tx.Error
	}
	return tx.RowsAffected, nil
}
