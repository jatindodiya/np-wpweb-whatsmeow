package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

var ErrInvalidGroupInvite = errors.New("invalid group invite")

type SendGroupInviteMessageResult struct {
	ID            string `json:"messageId"`
	Recipient     string `json:"recipient"`
	GroupJID      string `json:"groupJid"`
	GroupName     string `json:"groupName"`
	InviteLink    string `json:"inviteLink"`
	Timestamp     string `json:"timestamp"`
	HistoryStored bool   `json:"historyStored"`
}

func (m *Manager) SendGroupInviteMessage(ctx context.Context, id, mobileNumber, recipientGroupJID, inviteGroupJID, caption string) (SendGroupInviteMessageResult, error) {
	if !ValidSessionID(id) {
		return SendGroupInviteMessageResult{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)
	if _, _, err := ValidateRecipient(mobileNumber, recipientGroupJID); err != nil {
		return SendGroupInviteMessageResult{}, err
	}
	if len([]rune(caption)) > 1024 {
		return SendGroupInviteMessageResult{}, fmt.Errorf("%w: caption must be 1024 characters or fewer", ErrInvalidGroupInvite)
	}
	group, err := types.ParseJID(strings.TrimSpace(inviteGroupJID))
	if err != nil || group.Server != types.GroupServer || group.User == "" {
		return SendGroupInviteMessageResult{}, fmt.Errorf("%w: groupJID must be a valid WhatsApp group JID", ErrInvalidGroupInvite)
	}
	service, client, err := m.getConnectedSession(id)
	if err != nil {
		return SendGroupInviteMessageResult{}, err
	}

	info, err := client.GetGroupInfo(ctx, group)
	if err != nil {
		return SendGroupInviteMessageResult{}, fmt.Errorf("get WhatsApp group info: %w", err)
	}
	inviteLink, err := client.GetGroupInviteLink(ctx, group, false)
	if err != nil {
		return SendGroupInviteMessageResult{}, fmt.Errorf("get WhatsApp group invite link: %w", err)
	}
	code := strings.TrimPrefix(inviteLink, "https://chat.whatsapp.com/")
	if code == "" || code == inviteLink {
		return SendGroupInviteMessageResult{}, fmt.Errorf("WhatsApp returned an invalid group invite link")
	}
	recipient, err := resolveMessageRecipient(ctx, client, mobileNumber, recipientGroupJID)
	if err != nil {
		return SendGroupInviteMessageResult{}, err
	}
	groupName := strings.TrimSpace(info.Name)
	if groupName == "" {
		groupName = group.String()
	}
	invite := &waE2E.GroupInviteMessage{
		GroupJID:         proto.String(group.String()),
		InviteCode:       proto.String(code),
		InviteExpiration: proto.Int64(0),
		GroupName:        proto.String(groupName),
	}
	if caption != "" {
		invite.Caption = proto.String(caption)
	}
	message := &waE2E.Message{GroupInviteMessage: invite}
	response, err := client.SendMessage(ctx, recipient, message)
	if err != nil {
		return SendGroupInviteMessageResult{}, fmt.Errorf("send WhatsApp group invite card: %w", err)
	}
	historyStored := storeOutgoingMessage(id, service, client, recipient, response.ID, response.Timestamp, "group_invite", message)
	return SendGroupInviteMessageResult{
		ID:            response.ID,
		Recipient:     recipient.String(),
		GroupJID:      group.String(),
		GroupName:     groupName,
		InviteLink:    inviteLink,
		Timestamp:     response.Timestamp.UTC().Format(time.RFC3339Nano),
		HistoryStored: historyStored,
	}, nil
}
