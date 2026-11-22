package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow/appstate"
)

type SyncResult struct {
	ID                  string            `json:"id"`
	Status              string            `json:"status"`
	AppStateCollections []string          `json:"appStateCollections"`
	Contacts            int               `json:"contacts"`
	Groups              int               `json:"groups"`
	Errors              map[string]string `json:"errors,omitempty"`
}

func (m *Manager) Sync(ctx context.Context, id string) (SyncResult, error) {
	if !ValidSessionID(id) {
		return SyncResult{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)

	m.mu.RLock()
	service, exists := m.sessions[id]
	m.mu.RUnlock()
	if !exists || service.rejected.Load() {
		return SyncResult{}, ErrSessionNotFound
	}
	if !service.Client.IsConnected() || !service.Client.IsLoggedIn() {
		return SyncResult{}, errors.New("WhatsApp session is not connected")
	}

	service.syncMu.Lock()
	defer service.syncMu.Unlock()

	result := SyncResult{
		ID:     id,
		Status: "completed",
		Errors: make(map[string]string),
	}
	completedOperations := 0
	for _, collection := range appstate.AllPatchNames {
		if err := service.Client.FetchAppState(ctx, collection, false, false); err != nil {
			result.Errors[string(collection)] = err.Error()
			continue
		}
		result.AppStateCollections = append(result.AppStateCollections, string(collection))
		completedOperations++
	}

	groups, err := service.Client.GetJoinedGroups(ctx)
	if err != nil {
		result.Errors["groups"] = err.Error()
	} else {
		result.Groups = len(groups)
		completedOperations++
	}

	if service.Client.Store == nil || service.Client.Store.Contacts == nil {
		result.Errors["contacts"] = "contact store is not initialized"
	} else {
		contacts, err := service.Client.Store.Contacts.GetAllContacts(ctx)
		if err != nil {
			result.Errors["contacts"] = err.Error()
		} else {
			result.Contacts = len(contacts)
			completedOperations++
		}
	}

	if len(result.Errors) > 0 {
		result.Status = "partial"
	}
	if completedOperations == 0 {
		return result, fmt.Errorf("all WhatsApp sync operations failed")
	}
	return result, nil
}
