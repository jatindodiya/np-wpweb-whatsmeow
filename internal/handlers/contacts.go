package handlers

import (
	"fmt"
	"net/http"
	"sort"

	"go.mau.fi/whatsmeow"
)

type contactItem struct {
	JID          string `json:"jid"`
	Phone        string `json:"phone"`
	Found        bool   `json:"found"`
	FirstName    string `json:"firstName"`
	FullName     string `json:"fullName"`
	PushName     string `json:"pushName"`
	BusinessName string `json:"businessName"`
}

type contactsResponse struct {
	Count    int           `json:"count"`
	Contacts []contactItem `json:"contacts"`
}

type clientLookup func(string) (*whatsmeow.Client, bool)

func GetContacts(getClient clientLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"error": "only GET is allowed",
			})
			return
		}

		sessionID := r.URL.Query().Get("id")
		if sessionID == "" {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "id query parameter is required",
			})
			return
		}
		client, exists := getClient(sessionID)
		if !exists {
			WriteJSON(w, http.StatusNotFound, map[string]string{
				"error": "WhatsApp session not found",
			})
			return
		}

		if client == nil || client.Store == nil || client.Store.Contacts == nil {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "contact store not initialized",
			})
			return
		}

		rawContacts, err := client.Store.Contacts.GetAllContacts(r.Context())
		if err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to fetch contacts: %v", err),
			})
			return
		}

		list := make([]contactItem, 0, len(rawContacts))
		for jid, info := range rawContacts {
			list = append(list, contactItem{
				JID:          jid.String(),
				Phone:        jid.User,
				Found:        info.Found,
				FirstName:    info.FirstName,
				FullName:     info.FullName,
				PushName:     info.PushName,
				BusinessName: info.BusinessName,
			})
		}

		sort.Slice(list, func(i, j int) bool {
			return list[i].JID < list[j].JID
		})

		WriteJSON(w, http.StatusOK, contactsResponse{
			Count:    len(list),
			Contacts: list,
		})
	}
}