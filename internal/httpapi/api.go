package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pdf2epub/internal/app"
	"pdf2epub/internal/auth"
	"pdf2epub/internal/domain"
)

const sessionCookieName = "btc_session"

type API struct {
	sessions     *auth.Manager
	jobs         *app.Manager
	maxUpload    int64
	secureCookie bool
	mux          *http.ServeMux
}

type sessionResponse struct {
	Username  string    `json:"username"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func New(sessions *auth.Manager, jobs *app.Manager, maxUpload int64, secureCookie bool) *API {
	api := &API{
		sessions:     sessions,
		jobs:         jobs,
		maxUpload:    maxUpload,
		secureCookie: secureCookie,
		mux:          http.NewServeMux(),
	}
	api.routes()
	return api
}

func (a *API) Handler() http.Handler {
	return a.securityHeaders(a.mux)
}

func (a *API) routes() {
	a.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	a.mux.HandleFunc("POST /api/v1/auth/login", a.handleLogin)
	a.mux.HandleFunc("POST /api/v1/auth/logout", a.withSession(a.handleLogout, true))
	a.mux.HandleFunc("GET /api/v1/session", a.withSession(a.handleSession, false))
	a.mux.HandleFunc("POST /api/v1/jobs", a.withSession(a.handleCreateJob, true))
	a.mux.HandleFunc("GET /api/v1/jobs/{id}", a.withSession(a.handleGetJob, false))
	a.mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", a.withSession(a.handleCancelJob, true))
	a.mux.HandleFunc("GET /api/v1/jobs/{id}/download", a.withSession(a.handleDownload, false))
	a.registerUI()
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
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
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.Token,
		Path:     "/",
		Expires:  session.ExpiresAt,
		MaxAge:   int(time.Until(session.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   a.secureCookie,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, sessionResponse{
		Username:  request.Username,
		CSRFToken: session.CSRFToken,
		ExpiresAt: session.ExpiresAt,
	})
}

func (a *API) handleSession(w http.ResponseWriter, _ *http.Request, session auth.Session) {
	writeJSON(w, http.StatusOK, sessionResponse{
		Username:  session.Username,
		CSRFToken: session.CSRFToken,
		ExpiresAt: session.ExpiresAt,
	})
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

func (a *API) handleCreateJob(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if a.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "service.conversion_unavailable", "转换服务尚未就绪，请稍后重试。")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.maxUpload+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "input.file_too_large", "PDF 不能超过 100 MiB，请选择较小的文件。")
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
	snapshot, err := a.jobs.Submit(header.Filename, file)
	if errors.Is(err, app.ErrBusy) {
		writeError(w, http.StatusConflict, "job.active_exists", "已有 PDF 正在转换，请等待当前任务结束或先取消它。")
		return
	}
	if errors.Is(err, app.ErrUploadTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "input.file_too_large", "PDF 不能超过 100 MiB，请选择较小的文件。")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "job.create_failed", "暂时无法创建转换任务，请稍后重试。")
		return
	}
	writeJSON(w, http.StatusAccepted, snapshot)
}

func (a *API) handleGetJob(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if a.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "service.conversion_unavailable", "转换服务尚未就绪，请稍后重试。")
		return
	}
	snapshot, ok := a.jobs.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job.not_found", "转换任务不存在或已过期。")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *API) handleCancelJob(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if a.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "service.conversion_unavailable", "转换服务尚未就绪，请稍后重试。")
		return
	}
	err := a.jobs.Cancel(r.PathValue("id"))
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

func (a *API) handleDownload(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if a.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "service.conversion_unavailable", "转换服务尚未就绪，请稍后重试。")
		return
	}
	snapshot, ok := a.jobs.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job.not_found", "转换任务不存在或已过期。")
		return
	}
	if snapshot.Status != domain.JobSucceeded || snapshot.Artifact == nil {
		writeError(w, http.StatusConflict, "job.artifact_unavailable", "EPUB 尚不可下载，请等待转换成功。")
		return
	}
	file, err := os.Open(snapshot.Artifact.Path)
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
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(snapshot.Artifact.Name)})
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Type", "application/epub+zip")
	http.ServeContent(w, r, snapshot.Artifact.Name, info.ModTime(), file)
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
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
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
