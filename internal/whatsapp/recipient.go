package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

var ErrInvalidRecipient = errors.New("invalid recipient")

func ValidateRecipient(mobileNumber, groupJID string) (string, string, error) {
	mobileNumber = strings.TrimSpace(mobileNumber)
	groupJID = strings.TrimSpace(groupJID)
	if (mobileNumber == "") == (groupJID == "") {
		return "", "", fmt.Errorf("%w: provide exactly one of mobileNumber or groupJid", ErrInvalidRecipient)
	}
	if mobileNumber != "" {
		digits := strings.TrimPrefix(mobileNumber, "+")
		if len(digits) < 10 || len(digits) > 15 || !allPhoneNumberDigits(digits) {
			return "", "", fmt.Errorf("%w: mobileNumber must contain 10 to 15 digits, optionally prefixed with +", ErrInvalidRecipient)
		}
		return digits, "", nil
	}
	target, err := types.ParseJID(groupJID)
	if err != nil || target.Server != types.GroupServer || target.User == "" {
		return "", "", fmt.Errorf("%w: groupJid must be a valid WhatsApp group JID", ErrInvalidRecipient)
	}
	return "", target.String(), nil
}

func resolveMessageRecipient(ctx context.Context, client *whatsmeow.Client, mobileNumber, groupJID string) (types.JID, error) {
	digits, normalizedGroup, err := ValidateRecipient(mobileNumber, groupJID)
	if err != nil {
		return types.JID{}, err
	}
	if normalizedGroup != "" {
		return types.ParseJID(normalizedGroup)
	}
	return resolveWhatsAppRecipient(ctx, client, digits)
}
