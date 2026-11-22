package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

const (
	linkedDeviceName     = "NotifyPro - Perfect Innovations"
	maxQRPairingTimeouts = 3
)

var (
	ErrSessionAlreadyExists  = errors.New("session is already initialized or initializing")
	ErrSessionAlreadyRunning = errors.New("WhatsApp session is already running")
	ErrManagerClosed         = errors.New("WhatsApp session manager is closed")
	ErrSessionInitializing   = errors.New("WhatsApp session is still initializing")
	ErrSessionNotPaired      = errors.New("WhatsApp session has no paired device to resume")
	ErrSessionNumberMismatch = errors.New("WhatsApp account number does not match phoneNumber")
	ErrSessionNotFound       = errors.New("WhatsApp session not found")
	ErrQRCodeUnavailable     = errors.New("QR code is not available")
	sessionIDPattern         = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

type SessionInitResult struct {
	Status      string
	PairingCode string
	QRCode      string
	Existing    bool
}

type SessionStatus struct {
	ID                 string `json:"id"`
	Status             string `json:"status"`
	Connected          bool   `json:"connected"`
	WebsocketConnected bool   `json:"websocketConnected"`
	Authenticated      bool   `json:"authenticated"`
	SessionExists      bool   `json:"sessionExists"`
	SessionLoaded      bool   `json:"sessionLoaded"`
}

type Service struct {
	Client       *whatsmeow.Client
	container    *sqlstore.Container
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.RWMutex
	closeOnce    sync.Once
	mismatchOnce sync.Once
	rejected     atomic.Bool
	loggedOut    atomic.Bool
	manager      *Manager
	id           string
	phone        string
	mode         string
	result       SessionInitResult
	databasePath string
	qrTimeouts   int
}

type Manager struct {
	baseDSN      string
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.RWMutex
	sessions     map[string]*Service
	initializing map[string]struct{}
	closed       bool
}

func NewManager(baseDSN string) *Manager {
	waStore.SetOSInfo(linkedDeviceName, waStore.GetWAVersion())
	waStore.DeviceProps.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()

	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		baseDSN:      baseDSN,
		ctx:          ctx,
		cancel:       cancel,
		sessions:     make(map[string]*Service),
		initializing: make(map[string]struct{}),
	}
}

func ValidSessionID(id string) bool {
	return sessionIDPattern.MatchString(id)
}

func eventHandler(client *whatsmeow.Client) func(evt any) {
	return func(evt any) {
		switch v := evt.(type) {
		case *events.Connected:
			fmt.Println("Connected to WhatsApp!")
		case *events.Disconnected:
			fmt.Println("Disconnected from WhatsApp.")
		case *events.LoggedOut:
			fmt.Println("Logged out from WhatsApp.")
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

func (m *Manager) Initialize(ctx context.Context, id, phoneNumber, mode string) (SessionInitResult, error) {
	if !ValidSessionID(id) {
		return SessionInitResult{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)
	if len(phoneNumber) < 10 || len(phoneNumber) > 15 {
		return SessionInitResult{}, errors.New("phoneNumber must contain 10 to 15 digits")
	}
	for _, digit := range phoneNumber {
		if digit < '0' || digit > '9' {
			return SessionInitResult{}, errors.New("phoneNumber must contain 10 to 15 digits")
		}
	}
	if mode != "code" && mode != "qr" {
		return SessionInitResult{}, errors.New("mode must be code or qr")
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return SessionInitResult{}, ErrManagerClosed
	}
	if service, exists := m.sessions[id]; exists {
		m.mu.Unlock()
		if service.phone == phoneNumber && service.mode == mode {
			result := service.sessionResult()
			result.Existing = true
			return result, nil
		}
		return SessionInitResult{}, ErrSessionAlreadyExists
	}
	if _, exists := m.initializing[id]; exists {
		m.mu.Unlock()
		return SessionInitResult{}, ErrSessionAlreadyExists
	}
	m.initializing[id] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.initializing, id)
		m.mu.Unlock()
	}()

	sessionDSN, err := makeSessionDSN(m.baseDSN, id)
	if err != nil {
		return SessionInitResult{}, err
	}

	dbLog := waLog.Stdout("Database", "WARN", true)
	clientLog := waLog.Stdout("Client", "INFO", true)
	container, err := sqlstore.New(m.ctx, "sqlite3", sessionDSN, dbLog)
	if err != nil {
		return SessionInitResult{}, fmt.Errorf("failed to create session store: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(m.ctx)
	if err != nil {
		_ = container.Close()
		return SessionInitResult{}, fmt.Errorf("failed to get session device: %w", err)
	}
	if deviceStore.ID != nil && phoneNumberFromJID(*deviceStore.ID) != phoneNumber {
		fmt.Printf("WhatsApp session %s number mismatch; clearing local session before connecting\n", id)
		if err = deviceStore.Delete(m.ctx); err != nil {
			_ = container.Close()
			return SessionInitResult{}, fmt.Errorf("failed to clear mismatched WhatsApp device: %w", err)
		}
		if err = container.Close(); err != nil {
			return SessionInitResult{}, fmt.Errorf("failed to close mismatched WhatsApp session store: %w", err)
		}
		if err = removeSessionDatabase(sqlitePath(sessionDSN)); err != nil {
			return SessionInitResult{}, fmt.Errorf("failed to delete mismatched WhatsApp session database: %w", err)
		}
		return SessionInitResult{}, ErrSessionNumberMismatch
	}

	client := whatsmeow.NewClient(deviceStore, clientLog)
	handleEvent := eventHandler(client)
	sessionCtx, sessionCancel := context.WithCancel(m.ctx)
	service := &Service{
		Client:       client,
		container:    container,
		ctx:          sessionCtx,
		cancel:       sessionCancel,
		databasePath: sqlitePath(sessionDSN),
		manager:      m,
		id:           id,
		phone:        phoneNumber,
		mode:         mode,
	}
	client.AddEventHandler(func(evt any) {
		handleEvent(evt)
		if pairSuccess, ok := evt.(*events.PairSuccess); ok && phoneNumberFromJID(pairSuccess.ID) != phoneNumber {
			go service.rejectMismatchedNumber()
		}
	})
	keepSession := false
	defer func() {
		if !keepSession {
			sessionCancel()
			service.Close()
		}
	}()

	var qrChannel <-chan whatsmeow.QRChannelItem
	if deviceStore.ID == nil {
		qrChannel, err = client.GetQRChannel(sessionCtx)
		if err != nil {
			return SessionInitResult{}, fmt.Errorf("failed to prepare pairing: %w", err)
		}
	}

	if err = client.Connect(); err != nil {
		return SessionInitResult{}, fmt.Errorf("failed to connect WhatsApp client: %w", err)
	}

	result := SessionInitResult{Status: "connected"}
	if deviceStore.ID == nil {
		pairCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		qrCode, waitErr := waitForQR(pairCtx, qrChannel)
		if waitErr != nil {
			return SessionInitResult{}, waitErr
		}
		result.Status = "awaiting_pairing"
		if mode == "qr" {
			result.QRCode = qrCode
		}
		service.setSessionResult(result)
		go m.drainQRChannel(qrChannel, service)
		if mode == "code" {
			pairingCode, pairErr := client.PairPhone(pairCtx, phoneNumber, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
			if pairErr != nil {
				return SessionInitResult{}, fmt.Errorf("failed to request pairing code: %w", pairErr)
			}
			service.setPairingCode(pairingCode)
		}
	} else {
		service.setSessionResult(result)
	}

	result = service.sessionResult()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return SessionInitResult{}, ErrManagerClosed
	}
	if service.rejected.Load() {
		m.mu.Unlock()
		return SessionInitResult{}, ErrSessionNumberMismatch
	}
	m.sessions[id] = service
	m.mu.Unlock()
	keepSession = true
	return result, nil
}

func (m *Manager) Start(id string) (SessionStatus, error) {
	if !ValidSessionID(id) {
		return SessionStatus{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return SessionStatus{}, ErrManagerClosed
	}
	if _, exists := m.sessions[id]; exists {
		m.mu.Unlock()
		return SessionStatus{}, ErrSessionAlreadyRunning
	}
	if _, exists := m.initializing[id]; exists {
		m.mu.Unlock()
		return SessionStatus{}, ErrSessionInitializing
	}
	m.initializing[id] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.initializing, id)
		m.mu.Unlock()
	}()

	sessionDSN, err := makeSessionDSN(m.baseDSN, id)
	if err != nil {
		return SessionStatus{}, err
	}
	databasePath := sqlitePath(sessionDSN)
	if _, err := os.Stat(databasePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SessionStatus{}, ErrSessionNotFound
		}
		return SessionStatus{}, fmt.Errorf("failed to check persisted WhatsApp session: %w", err)
	}

	dbLog := waLog.Stdout("Database", "WARN", true)
	clientLog := waLog.Stdout("Client", "INFO", true)
	container, err := sqlstore.New(m.ctx, "sqlite3", sessionDSN, dbLog)
	if err != nil {
		return SessionStatus{}, fmt.Errorf("failed to open session store: %w", err)
	}
	devices, err := container.GetAllDevices(m.ctx)
	if err != nil {
		_ = container.Close()
		return SessionStatus{}, fmt.Errorf("failed to read stored WhatsApp devices: %w", err)
	}
	if len(devices) == 0 || devices[0].ID == nil {
		_ = container.Close()
		return SessionStatus{}, ErrSessionNotPaired
	}

	deviceStore := devices[0]
	client := whatsmeow.NewClient(deviceStore, clientLog)
	sessionCtx, sessionCancel := context.WithCancel(m.ctx)
	service := &Service{
		Client:       client,
		container:    container,
		ctx:          sessionCtx,
		cancel:       sessionCancel,
		databasePath: databasePath,
		manager:      m,
		id:           id,
		phone:        phoneNumberFromJID(*deviceStore.ID),
		mode:         "resume",
		result:       SessionInitResult{Status: "connecting"},
	}
	handleEvent := eventHandler(client)
	client.AddEventHandler(func(evt any) {
		handleEvent(evt)
		switch evt.(type) {
		case *events.Connected:
			service.setSessionResult(SessionInitResult{Status: "connected"})
		case *events.Disconnected:
			service.mu.Lock()
			if service.result.Status == "connected" || service.result.Status == "connecting" {
				service.result.Status = "disconnected"
			}
			service.mu.Unlock()
		case *events.LoggedOut:
			service.loggedOut.Store(true)
			service.setSessionResult(SessionInitResult{Status: "disconnected"})
		}
	})

	if err := client.Connect(); err != nil {
		sessionCancel()
		service.Close()
		return SessionStatus{}, fmt.Errorf("failed to reconnect WhatsApp client: %w", err)
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		sessionCancel()
		service.Close()
		return SessionStatus{}, ErrManagerClosed
	}
	m.sessions[id] = service
	m.mu.Unlock()

	return m.GetStatus(id)
}

func makeSessionDSN(baseDSN, id string) (string, error) {
	if !strings.HasPrefix(baseDSN, "file:") {
		return "", errors.New("DB_DSN must be a persistent SQLite file DSN beginning with file:")
	}

	pathAndQuery := strings.TrimPrefix(baseDSN, "file:")
	_, query, hasQuery := strings.Cut(pathAndQuery, "?")
	sessionPath, err := sessionDatabasePath(baseDSN, id)
	if err != nil {
		return "", err
	}

	if dir := filepath.Dir(sessionPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", fmt.Errorf("failed to create session database directory: %w", err)
		}
	}

	sessionDSN := "file:" + sessionPath
	if hasQuery {
		sessionDSN += "?" + query
	}
	return sessionDSN, nil
}

func sessionDatabasePath(baseDSN, id string) (string, error) {
	if !strings.HasPrefix(baseDSN, "file:") {
		return "", errors.New("DB_DSN must be a persistent SQLite file DSN beginning with file:")
	}

	pathAndQuery := strings.TrimPrefix(baseDSN, "file:")
	dbPath, _, _ := strings.Cut(pathAndQuery, "?")
	if dbPath == "" || dbPath == ":memory:" {
		return "", errors.New("DB_DSN must point to a persistent SQLite file")
	}

	extension := filepath.Ext(dbPath)
	sessionPath := strings.TrimSuffix(dbPath, extension) + "-" + id + extension
	return sessionPath, nil
}

func sqlitePath(sessionDSN string) string {
	pathAndQuery := strings.TrimPrefix(sessionDSN, "file:")
	dbPath, _, _ := strings.Cut(pathAndQuery, "?")
	return dbPath
}

func waitForQR(ctx context.Context, qrChannel <-chan whatsmeow.QRChannelItem) (string, error) {
	select {
	case item, ok := <-qrChannel:
		if !ok {
			return "", errors.New("WhatsApp closed the QR channel before sending a code")
		}
		if item.Event == whatsmeow.QRChannelEventCode {
			return item.Code, nil
		}
		if item.Error != nil {
			return "", fmt.Errorf("WhatsApp pairing failed: %w", item.Error)
		}
		return "", fmt.Errorf("unexpected WhatsApp pairing event: %s", item.Event)
	case <-ctx.Done():
		return "", fmt.Errorf("timed out waiting for WhatsApp pairing code: %w", ctx.Err())
	}
}

func (m *Manager) drainQRChannel(qrChannel <-chan whatsmeow.QRChannelItem, service *Service) {
	timedOut := false
	for item := range qrChannel {
		service.mu.Lock()
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			if service.mode == "qr" {
				service.result.QRCode = item.Code
			}
		case whatsmeow.QRChannelSuccess.Event:
			service.result.Status = "connected"
			service.result.PairingCode = ""
			service.result.QRCode = ""
		case whatsmeow.QRChannelTimeout.Event:
			service.qrTimeouts++
			service.result.Status = "refreshing_qr"
			service.result.PairingCode = ""
			service.result.QRCode = ""
			timedOut = true
		case whatsmeow.QRChannelEventError:
			service.result.Status = "pairing_failed"
			service.result.PairingCode = ""
			service.result.QRCode = ""
		}
		service.mu.Unlock()
	}
	if service.ctx.Err() != nil {
		return
	}
	if !timedOut {
		return
	}

	service.mu.RLock()
	timeoutCount := service.qrTimeouts
	service.mu.RUnlock()
	if timeoutCount >= maxQRPairingTimeouts {
		service.setSessionResult(SessionInitResult{Status: "pairing_timeout"})
		fmt.Printf("WhatsApp session %s reached %d QR pairing timeouts; deleting session\n", service.id, timeoutCount)
		m.deleteExpiredQRSession(service)
		return
	}

	if err := m.startNextPairingAttempt(service); err != nil {
		service.setSessionResult(SessionInitResult{Status: "pairing_failed"})
		fmt.Printf("WhatsApp session %s failed to refresh QR code: %v\n", service.id, err)
		return
	}
}

func (m *Manager) startNextPairingAttempt(service *Service) error {
	if service.ctx.Err() != nil {
		return service.ctx.Err()
	}
	service.Client.Disconnect()
	qrChannel, err := service.Client.GetQRChannel(service.ctx)
	if err != nil {
		return fmt.Errorf("failed to prepare QR pairing: %w", err)
	}
	if err = service.Client.Connect(); err != nil {
		return fmt.Errorf("failed to reconnect WhatsApp client: %w", err)
	}

	qrCtx, cancel := context.WithTimeout(service.ctx, 30*time.Second)
	defer cancel()
	qrCode, err := waitForQR(qrCtx, qrChannel)
	if err != nil {
		return err
	}
	result := SessionInitResult{Status: "awaiting_pairing"}
	if service.mode == "qr" {
		result.QRCode = qrCode
	}
	service.setSessionResult(result)
	go m.drainQRChannel(qrChannel, service)
	if service.mode == "code" {
		pairingCode, pairErr := service.Client.PairPhone(service.ctx, service.phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
		if pairErr != nil {
			return fmt.Errorf("failed to request refreshed pairing code: %w", pairErr)
		}
		service.setPairingCode(pairingCode)
	}
	return nil
}

func (m *Manager) deleteExpiredQRSession(service *Service) {
	service.rejected.Store(true)
	service.Close()

	if err := removeSessionDatabase(service.databasePath); err != nil {
		fmt.Printf("WhatsApp session %s failed to delete database files: %v\n", service.id, err)
	}
	m.removeSession(service.id, service)
}

func removeSessionDatabase(databasePath string) error {
	var removeErrors []error
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := databasePath + suffix
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErrors = append(removeErrors, fmt.Errorf("remove %s: %w", path, err))
		}
	}
	return errors.Join(removeErrors...)
}

func (s *Service) setSessionResult(result SessionInitResult) {
	s.mu.Lock()
	s.result = result
	s.mu.Unlock()
}

func (s *Service) setPairingCode(code string) {
	s.mu.Lock()
	if s.result.Status == "awaiting_pairing" {
		s.result.PairingCode = code
	}
	s.mu.Unlock()
}

func (s *Service) sessionResult() SessionInitResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.result
}

func (s *Service) rejectMismatchedNumber() {
	s.mismatchOnce.Do(func() {
		s.rejected.Store(true)
		fmt.Printf("WhatsApp session %s number mismatch; disconnecting and clearing local session\n", s.id)
		s.setSessionResult(SessionInitResult{Status: "number_mismatch"})
		go s.clearMismatchedSession()
	})
}

func phoneNumberFromJID(jid types.JID) string {
	return strings.SplitN(jid.User, ":", 2)[0]
}

func (s *Service) clearMismatchedSession() {
	s.manager.removeSession(s.id, s)

	s.Client.Disconnect()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if s.Client.Store != nil && s.Client.Store.ID != nil {
		if err := s.Client.Store.Delete(cleanupCtx); err != nil {
			fmt.Printf("WhatsApp session %s failed to clear mismatched device data: %v\n", s.id, err)
		}
	}
	cancel()
	s.Close()
	if err := removeSessionDatabase(s.databasePath); err != nil {
		fmt.Printf("WhatsApp session %s failed to delete mismatched session database files: %v\n", s.id, err)
	}
}

func (m *Manager) removeSession(id string, expected *Service) {
	m.mu.Lock()
	if m.sessions[id] == expected {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
}

func (m *Manager) Terminate(ctx context.Context, id string) error {
	if !ValidSessionID(id) {
		return errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)

	m.mu.Lock()
	if _, initializing := m.initializing[id]; initializing {
		m.mu.Unlock()
		return ErrSessionInitializing
	}
	service, exists := m.sessions[id]
	if !exists {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	delete(m.sessions, id)
	service.rejected.Store(true)
	service.cancel()
	m.mu.Unlock()

	var operationErrors []error
	if service.Client.Store != nil && service.Client.Store.ID != nil {
		if err := service.Client.Logout(ctx); err != nil {
			operationErrors = append(operationErrors, fmt.Errorf("WhatsApp remote logout failed: %w", err))
			service.Client.Disconnect()
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if deleteErr := service.Client.Store.Delete(cleanupCtx); deleteErr != nil {
				operationErrors = append(operationErrors, fmt.Errorf("failed to clear local device data: %w", deleteErr))
			}
			cancel()
		}
	}

	service.Close()
	if err := removeSessionDatabase(service.databasePath); err != nil {
		operationErrors = append(operationErrors, fmt.Errorf("failed to delete local session database: %w", err))
	}
	return errors.Join(operationErrors...)
}

func (m *Manager) CloseSession(id string) (SessionStatus, error) {
	if !ValidSessionID(id) {
		return SessionStatus{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return SessionStatus{}, ErrManagerClosed
	}
	if _, initializing := m.initializing[id]; initializing {
		m.mu.Unlock()
		return SessionStatus{}, ErrSessionInitializing
	}
	service, exists := m.sessions[id]
	if exists {
		if service.rejected.Load() {
			m.mu.Unlock()
			return SessionStatus{}, ErrSessionNotFound
		}
		delete(m.sessions, id)
		service.Close()
	}
	m.mu.Unlock()

	return m.GetStatus(id)
}

func (m *Manager) GetClient(id string) (*whatsmeow.Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	service, exists := m.sessions[strings.ToLower(id)]
	if !exists || service.rejected.Load() {
		return nil, false
	}
	return service.Client, true
}

func (m *Manager) GetQRCode(id string) (string, error) {
	m.mu.RLock()
	service, exists := m.sessions[strings.ToLower(id)]
	m.mu.RUnlock()
	if !exists || service.rejected.Load() {
		return "", ErrSessionNotFound
	}

	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.mode != "qr" || service.result.Status != "awaiting_pairing" || service.result.QRCode == "" {
		return "", ErrQRCodeUnavailable
	}
	return service.result.QRCode, nil
}

func (m *Manager) GetStatus(id string) (SessionStatus, error) {
	if !ValidSessionID(id) {
		return SessionStatus{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)

	m.mu.RLock()
	service, exists := m.sessions[id]
	_, initializing := m.initializing[id]
	m.mu.RUnlock()
	if !exists {
		if initializing {
			return SessionStatus{
				ID:            id,
				Status:        "initializing",
				SessionExists: true,
			}, nil
		}
		databasePath, err := sessionDatabasePath(m.baseDSN, id)
		if err != nil {
			return SessionStatus{}, err
		}
		if _, err := os.Stat(databasePath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return SessionStatus{}, ErrSessionNotFound
			}
			return SessionStatus{}, fmt.Errorf("failed to check persisted WhatsApp session: %w", err)
		}
		return SessionStatus{
			ID:            id,
			Status:        "disconnected",
			SessionExists: true,
			SessionLoaded: false,
		}, nil
	}
	if service.rejected.Load() {
		return SessionStatus{}, ErrSessionNotFound
	}

	websocketConnected := service.Client.IsConnected()
	authenticated := service.Client.IsLoggedIn()
	connected := websocketConnected && authenticated
	result := service.sessionResult()

	status := result.Status
	switch {
	case service.loggedOut.Load():
		status = "disconnected"
	case connected:
		status = "connected"
	case websocketConnected && !authenticated:
		if status == "" || status == "connected" || status == "disconnected" {
			status = "awaiting_pairing"
		}
	case !websocketConnected && (status == "" || status == "connected" || status == "connecting"):
		status = "disconnected"
	}

	return SessionStatus{
		ID:                 id,
		Status:             status,
		Connected:          connected,
		WebsocketConnected: websocketConnected,
		Authenticated:      authenticated,
		SessionExists:      true,
		SessionLoaded:      true,
	}, nil
}

func (m *Manager) ListStatuses() ([]SessionStatus, error) {
	sessionIDs := make(map[string]struct{})

	m.mu.RLock()
	for id := range m.sessions {
		sessionIDs[id] = struct{}{}
	}
	for id := range m.initializing {
		sessionIDs[id] = struct{}{}
	}
	m.mu.RUnlock()

	dbPath := sqlitePath(m.baseDSN)
	directory := filepath.Dir(dbPath)
	if directory == "" {
		directory = "."
	}
	baseName := filepath.Base(dbPath)
	extension := filepath.Ext(baseName)
	prefix := strings.TrimSuffix(baseName, extension) + "-"

	entries, err := os.ReadDir(directory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to list persisted WhatsApp sessions: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, extension) {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, prefix), extension)
		if ValidSessionID(id) {
			sessionIDs[strings.ToLower(id)] = struct{}{}
		}
	}

	ids := make([]string, 0, len(sessionIDs))
	for id := range sessionIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	statuses := make([]SessionStatus, 0, len(ids))
	for _, id := range ids {
		status, err := m.GetStatus(id)
		if errors.Is(err, ErrSessionNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to get status for WhatsApp session %s: %w", id, err)
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	sessions := make([]*Service, 0, len(m.sessions))
	for _, service := range m.sessions {
		sessions = append(sessions, service)
	}
	m.sessions = make(map[string]*Service)
	m.mu.Unlock()

	for _, service := range sessions {
		service.Close()
	}
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.Client != nil {
			s.Client.Disconnect()
		}
		if s.container != nil {
			if err := s.container.Close(); err != nil {
				fmt.Println("Failed to close WhatsApp session store:", err)
			}
		}
	})
}
