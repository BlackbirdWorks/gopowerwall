package tedapi

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/proto/tedapi/combined"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	tagSignatureType   = 0
	tagDomain          = 1
	tagPersonalization = 2
	tagExpiresAt       = 4
	tagEnd             = 0xFF

	tagLenOne        = 1
	tagLenFour       = 4
	rsaSignatureType = 7
	domainEnergyDev  = 7
	expirationOffset = 12
	minBackupSeconds = 60
	tzChicago        = "America/Chicago"
	keyTimezone      = "timezone"
)

// TEDAPIv1r implements RSA-signed transport for Powerwall 3 LAN TEDAPI (/tedapi/v1r).
type TEDAPIv1r struct {
	privateKey     *rsa.PrivateKey
	client         *http.Client
	host           string
	password       string
	token          string
	din            string
	KeyFingerprint string
	publicKeyDER   []byte
	timeout        time.Duration
	poolMaxSize    int
	mu             sync.Mutex
}

// NewTEDAPIv1r initializes a new TEDAPIv1r instance.
func NewTEDAPIv1r(host, password, rsaKeyPath string, timeout time.Duration, poolMaxSize int) (*TEDAPIv1r, error) {
	keyBytes, err := os.ReadFile(rsaKeyPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %s (%s)", backend.ErrRSAKeyParse, rsaKeyPath, err.Error())
	}

	block, _ := pem.Decode(keyBytes)
	if block == nil {
		return nil, fmt.Errorf("%w: failed to parse PEM block from %s", backend.ErrRSAKeyParse, rsaKeyPath)
	}

	var privKey *rsa.PrivateKey
	if key, parseErr := x509.ParsePKCS1PrivateKey(block.Bytes); parseErr == nil {
		privKey = key
	} else if key, parseErr8 := x509.ParsePKCS8PrivateKey(block.Bytes); parseErr8 == nil {
		var ok bool
		privKey, ok = key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%w: key in %s is not an RSA private key", backend.ErrRSAKeyParse, rsaKeyPath)
		}
	} else {
		return nil, fmt.Errorf(
			"%w: unable to parse key from %s: %s",
			backend.ErrRSAKeyParse,
			rsaKeyPath,
			parseErr.Error(),
		)
	}

	pubDER := x509.MarshalPKCS1PublicKey(&privKey.PublicKey)
	fp := sha256.Sum256(pubDER)
	keyFP := hex.EncodeToString(fp[:])

	transport := &http.Transport{
		//nolint:gosec // Local Powerwall gateway uses self-signed HTTPS certificate.
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        poolMaxSize,
		MaxIdleConnsPerHost: poolMaxSize,
		DisableKeepAlives:   poolMaxSize == 0,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	return &TEDAPIv1r{
		host:           host,
		password:       password,
		timeout:        timeout,
		poolMaxSize:    poolMaxSize,
		privateKey:     privKey,
		publicKeyDER:   pubDER,
		KeyFingerprint: keyFP,
		client:         client,
	}, nil
}

// Login authenticates with the gateway via /api/login/Basic to obtain a Bearer token.
func (v *TEDAPIv1r) Login(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	url := fmt.Sprintf("https://%s/api/login/Basic", v.host)
	payload := map[string]any{
		"username": "customer",
		"password": v.password,
		"email":    "customer@customer.domain",
		"clientInfo": map[string]string{
			keyTimezone: tzChicago,
		},
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("v1r login error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("%w: login failed (HTTP %d): %s", backend.ErrLogin, resp.StatusCode, string(b))
	}

	var res struct {
		Token string `json:"token"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&res); decodeErr != nil {
		return fmt.Errorf("v1r login parse error: %w", decodeErr)
	}

	v.token = res.Token
	logger.Load(ctx).DebugContext(ctx, "v1r login successful, token acquired")

	return nil
}

// GetDin queries the gateway DIN via /tedapi/din.
func (v *TEDAPIv1r) GetDin(ctx context.Context) (string, error) {
	v.mu.Lock()
	if v.din != "" {
		din := v.din
		v.mu.Unlock()

		return din, nil
	}
	v.mu.Unlock()

	url := fmt.Sprintf("https://%s/tedapi/din", v.host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	v.mu.Lock()
	if v.token != "" {
		req.Header.Set("Authorization", "Bearer "+v.token)
	}
	v.mu.Unlock()

	resp, err := v.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("v1r get_din error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: get_din failed (HTTP %d)", backend.ErrUnexpectedStatus, resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	din := strings.TrimSpace(string(b))

	v.mu.Lock()
	v.din = din
	v.mu.Unlock()

	return din, nil
}

// BuildTLVPayload builds the binary TLV payload for RSA signing.
func (v *TEDAPIv1r) BuildTLVPayload(din string, expiresAt uint32, innerBytes []byte) []byte {
	var buf bytes.Buffer

	// Tag 0: TAG_SIGNATURE_TYPE = RSA (7)
	buf.WriteByte(tagSignatureType)
	buf.WriteByte(tagLenOne)
	buf.WriteByte(rsaSignatureType)

	// Tag 1: TAG_DOMAIN = ENERGY_DEVICE (7)
	buf.WriteByte(tagDomain)
	buf.WriteByte(tagLenOne)
	buf.WriteByte(domainEnergyDev)

	// Tag 2: TAG_PERSONALIZATION = din
	buf.WriteByte(tagPersonalization)
	//nolint:gosec // DIN string length is bounded by gateway protocol (< 32 chars).
	buf.WriteByte(uint8(len(din)))
	buf.WriteString(din)

	// Tag 4: TAG_EXPIRES_AT = 4-byte big-endian uint32
	buf.WriteByte(tagExpiresAt)
	buf.WriteByte(tagLenFour)
	var expBytes [tagLenFour]byte
	binary.BigEndian.PutUint32(expBytes[:], expiresAt)
	buf.Write(expBytes[:])

	// Tag 255: TAG_END (0xFF)
	buf.WriteByte(tagEnd)

	// Append inner payload
	buf.Write(innerBytes)

	return buf.Bytes()
}

// Sign signs the TLV payload using RSA PKCS#1 v1.5 with SHA-512.
func (v *TEDAPIv1r) Sign(tlvPayload []byte) ([]byte, error) {
	hashed := sha512.Sum512(tlvPayload)

	return rsa.SignPKCS1v15(rand.Reader, v.privateKey, crypto.SHA512, hashed[:])
}

// PostV1r wraps envelopeBytes in an RSA-signed RoutableMessage and POSTs to /tedapi/v1r.
func (v *TEDAPIv1r) PostV1r(ctx context.Context, envelopeBytes []byte, din string) ([]byte, error) {
	routable := &combined.RoutableMessage{
		ToDestination: &combined.Destination{
			SubDestination: &combined.Destination_Domain{
				Domain: combined.Domain_DOMAIN_ENERGY_DEVICE,
			},
		},
		Payload: &combined.RoutableMessage_ProtobufMessageAsBytes{
			ProtobufMessageAsBytes: envelopeBytes,
		},
		Uuid: []byte(strconv.FormatInt(time.Now().UnixNano(), 10)),
	}

	//nolint:gosec // Unix timestamp is within 32-bit uint expiration range.
	expiresAt := uint32(time.Now().Unix()) + expirationOffset
	tlvPayload := v.BuildTLVPayload(din, expiresAt, envelopeBytes)
	sig, err := v.Sign(tlvPayload)
	if err != nil {
		return nil, fmt.Errorf("v1r sign error: %w", err)
	}

	routable.SignatureData = &combined.SignatureData{
		SignerIdentity: &combined.KeyIdentity{
			IdentityType: &combined.KeyIdentity_PublicKey{
				PublicKey: v.publicKeyDER,
			},
		},
		SigType: &combined.SignatureData_RsaData{
			RsaData: &combined.RsaSignatureData{
				ExpiresAt: expiresAt,
				Signature: sig,
			},
		},
	}

	wireBytes, err := proto.Marshal(routable)
	if err != nil {
		return nil, fmt.Errorf("v1r marshal error: %w", err)
	}

	url := fmt.Sprintf("https://%s/tedapi/v1r", v.host)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(wireBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("v1r post error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		logger.Load(ctx).WarnContext(ctx, "v1r auth error, attempting re-login", "status", resp.StatusCode)
		if loginErr := v.Login(ctx); loginErr == nil {
			// Retry once
			req2, _ := http.NewRequestWithContext(
				ctx,
				http.MethodPost,
				url,
				bytes.NewReader(wireBytes),
			)
			req2.Header.Set("Content-Type", "application/octet-stream")
			resp2, err2 := v.client.Do(req2)
			if err2 == nil {
				defer resp2.Body.Close()
				resp = resp2
			}
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: POST failed with HTTP %d", backend.ErrUnexpectedStatus, resp.StatusCode)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var respMsg combined.RoutableMessage
	if unmarshalErr := proto.Unmarshal(respBytes, &respMsg); unmarshalErr != nil {
		return nil, fmt.Errorf("v1r unmarshal response error: %w", unmarshalErr)
	}

	if status := respMsg.GetSignedMessageStatus(); status != nil &&
		status.GetMessageFault() != combined.MessageFault_E_MESSAGEFAULT_ERROR_NONE {
		fault := status.GetMessageFault()
		if fault == combined.MessageFault_E_MESSAGEFAULT_ERROR_UNKNOWN_KEY_ID {
			return nil, fmt.Errorf("%w: key fingerprint %s", backend.ErrUnknownKeyID, v.KeyFingerprint)
		}

		return nil, fmt.Errorf("%w: %s (%d)", backend.ErrMessageFault, fault.String(), fault)
	}

	return respMsg.GetProtobufMessageAsBytes(), nil
}

// SendTEGMessage sends a TEGMessages command via v1r and parses the response envelope.
func (v *TEDAPIv1r) SendTEGMessage(
	ctx context.Context,
	din string,
	teg *combined.TEGMessages,
) (*combined.MessageEnvelope, error) {
	msg := &combined.MessageEnvelope{
		DeliveryChannel: combined.DeliveryChannel_DELIVERY_CHANNEL_HERMES_COMMAND,
		Sender: &combined.Participant{
			Id: &combined.Participant_AuthorizedClient{
				AuthorizedClient: 1, // CUSTOMER_MOBILE_APP
			},
		},
		Recipient: &combined.Participant{
			Id: &combined.Participant_Din{
				Din: din,
			},
		},
		Payload: &combined.MessageEnvelope_Teg{
			Teg: teg,
		},
	}

	envelopeBytes, err := proto.Marshal(msg)
	if err != nil {
		return nil, err
	}

	inner, err := v.PostV1r(ctx, envelopeBytes, din)
	if err != nil {
		return nil, err
	}

	var respEnv combined.MessageEnvelope
	if unmarshalErr := proto.Unmarshal(inner, &respEnv); unmarshalErr != nil {
		return nil, fmt.Errorf("send_teg_message unmarshal error: %w", unmarshalErr)
	}

	return &respEnv, nil
}

// ScheduleMaxBackup schedules a manual backup event (storm watch / max backup) via v1r TEGMessages.
func (v *TEDAPIv1r) ScheduleMaxBackup(ctx context.Context, durationSeconds int) (bool, error) {
	din, err := v.GetDin(ctx)
	if err != nil {
		return false, err
	}
	if durationSeconds < minBackupSeconds {
		durationSeconds = minBackupSeconds
	}

	// Gateway requires canceling existing backup event before scheduling new
	_, _ = v.CancelMaxBackup(ctx)

	now := time.Now().Unix()
	teg := &combined.TEGMessages{
		Message: &combined.TEGMessages_ScheduleManualBackupEventRequest{
			ScheduleManualBackupEventRequest: &combined.TEGAPIScheduleManualBackupEventRequest{
				SchedulingInfo: &combined.ControlEventSchedulingInfo{
					StartTime: &timestamppb.Timestamp{
						Seconds: now,
					},
					DurationSeconds: uint32(durationSeconds),
					Priority:        math.MaxUint64,
				},
			},
		},
	}

	resp, err := v.SendTEGMessage(ctx, din, teg)
	if err != nil {
		return false, err
	}

	if resp.GetTeg() != nil && resp.GetTeg().GetScheduleManualBackupEventResponse() != nil {
		logger.Load(ctx).DebugContext(ctx, "max backup scheduled", "duration_seconds", durationSeconds)

		return true, nil
	}

	return false, fmt.Errorf("%w: schedule_max_backup", backend.ErrUnexpectedResponse)
}

// CancelMaxBackup cancels the active manual backup event via v1r TEGMessages.
func (v *TEDAPIv1r) CancelMaxBackup(ctx context.Context) (bool, error) {
	din, err := v.GetDin(ctx)
	if err != nil {
		return false, err
	}

	teg := &combined.TEGMessages{
		Message: &combined.TEGMessages_CancelManualBackupEventRequest{
			CancelManualBackupEventRequest: &combined.TEGAPICancelManualBackupEventRequest{},
		},
	}

	resp, err := v.SendTEGMessage(ctx, din, teg)
	if err != nil {
		return false, err
	}

	if resp.GetTeg() != nil && resp.GetTeg().GetCancelManualBackupEventResponse() != nil {
		logger.Load(ctx).DebugContext(ctx, "max backup cancelled")

		return true, nil
	}

	return false, fmt.Errorf("%w: cancel_max_backup", backend.ErrUnexpectedResponse)
}

// GetBackupEvents retrieves active backup events via v1r TEGMessages.
func (v *TEDAPIv1r) GetBackupEvents(ctx context.Context) (map[string]any, error) {
	din, err := v.GetDin(ctx)
	if err != nil {
		return nil, err
	}

	teg := &combined.TEGMessages{
		Message: &combined.TEGMessages_GetBackupEventsRequest{
			GetBackupEventsRequest: &combined.TEGAPIGetBackupEventsRequest{},
		},
	}

	resp, err := v.SendTEGMessage(ctx, din, teg)
	if err != nil {
		return nil, err
	}

	res := resp.GetTeg().GetGetBackupEventsResponse()
	if res == nil {
		return nil, fmt.Errorf("%w: get_backup_events", backend.ErrEmptyResponse)
	}

	backupEvents := make([]any, 0, len(res.GetBackupEvents()))
	for _, b := range res.GetBackupEvents() {
		bMap := map[string]any{
			"id":   b.GetId(),
			"name": b.GetName(),
		}
		if info := b.GetSchedulingInfo(); info != nil {
			bMap["duration_seconds"] = info.GetDurationSeconds()
			bMap["priority"] = info.GetPriority()
		}
		backupEvents = append(backupEvents, bMap)
	}

	out := map[string]any{
		"backup_events": backupEvents,
	}
	if m := res.GetManualBackupEvent(); m != nil && m.GetSchedulingInfo() != nil {
		out["manual_backup_event"] = map[string]any{
			"duration_seconds": m.GetSchedulingInfo().GetDurationSeconds(),
			"priority":         m.GetSchedulingInfo().GetPriority(),
		}
	}

	return out, nil
}

// APIGet makes an authenticated GET request with the Bearer token to a standard gateway endpoint.
func (v *TEDAPIv1r) APIGet(ctx context.Context, path string) (any, error) {
	url := fmt.Sprintf("https://%s%s", v.host, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	if v.token != "" {
		req.Header.Set("Authorization", "Bearer "+v.token)
	}
	v.mu.Unlock()

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: API GET %s HTTP %d", backend.ErrUnexpectedStatus, path, resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsed any
	if unmarshalErr := json.Unmarshal(b, &parsed); unmarshalErr == nil {
		return parsed, nil
	}

	return string(b), nil
}
