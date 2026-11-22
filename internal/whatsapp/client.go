package whatsapp

import (
	"context"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

type Service struct {
	Client *whatsmeow.Client
	store  *sqlstore.Container
}

func eventHandler(client *whatsmeow.Client) func(evt any) {
	return func(evt any) {
		switch v := evt.(type) {
		case *events.Connected:
			fmt.Println("Connected to WhatsApp!")
		case *events.Disconnected:
			fmt.Println("Disconnected from WhatsApp.")
		case *events.LoggedOut:
			fmt.Println("Logged out from WhatsApp. Delete whatsmeow.db to re-pair.")
		case *events.Message:
			if v.Message == nil {
				return
			}
			text := v.Message.GetConversation()
			if ext := v.Message.GetExtendedTextMessage(); ext != nil && text == "" {
				text = ext.GetText()
			}
			if text != "" {
				fmt.Printf("[%s] %s: %s\n", v.Info.Chat.String(), v.Info.Sender.User, text)
			}
		}
	}
}

func NewService(ctx context.Context, dbDSN, phoneNumber string) (*Service, error) {
	dbLog := waLog.Stdout("Database", "WARN", true)
	clientLog := waLog.Stdout("Client", "INFO", true)

	container, err := sqlstore.New(ctx, "sqlite3", dbDSN, dbLog)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get device: %w", err)
	}

	client := whatsmeow.NewClient(deviceStore, clientLog)
	client.AddEventHandler(eventHandler(client))

	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	if client.Store.ID == nil {
		code, err := client.PairPhone(ctx, phoneNumber, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
		if err != nil {
			client.Disconnect()
			return nil, fmt.Errorf("failed to request pairing code: %w", err)
		}

		fmt.Printf("WhatsApp Pairing Code for +%s: %s\n", phoneNumber, code)
		fmt.Println("On your phone: WhatsApp -> Linked Devices -> Link a Device -> Link with phone number instead")
	}

	return &Service{
		Client: client,
		store:  container,
	}, nil
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	if s.Client != nil {
		s.Client.Disconnect()
	}
}