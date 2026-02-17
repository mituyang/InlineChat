package repository

import (
	"context"
	"errors"

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

type MessageRepository interface {
	Create(ctx context.Context, message *model.Message) error
	ListByConversation(ctx context.Context, conversationID uint64, limit int, beforeID uint64) ([]model.Message, error)
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
	return r.db.WithContext(ctx).Create(conversation).Error
}

func (r *GormConversationRepository) GetByID(ctx context.Context, id uint64) (*model.Conversation, error) {
	var conversation model.Conversation
	if err := r.db.WithContext(ctx).First(&conversation, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &conversation, nil
}

func (r *GormConversationRepository) List(ctx context.Context, filter ListConversationsFilter) ([]model.Conversation, error) {
	query := r.db.WithContext(ctx).Model(&model.Conversation{})

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

	var conversation model.Conversation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&conversation, id).Error; err != nil {
		_ = tx.Rollback()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	changed, err := mutation(&conversation)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	if changed {
		if err := tx.Save(&conversation).Error; err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
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
	return r.db.WithContext(ctx).Create(message).Error
}

func (r *GormMessageRepository) ListByConversation(ctx context.Context, conversationID uint64, limit int, beforeID uint64) ([]model.Message, error) {
	query := r.db.WithContext(ctx).
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
