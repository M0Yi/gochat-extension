package store

import (
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/m0yi/gochat-server/internal/types"
)

type Store struct {
	mu            sync.RWMutex
	conversations map[string]*conversation
	messages      map[string][]types.StoredMessage
}

type conversation struct {
	info types.ConversationInfo
}

func New() *Store {
	return &Store{
		conversations: make(map[string]*conversation),
		messages:      make(map[string][]types.StoredMessage),
	}
}

func (s *Store) GetOrCreateConversation(id, name string) types.ConversationInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c, ok := s.conversations[id]; ok {
		c.info.LastActive = time.Now()
		c.info.MessageCount++
		c.info.Name = name
		return c.info
	}

	info := types.ConversationInfo{
		ID:           id,
		Name:         name,
		CreatedAt:    time.Now(),
		LastActive:   time.Now(),
		MessageCount: 1,
	}
	s.conversations[id] = &conversation{info: info}
	return info
}

func (s *Store) ListConversations() []types.ConversationInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]types.ConversationInfo, 0, len(s.conversations))
	for _, c := range s.conversations {
		result = append(result, c.info)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].LastActive.After(result[j].LastActive)
	})
	return result
}

func (s *Store) AddMessage(msg types.StoredMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	s.messages[msg.ConversationID] = append(s.messages[msg.ConversationID], msg)
}

func (s *Store) GetMessages(conversationID string, limit int) []types.StoredMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	msgs := s.messages[conversationID]
	if msgs == nil {
		return nil
	}

	if limit <= 0 || limit > len(msgs) {
		limit = len(msgs)
	}

	result := make([]types.StoredMessage, limit)
	copy(result, msgs[len(msgs)-limit:])
	return result
}

func (s *Store) DeleteConversation(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conversations, id)
	delete(s.messages, id)
}

func (s *Store) GetConversation(id string) (types.ConversationInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.conversations[id]
	if !ok {
		return types.ConversationInfo{}, false
	}
	return c.info, true
}
