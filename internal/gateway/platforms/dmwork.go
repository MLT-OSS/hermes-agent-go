// Package platforms implements messaging platform adapters for the gateway.
//
// DMWork adapter: WuKongIM binary protocol over WebSocket + REST API.
package platforms

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hermes-agent/hermes-agent-go/internal/gateway"
	"golang.org/x/crypto/curve25519"
)

// ─── DMWork Constants ───────────────────────────────────────────────────────

const (
	dmworkProtoVersion = 4
	dmworkPingInterval = 60 * time.Second
	dmworkPingMaxRetry = 3

	// WuKongIM Packet Types
	pktConnect    = 1
	pktConnack    = 2
	pktSend       = 3
	pktSendack    = 4
	pktRecv       = 5
	pktRecvack    = 6
	pktPing       = 7
	pktPong       = 8
	pktDisconnect = 9
)

// ─── DMWork Types ───────────────────────────────────────────────────────────

type dmworkChannelType int

const (
	dmworkDM    dmworkChannelType = 1
	dmworkGroup dmworkChannelType = 2
)

type dmworkMessageType int

const (
	dmworkText  dmworkMessageType = 1
	dmworkImage dmworkMessageType = 2
	dmworkFile  dmworkMessageType = 8
)

type botRegisterResp struct {
	RobotID        string `json:"robot_id"`
	IMToken        string `json:"im_token"`
	WSURL          string `json:"ws_url"`
	APIURL         string `json:"api_url"`
	OwnerUID       string `json:"owner_uid"`
	OwnerChannelID string `json:"owner_channel_id"`
}

type botMessage struct {
	MessageID   string            `json:"message_id"`
	MessageSeq  int               `json:"message_seq"`
	FromUID     string            `json:"from_uid"`
	ChannelID   string            `json:"channel_id"`
	ChannelType dmworkChannelType `json:"channel_type"`
	Timestamp   int               `json:"timestamp"`
	Payload     messagePayload    `json:"payload"`
}

type messagePayload struct {
	Type    dmworkMessageType      `json:"type"`
	Content string                 `json:"content,omitempty"`
	Mention *mentionPayload        `json:"mention,omitempty"`
	Reply   *replyPayload          `json:"reply,omitempty"`
	Extra   map[string]interface{} `json:"-"`
}

type mentionPayload struct {
	UIDs []string `json:"uids,omitempty"`
	All  bool     `json:"all,omitempty"`
}

type replyPayload struct {
	FromUID  string          `json:"from_uid,omitempty"`
	FromName string          `json:"from_name,omitempty"`
	Payload  *messagePayload `json:"payload,omitempty"`
}

// ─── DMWork Adapter ─────────────────────────────────────────────────────────

// DMWorkAdapter implements the gateway.PlatformAdapter for DMWork.
type DMWorkAdapter struct {
	BasePlatformAdapter

	apiURL   string
	botToken string

	// Set after registration.
	robotID  string
	imToken  string
	wsURL    string
	ownerUID string

	// WebSocket connection state.
	ws            *websocket.Conn
	wsMu          sync.Mutex
	aesKey        []byte
	aesIV         []byte
	connected     bool
	pingRetry     int
	heartTicker   *time.Ticker
	heartDone     chan struct{}
	serverVersion int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewDMWorkAdapter creates a new DMWork adapter.
func NewDMWorkAdapter(apiURL, botToken string) *DMWorkAdapter {
	return &DMWorkAdapter{
		BasePlatformAdapter: NewBasePlatformAdapter(gateway.PlatformDMWork),
		apiURL:              strings.TrimRight(apiURL, "/"),
		botToken:            botToken,
	}
}

// Connect registers the bot and establishes the WebSocket connection.
func (d *DMWorkAdapter) Connect(ctx context.Context) error {
	d.ctx, d.cancel = context.WithCancel(ctx)

	// Register bot.
	reg, err := d.registerBot(false)
	if err != nil {
		return fmt.Errorf("dmwork bot registration: %w", err)
	}

	d.robotID = reg.RobotID
	d.imToken = reg.IMToken
	d.wsURL = reg.WSURL
	d.ownerUID = reg.OwnerUID

	if reg.APIURL != "" {
		d.apiURL = strings.TrimRight(reg.APIURL, "/")
	}

	slog.Info("DMWork bot registered", "robot_id", d.robotID, "ws_url", d.wsURL)

	// Connect WebSocket.
	if err := d.connectWS(); err != nil {
		return fmt.Errorf("dmwork websocket: %w", err)
	}

	return nil
}

// Disconnect closes the WebSocket connection.
func (d *DMWorkAdapter) Disconnect() error {
	if d.cancel != nil {
		d.cancel()
	}
	d.stopHeart()
	d.wsMu.Lock()
	if d.ws != nil {
		d.ws.Close()
		d.ws = nil
	}
	d.wsMu.Unlock()
	d.wg.Wait()
	d.BasePlatformAdapter.connected = false
	return nil
}

// Send sends a text message to a DMWork channel.
func (d *DMWorkAdapter) Send(ctx context.Context, chatID, content string, metadata map[string]string) (*gateway.SendResult, error) {
	channelType := dmworkDM
	if metadata != nil {
		if ct, ok := metadata["channel_type"]; ok && ct == "2" {
			channelType = dmworkGroup
		}
	}

	parts := splitMessage(content, MaxMessageLength)
	for _, part := range parts {
		if err := d.sendMessage(chatID, channelType, part); err != nil {
			return &gateway.SendResult{Success: false, Error: err.Error()}, err
		}
	}
	return &gateway.SendResult{Success: true}, nil
}

// SendDocument sends a file (not yet implemented).
func (d *DMWorkAdapter) SendDocument(ctx context.Context, chatID, filePath string, metadata map[string]string) (*gateway.SendResult, error) {
	return &gateway.SendResult{Success: false, Error: "document sending not yet implemented"}, nil
}

// SendImage sends an image (not yet implemented).
func (d *DMWorkAdapter) SendImage(ctx context.Context, chatID, imagePath, caption string, metadata map[string]string) (*gateway.SendResult, error) {
	return &gateway.SendResult{Success: false, Error: "image sending not yet implemented"}, nil
}

// SendVoice sends a voice message (not yet implemented).
func (d *DMWorkAdapter) SendVoice(ctx context.Context, chatID, audioPath string, metadata map[string]string) (*gateway.SendResult, error) {
	return &gateway.SendResult{Success: false, Error: "voice sending not yet implemented"}, nil
}

// SendTyping sends a typing indicator.
func (d *DMWorkAdapter) SendTyping(ctx context.Context, chatID string) error {
	channelType := dmworkDM // default; could infer from session
	d.sendTyping(chatID, channelType)
	return nil
}

// IsConnected returns the connection status.
func (d *DMWorkAdapter) IsConnected() bool {
	return d.BasePlatformAdapter.connected
}

// ─── REST API ───────────────────────────────────────────────────────────────

func (d *DMWorkAdapter) registerBot(forceRefresh bool) (*botRegisterResp, error) {
	path := "/v1/bot/register"
	if forceRefresh {
		path += "?force_refresh=true"
	}

	body, err := d.postJSON(path, map[string]interface{}{})
	if err != nil {
		return nil, err
	}

	var resp botRegisterResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse register response: %w", err)
	}
	return &resp, nil
}

func (d *DMWorkAdapter) sendMessage(channelID string, channelType dmworkChannelType, content string) error {
	_, err := d.postJSON("/v1/bot/sendMessage", map[string]interface{}{
		"channel_id":   channelID,
		"channel_type": int(channelType),
		"payload": map[string]interface{}{
			"type":    int(dmworkText),
			"content": content,
		},
	})
	return err
}

func (d *DMWorkAdapter) sendTyping(channelID string, channelType dmworkChannelType) {
	d.postJSON("/v1/bot/typing", map[string]interface{}{
		"channel_id":   channelID,
		"channel_type": int(channelType),
	})
}

func (d *DMWorkAdapter) postJSON(path string, payload interface{}) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	url := d.apiURL + path
	req, err := http.NewRequestWithContext(d.ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.botToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dmwork api %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("dmwork api %s failed (HTTP %d): %s", path, resp.StatusCode, string(body))
	}

	return body, nil
}

// ─── WebSocket Protocol ─────────────────────────────────────────────────────

func (d *DMWorkAdapter) connectWS() error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(d.ctx, d.wsURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}

	d.wsMu.Lock()
	d.ws = conn
	d.wsMu.Unlock()

	// Generate DH key pair.
	var privateKey [32]byte
	if _, err := rand.Read(privateKey[:]); err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	// Clamp private key for Curve25519.
	privateKey[0] &= 248
	privateKey[31] &= 127
	privateKey[31] |= 64

	publicKey, err := curve25519.X25519(privateKey[:], curve25519.Basepoint)
	if err != nil {
		return fmt.Errorf("compute public key: %w", err)
	}

	pubKeyB64 := base64.StdEncoding.EncodeToString(publicKey)

	// Send CONNECT packet.
	connectPkt := encodeConnectPacket(d.robotID, d.imToken, pubKeyB64)
	if err := conn.WriteMessage(websocket.BinaryMessage, connectPkt); err != nil {
		return fmt.Errorf("send connect: %w", err)
	}

	// Read CONNACK.
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read connack: %w", err)
	}

	if err := d.handleConnack(msg, privateKey[:]); err != nil {
		return err
	}

	d.BasePlatformAdapter.connected = true
	slog.Info("DMWork WebSocket connected")

	// Start heartbeat.
	d.startHeart()

	// Start message reader.
	d.wg.Add(1)
	go d.readLoop()

	return nil
}

func (d *DMWorkAdapter) handleConnack(data []byte, privateKey []byte) error {
	if len(data) < 2 {
		return fmt.Errorf("connack too short")
	}

	header := data[0]
	hasServerVersion := (header & 0x01) > 0

	// Skip header + variable length.
	offset := 1
	_, n := decodeVariableLength(data[offset:])
	offset += n

	if hasServerVersion {
		if offset >= len(data) {
			return fmt.Errorf("connack: missing server version")
		}
		d.serverVersion = int(data[offset])
		offset++
	}

	// Skip timeDiff (8 bytes).
	if offset+8 > len(data) {
		return fmt.Errorf("connack: missing time diff")
	}
	offset += 8

	// Read reasonCode.
	if offset >= len(data) {
		return fmt.Errorf("connack: missing reason code")
	}
	reasonCode := data[offset]
	offset++

	if reasonCode != 1 {
		return fmt.Errorf("connack failed: reason=%d", reasonCode)
	}

	// Read serverKey (string).
	serverKeyStr, n := readString(data[offset:])
	offset += n

	// Read salt (string).
	salt, _ := readString(data[offset:])

	// Derive AES key from DH shared secret.
	serverPubKey, err := base64.StdEncoding.DecodeString(serverKeyStr)
	if err != nil {
		return fmt.Errorf("decode server key: %w", err)
	}

	sharedSecret, err := curve25519.X25519(privateKey, serverPubKey)
	if err != nil {
		return fmt.Errorf("compute shared secret: %w", err)
	}

	secretB64 := base64.StdEncoding.EncodeToString(sharedSecret)
	aesKeyFull := fmt.Sprintf("%x", md5.Sum([]byte(secretB64)))
	d.aesKey = []byte(aesKeyFull[:16])

	if len(salt) > 16 {
		d.aesIV = []byte(salt[:16])
	} else {
		d.aesIV = []byte(salt)
	}

	return nil
}

func (d *DMWorkAdapter) readLoop() {
	defer d.wg.Done()

	var tempBuf []byte

	for {
		select {
		case <-d.ctx.Done():
			return
		default:
		}

		d.wsMu.Lock()
		ws := d.ws
		d.wsMu.Unlock()
		if ws == nil {
			return
		}

		_, msg, err := ws.ReadMessage()
		if err != nil {
			if d.ctx.Err() != nil {
				return // context cancelled
			}
			slog.Debug("dmwork ws read error", "error", err)
			d.scheduleReconnect()
			return
		}

		tempBuf = append(tempBuf, msg...)
		tempBuf = d.processPackets(tempBuf)
	}
}

func (d *DMWorkAdapter) processPackets(data []byte) []byte {
	for len(data) > 0 {
		header := data[0]
		packetType := header >> 4

		// Single-byte packets.
		if packetType == pktPong {
			d.pingRetry = 0
			data = data[1:]
			continue
		}
		if packetType == pktPing {
			data = data[1:]
			continue
		}

		// Read variable length.
		if len(data) < 2 {
			break
		}
		remLen, n := decodeVariableLength(data[1:])
		totalLen := 1 + n + remLen

		if totalLen > len(data) {
			break // incomplete packet
		}

		pkt := data[:totalLen]
		d.handlePacket(pkt)
		data = data[totalLen:]
	}
	return data
}

func (d *DMWorkAdapter) handlePacket(data []byte) {
	header := data[0]
	packetType := header >> 4

	// Skip header + variable length to get body.
	offset := 1
	_, n := decodeVariableLength(data[offset:])
	offset += n

	switch packetType {
	case pktRecv:
		d.handleRecv(data[offset:])
	case pktDisconnect:
		slog.Warn("dmwork disconnected by server")
	}
}

func (d *DMWorkAdapter) handleRecv(body []byte) {
	if len(body) < 10 {
		return
	}

	offset := 0

	// Setting byte.
	offset++

	// msgKey (string).
	_, n := readString(body[offset:])
	offset += n

	// fromUID.
	fromUID, n := readString(body[offset:])
	offset += n

	// channelID.
	channelID, n := readString(body[offset:])
	offset += n

	// channelType.
	if offset >= len(body) {
		return
	}
	channelType := dmworkChannelType(body[offset])
	offset++

	// expire (v3+).
	if d.serverVersion >= 3 {
		if offset+4 > len(body) {
			return
		}
		offset += 4
	}

	// clientMsgNo.
	_, n = readString(body[offset:])
	offset += n

	// messageID (int64).
	if offset+8 > len(body) {
		return
	}
	messageID := binary.BigEndian.Uint64(body[offset : offset+8])
	offset += 8

	// messageSeq (int32).
	if offset+4 > len(body) {
		return
	}
	messageSeq := int(binary.BigEndian.Uint32(body[offset : offset+4]))
	offset += 4

	// timestamp (int32).
	if offset+4 > len(body) {
		return
	}
	offset += 4

	// Remaining is encrypted payload.
	encryptedPayload := body[offset:]

	// Send RECVACK.
	d.sendRecvack(messageID, messageSeq)

	// Decrypt payload.
	decryptedBytes, err := aesDecryptCBC(encryptedPayload, d.aesKey, d.aesIV)
	if err != nil {
		slog.Debug("dmwork payload decrypt error", "error", err)
		return
	}

	var payload messagePayload
	if err := json.Unmarshal(decryptedBytes, &payload); err != nil {
		slog.Debug("dmwork payload parse error", "error", err)
		return
	}

	// Skip non-text messages.
	if payload.Type != dmworkText || payload.Content == "" {
		return
	}

	// Skip messages from self.
	if fromUID == d.robotID {
		return
	}

	// Build gateway event.
	chatType := "dm"
	if channelType == dmworkGroup {
		chatType = "group"
	}

	event := &gateway.MessageEvent{
		Text:        payload.Content,
		MessageType: gateway.MessageTypeText,
		Source: gateway.SessionSource{
			Platform: gateway.PlatformDMWork,
			ChatID:   channelID,
			UserID:   fromUID,
			ChatType: chatType,
			UserName: fromUID,
		},
	}

	if d.messageHandler != nil {
		d.messageHandler(event)
	}
}

func (d *DMWorkAdapter) sendRecvack(messageID uint64, messageSeq int) {
	var buf bytes.Buffer

	// Body: messageID (int64) + messageSeq (int32).
	var idBuf [8]byte
	binary.BigEndian.PutUint64(idBuf[:], messageID)
	buf.Write(idBuf[:])

	var seqBuf [4]byte
	binary.BigEndian.PutUint32(seqBuf[:], uint32(messageSeq))
	buf.Write(seqBuf[:])

	bodyBytes := buf.Bytes()

	// Frame.
	var frame bytes.Buffer
	frame.WriteByte(byte(pktRecvack<<4) | 0)
	frame.Write(encodeVariableLength(len(bodyBytes)))
	frame.Write(bodyBytes)

	d.wsMu.Lock()
	if d.ws != nil {
		d.ws.WriteMessage(websocket.BinaryMessage, frame.Bytes())
	}
	d.wsMu.Unlock()
}

// ─── Heartbeat ──────────────────────────────────────────────────────────────

func (d *DMWorkAdapter) startHeart() {
	d.heartTicker = time.NewTicker(dmworkPingInterval)
	d.heartDone = make(chan struct{})
	d.pingRetry = 0

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			select {
			case <-d.heartDone:
				return
			case <-d.ctx.Done():
				return
			case <-d.heartTicker.C:
				d.pingRetry++
				if d.pingRetry > dmworkPingMaxRetry {
					slog.Debug("dmwork ping timeout, reconnecting")
					d.scheduleReconnect()
					return
				}
				d.wsMu.Lock()
				if d.ws != nil {
					d.ws.WriteMessage(websocket.BinaryMessage, []byte{byte(pktPing << 4)})
				}
				d.wsMu.Unlock()
			}
		}
	}()
}

func (d *DMWorkAdapter) stopHeart() {
	if d.heartTicker != nil {
		d.heartTicker.Stop()
	}
	if d.heartDone != nil {
		select {
		case <-d.heartDone:
		default:
			close(d.heartDone)
		}
	}
}

func (d *DMWorkAdapter) scheduleReconnect() {
	d.wsMu.Lock()
	if d.ws != nil {
		d.ws.Close()
		d.ws = nil
	}
	d.wsMu.Unlock()

	d.stopHeart()

	// Reconnect after 3 seconds.
	time.AfterFunc(3*time.Second, func() {
		if d.ctx.Err() != nil {
			return
		}
		slog.Info("dmwork reconnecting")
		if err := d.connectWS(); err != nil {
			slog.Warn("dmwork reconnect failed", "error", err)
		}
	})
}

// ─── Binary Protocol Helpers ────────────────────────────────────────────────

func encodeConnectPacket(uid, token, clientKey string) []byte {
	var body bytes.Buffer

	body.WriteByte(dmworkProtoVersion) // version
	body.WriteByte(0)                  // deviceFlag (0 = app)
	writeString(&body, generateDeviceID()+"W")
	writeString(&body, uid)
	writeString(&body, token)

	// clientTimestamp (int64).
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(time.Now().UnixMilli()))
	body.Write(ts[:])

	writeString(&body, clientKey)

	bodyBytes := body.Bytes()

	var frame bytes.Buffer
	frame.WriteByte(byte(pktConnect<<4) | 0)
	frame.Write(encodeVariableLength(len(bodyBytes)))
	frame.Write(bodyBytes)

	return frame.Bytes()
}

func writeString(buf *bytes.Buffer, s string) {
	data := []byte(s)
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(data)))
	buf.Write(lenBuf[:])
	buf.Write(data)
}

func readString(data []byte) (string, int) {
	if len(data) < 2 {
		return "", 0
	}
	l := int(binary.BigEndian.Uint16(data[:2]))
	if l <= 0 {
		return "", 2
	}
	if 2+l > len(data) {
		return "", 2
	}
	return string(data[2 : 2+l]), 2 + l
}

func encodeVariableLength(length int) []byte {
	var buf []byte
	for length > 0 {
		digit := byte(length % 0x80)
		length /= 0x80
		if length > 0 {
			digit |= 0x80
		}
		buf = append(buf, digit)
	}
	if len(buf) == 0 {
		buf = append(buf, 0)
	}
	return buf
}

func decodeVariableLength(data []byte) (int, int) {
	multiplier := 0
	rLength := 0
	bytesRead := 0
	for multiplier < 27 && bytesRead < len(data) {
		b := data[bytesRead]
		bytesRead++
		rLength |= int(b&127) << multiplier
		if b&128 == 0 {
			break
		}
		multiplier += 7
	}
	return rLength, bytesRead
}

func generateDeviceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	id := new(big.Int).SetBytes(b)
	return fmt.Sprintf("%032x", id)[:32]
}

// ─── AES-CBC Encryption ────────────────────────────────────────────────────

func aesDecryptCBC(encrypted, key, iv []byte) ([]byte, error) {
	// The encrypted data is base64-encoded in the WuKongIM protocol.
	ciphertext, err := base64.StdEncoding.DecodeString(string(encrypted))
	if err != nil {
		// Try raw bytes if not base64.
		ciphertext = encrypted
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}

	if len(ciphertext) < aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid ciphertext length: %d", len(ciphertext))
	}

	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	// Remove PKCS7 padding.
	plaintext, err = pkcs7Unpad(plaintext)
	if err != nil {
		return nil, fmt.Errorf("pkcs7 unpad: %w", err)
	}

	return plaintext, nil
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	padding := int(data[len(data)-1])
	if padding > len(data) || padding > aes.BlockSize || padding == 0 {
		return nil, fmt.Errorf("invalid padding: %d", padding)
	}
	for i := len(data) - padding; i < len(data); i++ {
		if data[i] != byte(padding) {
			return nil, fmt.Errorf("invalid pkcs7 padding")
		}
	}
	return data[:len(data)-padding], nil
}

func splitMessage(content string, maxLen int) []string {
	if len(content) <= maxLen {
		return []string{content}
	}
	var parts []string
	for len(content) > 0 {
		end := maxLen
		if end > len(content) {
			end = len(content)
		}
		// Try to split at newline.
		if end < len(content) {
			if idx := strings.LastIndex(content[:end], "\n"); idx > end/2 {
				end = idx + 1
			}
		}
		parts = append(parts, content[:end])
		content = content[end:]
	}
	return parts
}
