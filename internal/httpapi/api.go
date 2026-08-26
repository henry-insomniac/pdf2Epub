package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pdf2epub/internal/abuse"
	"pdf2epub/internal/app"
	"pdf2epub/internal/auth"
	"pdf2epub/internal/commerce"
)

const sessionCookieName = "btc_session"

type API struct {
	sessions          *auth.Manager
	jobs              *app.Manager
	maxUpload         int64
	secureCookie      bool
	publicAccess      bool
	limiter           *requestLimiter
	subjectLimit      *requestLimiter
	commerce          *commerce.Service
	challenge         abuse.Verifier
	challengeIssuer   abuse.Issuer
	challengeProvider string
	challengeKey      string
	uploadTickets     *uploadTickets
	trustProxyHeaders bool
	mux               *http.ServeMux
}

type Options struct {
	PublicAccess      bool
	Commerce          *commerce.Service
	Challenge         abuse.Verifier
	ChallengeIssuer   abuse.Issuer
	ChallengeProvider string
	ChallengeSiteKey  string
	TrustProxyHeaders bool
}

type sessionResponse struct {
	Username          string         `json:"username"`
	Role              string         `json:"role"`
	Access            string         `json:"access"`
	CSRFToken         string         `json:"csrf_token"`
	ExpiresAt         time.Time      `json:"expires_at"`
	Credits           int64          `json:"credits"`
	BillingEnabled    bool           `json:"billing_enabled"`
	CheckoutEnabled   bool           `json:"checkout_enabled"`
	VoucherEnabled    bool           `json:"voucher_enabled"`
	Pack              *commerce.Pack `json:"pack,omitempty"`
	ChallengeProvider string         `json:"challenge_provider,omitempty"`
	ChallengeURL      string         `json:"challenge_url,omitempty"`
	ChallengeSiteKey  string         `json:"challenge_site_key,omitempty"`
	MaxUploadBytes    int64          `json:"max_upload_bytes"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func New(sessions *auth.Manager, jobs *app.Manager, maxUpload int64, secureCookie bool) *API {
	return NewWithOptions(sessions, jobs, maxUpload, secureCookie, Options{})
}

func NewWithOptions(sessions *auth.Manager, jobs *app.Manager, maxUpload int64, secureCookie bool, options Options) *API {
	if options.ChallengeProvider == "" && options.ChallengeSiteKey != "" {
		options.ChallengeProvider = "turnstile"
	}
	api := &API{
		sessions:          sessions,
		jobs:              jobs,
		maxUpload:         maxUpload,
		secureCookie:      secureCookie,
		publicAccess:      options.PublicAccess,
		limiter:           newRequestLimiter(2, 30),
		subjectLimit:      newRequestLimiter(1, 12),
		commerce:          options.Commerce,
		challenge:         options.Challenge,
		challengeIssuer:   options.ChallengeIssuer,
		challengeProvider: options.ChallengeProvider,
		challengeKey:      options.ChallengeSiteKey,
		uploadTickets:     newUploadTickets(2 * time.Minute),
		trustProxyHeaders: options.TrustProxyHeaders,
		mux:               http.NewServeMux(),
	}
	api.routes()
	return api
}

func (a *API) Handler() http.Handler {
	return a.securityHeaders(a.limitRequests(a.mux))
}

func (a *API) routes() {
	a.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	a.mux.HandleFunc("GET /api/v1/meta", func(w http.ResponseWriter, r *http.Request) {
		hasSession := false
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			_, hasSession = a.sessions.Validate(cookie.Value)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"public_access": a.publicAccess, "has_session": hasSession})
	})
	a.mux.HandleFunc("POST /api/v1/auth/login", a.handleLogin)
	a.mux.HandleFunc("POST /api/v1/auth/guest", a.handleGuest)
	a.mux.HandleFunc("POST /api/v1/auth/logout", a.withSession(a.handleLogout, true))
	a.mux.HandleFunc("GET /api/v1/session", a.withSession(a.handleSession, false))
	a.mux.HandleFunc("POST /api/v1/upload-tickets", a.withSession(a.handleUploadTicket, true))
	a.mux.HandleFunc("GET /api/v1/challenge", a.withSession(a.handleChallenge, false))
	a.mux.HandleFunc("POST /api/v1/billing/checkout", a.withSession(a.handleCheckout, true))
	a.mux.HandleFunc("POST /api/v1/billing/redeem", a.withSession(a.handleRedeem, true))
	a.mux.HandleFunc("POST /api/v1/billing/webhook", a.handleBillingWebhook)
	a.mux.HandleFunc("POST /api/v1/jobs", a.withSession(a.handleCreateJob, true))
	a.mux.HandleFunc("GET /api/v1/jobs/{id}", a.withSession(a.handleGetJob, false))
	a.mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", a.withSession(a.handleCancelJob, true))
	a.mux.HandleFunc("GET /api/v1/jobs/{id}/download", a.withSession(a.handleDownload, false))
	a.registerUI()
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	if a.publicAccess {
		writeError(w, http.StatusNotFound, "request.not_found", "请求的接口不存在。")
		return
	}
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "request.invalid_json", "登录信息格式不正确，请重新输入。")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeError(w, http.StatusBadRequest, "request.invalid_json", "登录信息格式不正确，请重新输入。")
		return
	}
	session, err := a.sessions.Login(request.Username, request.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "identity.invalid_credentials", "账号或密码不正确，请检查后重试。")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "identity.session_failed", "暂时无法建立登录会话，请稍后重试。")
		return
	}
	a.setSessionCookie(w, session)
	payload, err := a.sessionPayload(r.Context(), session)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "billing.balance_unavailable", "暂时无法读取额度，请稍后重试。")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleGuest(w http.ResponseWriter, r *http.Request) {
	if !a.publicAccess {
		writeError(w, http.StatusNotFound, "request.not_found", "请求的接口不存在。")
		return
	}
	if r.ContentLength > 0 {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128))
		decoder.DisallowUnknownFields()
		var request struct{}
		if err := decoder.Decode(&request); err != nil || ensureJSONEOF(decoder) != nil {
			writeError(w, http.StatusBadRequest, "request.invalid_json", "请求格式不正确。")
			return
		}
	}
	session, err := a.sessions.CreateGuest()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "identity.session_failed", "暂时无法建立访问会话，请稍后重试。")
		return
	}
	if a.commerce == nil || a.challenge == nil || (a.challengeProvider == "turnstile" && a.challengeKey == "") {
		writeError(w, http.StatusServiceUnavailable, "service.public_access_unavailable", "公开转换服务尚未完成安全配置。")
		return
	}
	if err := a.commerce.EnsureAccount(r.Context(), session.SubjectID); err != nil {
		writeError(w, http.StatusServiceUnavailable, "billing.account_unavailable", "暂时无法建立额度账户，请稍后重试。")
		return
	}
	a.setSessionCookie(w, session)
	payload, err := a.sessionPayload(r.Context(), session)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "billing.balance_unavailable", "暂时无法读取额度，请稍后重试。")
		return
	}
	writeJSON(w, http.StatusCreated, payload)
}

func (a *API) setSessionCookie(w http.ResponseWriter, session auth.Session) {
	sameSite := http.SameSiteStrictMode
	if a.publicAccess {
		sameSite = http.SameSiteLaxMode
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.Token,
		Path:     "/",
		Expires:  session.ExpiresAt,
		MaxAge:   int(time.Until(session.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   a.secureCookie,
		SameSite: sameSite,
	})
}

func (a *API) handleSession(w http.ResponseWriter, r *http.Request, session auth.Session) {
	payload, err := a.sessionPayload(r.Context(), session)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "billing.balance_unavailable", "暂时无法读取额度，请稍后重试。")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) sessionPayload(ctx context.Context, session auth.Session) (sessionResponse, error) {
	access := "private"
	if a.publicAccess {
		access = "public"
	}
	response := sessionResponse{
		Username:       session.Username,
		Role:           session.Role,
		Access:         access,
		CSRFToken:      session.CSRFToken,
		ExpiresAt:      session.ExpiresAt,
		MaxUploadBytes: a.maxUpload,
	}
	if a.publicAccess {
		if a.commerce == nil {
			return sessionResponse{}, errors.New("commerce service is not configured")
		}
		balance, err := a.commerce.Balance(ctx, session.SubjectID)
		if err != nil {
			return sessionResponse{}, err
		}
		pack := a.commerce.Pack()
		response.Credits = balance
		response.BillingEnabled = true
		response.CheckoutEnabled = a.commerce.CheckoutEnabled()
		response.VoucherEnabled = a.commerce.VouchersEnabled()
		response.Pack = &pack
		response.ChallengeProvider = a.challengeProvider
		if a.challengeProvider == "turnstile" {
			response.ChallengeSiteKey = a.challengeKey
		}
		if a.challengeIssuer != nil {
			response.ChallengeURL = "/api/v1/challenge"
		}
	}
	return response, nil
}

func (a *API) handleLogout(w http.ResponseWriter, _ *http.Request, session auth.Session) {
	a.sessions.Logout(session.Token)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.secureCookie,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleCreateJob(w http.ResponseWriter, r *http.Request, session auth.Session) {
	if a.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "service.conversion_unavailable", "转换服务尚未就绪，请稍后重试。")
		return
	}
	if a.publicAccess {
		if a.commerce == nil {
			writeError(w, http.StatusServiceUnavailable, "billing.unavailable", "额度服务暂时不可用，请稍后重试。")
			return
		}
		balance, err := a.commerce.Balance(r.Context(), session.SubjectID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "billing.balance_unavailable", "暂时无法读取额度，请稍后重试。")
			return
		}
		if balance < 1 {
			writeError(w, http.StatusPaymentRequired, "billing.insufficient_credits", "转换额度不足，请先购买额度。")
			return
		}
		if !a.uploadTickets.Consume(session.SubjectID, r.Header.Get("X-Upload-Ticket")) {
			writeError(w, http.StatusForbidden, "abuse.upload_ticket_invalid", "上传验证已失效，请重新完成人机验证。")
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.maxUpload+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "input.file_too_large", a.uploadTooLargeMessage())
			return
		}
		writeError(w, http.StatusBadRequest, "request.invalid_multipart", "无法读取上传文件，请重新选择 PDF。")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "input.file_required", "请选择一个 PDF 文件。")
		return
	}
	defer file.Close()
	if !hasPDFSignature(file) {
		writeError(w, http.StatusUnsupportedMediaType, "input.not_pdf", "所选文件不是有效的 PDF，请重新选择。")
		return
	}
	mode, err := app.ParseConversionMode(r.FormValue("mode"))
	if errors.Is(err, app.ErrInvalidMode) {
		writeError(w, http.StatusBadRequest, "input.invalid_conversion_mode", "转换模式无效，请重新选择。")
		return
	}
	snapshot, err := a.jobs.SubmitFor(session.SubjectID, header.Filename, mode, file)
	if errors.Is(err, app.ErrBusy) {
		writeError(w, http.StatusConflict, "job.queue_full", "当前转换队列已满，请稍后再试。")
		return
	}
	if errors.Is(err, commerce.ErrInsufficientCredits) {
		writeError(w, http.StatusPaymentRequired, "billing.insufficient_credits", "转换额度不足，请先购买额度。")
		return
	}
	if errors.Is(err, app.ErrUploadTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "input.file_too_large", a.uploadTooLargeMessage())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "job.create_failed", "暂时无法创建转换任务，请稍后重试。")
		return
	}
	writeJSON(w, http.StatusAccepted, snapshot)
}

func (a *API) uploadTooLargeMessage() string {
	return fmt.Sprintf("PDF 不能超过 %d MiB，请选择较小的文件。", a.maxUpload>>20)
}

func (a *API) handleUploadTicket(w http.ResponseWriter, r *http.Request, session auth.Session) {
	if !a.publicAccess || a.challenge == nil {
		writeError(w, http.StatusNotFound, "request.not_found", "请求的接口不存在。")
		return
	}
	var request struct {
		Token string `json:"turnstile_token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || ensureJSONEOF(decoder) != nil {
		writeError(w, http.StatusBadRequest, "request.invalid_json", "验证请求格式不正确。")
		return
	}
	if err := a.challenge.Verify(r.Context(), request.Token, clientIP(r, a.trustProxyHeaders)); err != nil {
		writeError(w, http.StatusForbidden, "abuse.challenge_rejected", "人机验证未通过，请重试。")
		return
	}
	ticket, err := a.uploadTickets.Issue(session.SubjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "abuse.ticket_failed", "暂时无法建立上传验证，请稍后重试。")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"upload_ticket": ticket, "expires_in": 120})
}

func (a *API) handleChallenge(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if !a.publicAccess || a.challengeIssuer == nil {
		writeError(w, http.StatusNotFound, "request.not_found", "请求的接口不存在。")
		return
	}
	challenge, err := a.challengeIssuer.Issue(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "abuse.challenge_unavailable", "暂时无法建立人机验证，请稍后重试。")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, challenge)
}

func (a *API) handleCheckout(w http.ResponseWriter, r *http.Request, session auth.Session) {
	if !a.publicAccess || a.commerce == nil || !a.commerce.CheckoutEnabled() || a.challenge == nil {
		writeError(w, http.StatusNotFound, "request.not_found", "请求的接口不存在。")
		return
	}
	var request struct {
		Token string `json:"turnstile_token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || ensureJSONEOF(decoder) != nil {
		writeError(w, http.StatusBadRequest, "request.invalid_json", "支付请求格式不正确。")
		return
	}
	if err := a.challenge.Verify(r.Context(), request.Token, clientIP(r, a.trustProxyHeaders)); err != nil {
		writeError(w, http.StatusForbidden, "abuse.challenge_rejected", "人机验证未通过，请重试。")
		return
	}
	checkout, err := a.commerce.CreateCheckout(r.Context(), session.SubjectID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "billing.checkout_unavailable", "暂时无法创建支付页面，请稍后重试。")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"checkout_url": checkout.URL})
}

func (a *API) handleRedeem(w http.ResponseWriter, r *http.Request, session auth.Session) {
	if !a.publicAccess || a.commerce == nil || !a.commerce.VouchersEnabled() || a.challenge == nil {
		writeError(w, http.StatusNotFound, "request.not_found", "请求的接口不存在。")
		return
	}
	var request struct {
		Code  string `json:"code"`
		Token string `json:"challenge_token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || ensureJSONEOF(decoder) != nil {
		writeError(w, http.StatusBadRequest, "request.invalid_json", "兑换请求格式不正确。")
		return
	}
	if err := a.challenge.Verify(r.Context(), request.Token, clientIP(r, a.trustProxyHeaders)); err != nil {
		writeError(w, http.StatusForbidden, "abuse.challenge_rejected", "人机验证未通过，请重试。")
		return
	}
	redemption, err := a.commerce.RedeemVoucher(r.Context(), session.SubjectID, request.Code)
	switch {
	case errors.Is(err, commerce.ErrInvalidVoucher):
		writeError(w, http.StatusBadRequest, "billing.voucher_invalid", "兑换码无效或已过期。")
	case errors.Is(err, commerce.ErrVoucherRedeemed):
		writeError(w, http.StatusConflict, "billing.voucher_redeemed", "该兑换码已经使用。")
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "billing.redeem_unavailable", "暂时无法兑换额度，请稍后重试。")
	default:
		csrfToken := session.CSRFToken
		if redemption.AccountID != session.SubjectID {
			recoveredSession, sessionErr := a.sessions.CreateGuestFor(redemption.AccountID)
			if sessionErr != nil {
				writeError(w, http.StatusInternalServerError, "identity.session_failed", "额度已找到，但暂时无法恢复访问会话。")
				return
			}
			a.setSessionCookie(w, recoveredSession)
			csrfToken = recoveredSession.CSRFToken
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"credits": redemption.Credits, "credits_added": redemption.CreditsAdded,
			"recovered": redemption.Recovered, "csrf_token": csrfToken,
		})
	}
}

func (a *API) handleBillingWebhook(w http.ResponseWriter, r *http.Request) {
	if !a.publicAccess || a.commerce == nil || !a.commerce.CheckoutEnabled() {
		writeError(w, http.StatusNotFound, "request.not_found", "请求的接口不存在。")
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "billing.webhook_invalid", "支付回调格式不正确。")
		return
	}
	if _, err := a.commerce.HandleWebhook(r.Context(), payload, r.Header.Get("Stripe-Signature")); err != nil {
		writeError(w, http.StatusBadRequest, "billing.webhook_rejected", "支付回调验证失败。")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleGetJob(w http.ResponseWriter, r *http.Request, session auth.Session) {
	if a.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "service.conversion_unavailable", "转换服务尚未就绪，请稍后重试。")
		return
	}
	snapshot, ok := a.jobs.GetFor(session.SubjectID, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job.not_found", "转换任务不存在或已过期。")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *API) handleCancelJob(w http.ResponseWriter, r *http.Request, session auth.Session) {
	if a.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "service.conversion_unavailable", "转换服务尚未就绪，请稍后重试。")
		return
	}
	err := a.jobs.CancelFor(session.SubjectID, r.PathValue("id"))
	switch {
	case errors.Is(err, app.ErrJobNotFound):
		writeError(w, http.StatusNotFound, "job.not_found", "转换任务不存在或已过期。")
	case errors.Is(err, app.ErrNotCancelable):
		writeError(w, http.StatusConflict, "job.not_cancelable", "该任务已经结束，无法取消。")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "job.cancel_failed", "暂时无法取消转换，请稍后重试。")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) handleDownload(w http.ResponseWriter, r *http.Request, session auth.Session) {
	if a.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "service.conversion_unavailable", "转换服务尚未就绪，请稍后重试。")
		return
	}
	download, err := a.jobs.DownloadFor(r.Context(), session.SubjectID, r.PathValue("id"))
	if errors.Is(err, app.ErrJobNotFound) {
		writeError(w, http.StatusNotFound, "job.not_found", "转换任务不存在或已过期。")
		return
	}
	if errors.Is(err, app.ErrArtifactUnavailable) {
		writeError(w, http.StatusConflict, "job.artifact_unavailable", "EPUB 尚不可下载，请等待转换成功。")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "job.download_unavailable", "暂时无法建立下载链接，请稍后重试。")
		return
	}
	if download.URL != "" {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, download.URL, http.StatusFound)
		return
	}
	file, err := os.Open(download.Artifact.Path)
	if err != nil {
		writeError(w, http.StatusGone, "job.artifact_expired", "EPUB 已被清理，请重新转换。")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "job.artifact_unreadable", "无法读取 EPUB，请重新转换。")
		return
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(download.Artifact.Name)})
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Type", "application/epub+zip")
	http.ServeContent(w, r, download.Artifact.Name, info.ModTime(), file)
}

type sessionHandler func(http.ResponseWriter, *http.Request, auth.Session)

func (a *API) withSession(next sessionHandler, requireCSRF bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "identity.authentication_required", "请先登录后再继续。")
			return
		}
		session, ok := a.sessions.Validate(cookie.Value)
		if !ok {
			writeError(w, http.StatusUnauthorized, "identity.session_expired", "登录会话已失效，请重新登录。")
			return
		}
		if !a.subjectLimit.Allow(session.SubjectID) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "request.rate_limited", "请求过于频繁，请稍后再试。")
			return
		}
		if requireCSRF && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.CSRFToken)) != 1 {
			writeError(w, http.StatusForbidden, "identity.csrf_invalid", "请求验证信息已失效，请刷新页面后重试。")
			return
		}
		session.Token = cookie.Value
		next(w, r, session)
	}
}

func (a *API) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' https://challenges.cloudflare.com https://cdn.jsdelivr.net; worker-src 'self' blob:; frame-src https://challenges.cloudflare.com; connect-src 'self' https://challenges.cloudflare.com; base-uri 'none'; frame-ancestors 'none'; form-action 'self' https://checkout.stripe.com")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request contains trailing JSON")
	}
	return nil
}

func hasPDFSignature(file multipart.File) bool {
	buffer := make([]byte, 5)
	n, err := io.ReadFull(file, buffer)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	return n == len(buffer) && strings.EqualFold(string(buffer), "%PDF-")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}
