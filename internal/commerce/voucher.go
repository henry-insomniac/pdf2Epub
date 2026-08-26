package commerce

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidVoucher  = errors.New("invalid voucher")
	ErrVoucherRedeemed = errors.New("voucher already redeemed")
)

type voucherClaims struct {
	ID        string `json:"id"`
	Credits   int64  `json:"credits"`
	ExpiresAt int64  `json:"exp"`
}

type VoucherCodec struct {
	secret []byte
	now    func() time.Time
}

func NewVoucherCodec(secret []byte) (*VoucherCodec, error) {
	if len(secret) < 32 {
		return nil, errors.New("voucher secret must contain at least 32 bytes")
	}
	return &VoucherCodec{
		secret: append([]byte(nil), secret...),
		now:    func() time.Time { return time.Now().UTC() },
	}, nil
}

func (c *VoucherCodec) Generate(credits int64, ttl time.Duration) (string, error) {
	if credits <= 0 || credits > 1000 {
		return "", errors.New("voucher credits must be between 1 and 1000")
	}
	if ttl <= 0 {
		return "", errors.New("voucher lifetime must be positive")
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	claims := voucherClaims{
		ID:        hex.EncodeToString(random),
		Credits:   credits,
		ExpiresAt: c.now().Add(ttl).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return "YF1." + encoded + "." + c.sign(encoded), nil
}

func (c *VoucherCodec) parse(code string) (voucherClaims, error) {
	parts := strings.Split(strings.TrimSpace(code), ".")
	if len(parts) != 3 || parts[0] != "YF1" {
		return voucherClaims{}, ErrInvalidVoucher
	}
	expected := c.sign(parts[1])
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(expected)) != 1 {
		return voucherClaims{}, ErrInvalidVoucher
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return voucherClaims{}, ErrInvalidVoucher
	}
	var claims voucherClaims
	if err := json.Unmarshal(payload, &claims); err != nil || claims.ID == "" || claims.Credits <= 0 || claims.Credits > 1000 {
		return voucherClaims{}, ErrInvalidVoucher
	}
	if !c.now().Before(time.Unix(claims.ExpiresAt, 0)) {
		return voucherClaims{}, ErrInvalidVoucher
	}
	return claims, nil
}

func (c *VoucherCodec) sign(payload string) string {
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte("pdf2epub-voucher:" + payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
