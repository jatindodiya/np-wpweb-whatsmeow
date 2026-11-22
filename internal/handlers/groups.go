package handlers

import (
	"fmt"
	"net/http"
	"sort"
)

type groupItem struct {
	JID              string `json:"jid"`
	Name             string `json:"name"`
	ParticipantCount int    `json:"participantCount"`
}

type groupsResponse struct {
	Count  int         `json:"count"`
	Groups []groupItem `json:"groups"`
}

func GetGroups(getClient clientLookup) http.HandlerFunc {
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
		if !exists || client == nil {
			WriteJSON(w, http.StatusNotFound, map[string]string{
				"error": "WhatsApp session not found",
			})
			return
		}

		groups, err := client.GetJoinedGroups(r.Context())
		if err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to fetch groups: %v", err),
			})
			return
		}

		list := make([]groupItem, 0, len(groups))
		for _, group := range groups {
			list = append(list, groupItem{
				JID:              group.JID.String(),
				Name:             group.Name,
				ParticipantCount: group.ParticipantCount,
			})
		}

		sort.Slice(list, func(i, j int) bool {
			return list[i].JID < list[j].JID
		})

		WriteJSON(w, http.StatusOK, groupsResponse{
			Count:  len(list),
			Groups: list,
		})
	}
}