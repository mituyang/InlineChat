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

func dbWithContext(db *gorm.DB, ctx context.Context) *gorm.DB {
	if tx := txFromContext(ctx); tx != nil {
		return tx.WithContext(ctx)
	}
	return db.WithContext(ctx)
}

type GormTransactionManager struct {
	db *gorm.DB
}

func NewTransactionManager(db *gorm.DB) *GormTransactionManager {
	return &GormTransactionManager{db: db}
}

func (m *GormTransactionManager) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(withTx(ctx, tx))
	})
}

type GormConversationRepository struct {
	db *gorm.DB
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

func NewConversationRepository(db *gorm.DB) *GormConversationRepository {
	return &GormConversationRepository{db: db}
}

func (r *GormConversationRepository) Create(ctx context.Context, conversation *model.Conversation) error {
	return dbWithContext(r.db, ctx).Create(conversation).Error
}

func (r *GormConversationRepository) GetByID(ctx context.Context, id uint64) (*model.Conversation, error) {
	var conversation model.Conversation
	if err := dbWithContext(r.db, ctx).First(&conversation, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &conversation, nil
}

func (r *GormConversationRepository) List(ctx context.Context, filter ListConversationsFilter) ([]model.Conversation, error) {
	query := dbWithContext(r.db, ctx).Model(&model.Conversation{})

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

	tx := r.db.WithContext(ctx).Begin()
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
	db *gorm.DB
}

func NewMessageRepository(db *gorm.DB) *GormMessageRepository {
	return &GormMessageRepository{db: db}
}

func (r *GormMessageRepository) Create(ctx context.Context, message *model.Message) error {
	return dbWithContext(r.db, ctx).Create(message).Error
}

func (r *GormMessageRepository) GetByID(ctx context.Context, conversationID uint64, messageID uint64) (*model.Message, error) {
	var message model.Message
	if err := dbWithContext(r.db, ctx).
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
	var message model.Message
	if err := dbWithContext(r.db, ctx).
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
	var message model.Message
	if err := dbWithContext(r.db, ctx).
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
	var message model.Message
	if err := dbWithContext(r.db, ctx).
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
	query := dbWithContext(r.db, ctx).
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
	tx := dbWithContext(r.db, ctx).
		Model(&model.Message{}).
		Where("conversation_id = ? AND id = ? AND status = ?", conversationID, messageID, "sent").
		Update("status", "delivered")
	if tx.Error != nil {
		return false, tx.Error
	}
	return tx.RowsAffected > 0, nil
}

func (r *GormMessageRepository) MarkReadByConversationAndSender(ctx context.Context, conversationID uint64, senderType string, lastReadMessageID uint64) (int64, error) {
	tx := dbWithContext(r.db, ctx).
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
	db *gorm.DB
}

func NewEventOutboxRepository(db *gorm.DB) *GormEventOutboxRepository {
	return &GormEventOutboxRepository{db: db}
}

func (r *GormEventOutboxRepository) Create(ctx context.Context, event *model.EventOutbox) error {
	return dbWithContext(r.db, ctx).Create(event).Error
}

func (r *GormEventOutboxRepository) FetchPendingForUpdate(ctx context.Context, limit int, retryableBefore time.Time) ([]model.EventOutbox, error) {
	if limit <= 0 {
		limit = 100
	}

	items := make([]model.EventOutbox, 0, limit)
	err := dbWithContext(r.db, ctx).Transaction(func(tx *gorm.DB) error {
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
	return dbWithContext(r.db, ctx).
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
	return dbWithContext(r.db, ctx).
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

func (r *GormEventOutboxRepository) RequeueStaleProcessing(ctx context.Context, staleBefore time.Time) (int64, error) {
	now := time.Now()
	tx := dbWithContext(r.db, ctx).
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
