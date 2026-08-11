package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Rst307/emby-service-portal/internal/accounts"
	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

func (s *Server) accountList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.renderAccounts(w, r, http.StatusOK, "", r.URL.Query().Get("message"))
}
func (s *Server) accountCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	expiresAt, err := s.parseDateTime(r, r.Form.Get("expires_at"))
	if err == nil {
		_, err = s.accounts.Create(r.Context(), accounts.CreateInput{Username: r.Form.Get("username"), Password: r.Form.Get("password"), ExpiresAt: expiresAt, Note: r.Form.Get("note")})
	}
	if err != nil {
		s.renderAccounts(w, r, http.StatusBadRequest, accountError(err), "")
		return
	}
	http.Redirect(w, r, "/admin/accounts", http.StatusSeeOther)
}
func (s *Server) accountSync(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	expiresAt, err := s.parseDateTime(r, r.Form.Get("expires_at"))
	if err == nil {
		_, err = s.accounts.SyncFromEmby(r.Context(), accounts.SyncInput{ExpiresAt: expiresAt, Note: r.Form.Get("note")})
	}
	if err != nil {
		s.renderAccounts(w, r, http.StatusBadRequest, "同步失败：请检查 Emby 连接和到期时间", "")
		return
	}
	http.Redirect(w, r, "/admin/accounts", http.StatusSeeOther)
}
func (s *Server) accountUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	expiresAt, parseErr := s.parseAccountDateTime(r, r.Form.Get("expires_at"), r.Form.Get("expires_at_original"))
	if err == nil && id > 0 && parseErr == nil {
		version, versionErr := accountVersion(r)
		if versionErr != nil {
			err = versionErr
		} else {
			_, err = s.accounts.Update(r.Context(), id, accounts.UpdateInput{ExpiresAt: expiresAt, Note: r.Form.Get("note"), Version: version})
		}
	} else if err == nil {
		err = parseErr
	}
	if err != nil {
		s.renderAccounts(w, r, http.StatusBadRequest, accountError(err), "")
		return
	}
	http.Redirect(w, r, "/admin/accounts", http.StatusSeeOther)
}
func (s *Server) accountBatch(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	ids, versions, err := accountSelections(r.Form["account_id"])
	input := accounts.BatchInput{AccountIDs: ids, Versions: versions, Action: accounts.BatchAction(r.Form.Get("action"))}
	if err == nil {
		switch input.Action {
		case accounts.BatchSetExpiry:
			input.ExpiresAt, err = s.parseDateTime(r, r.Form.Get("expires_at"))
		case accounts.BatchExtend, accounts.BatchReduce:
			input.Duration, err = batchDuration(r.Form.Get("duration"), r.Form.Get("duration_unit"))
		}
	}
	completed := 0
	if err == nil {
		completed, err = s.accounts.Batch(r.Context(), input)
	}
	if err != nil {
		message := "批量操作失败，请检查所选账号和输入后重试"
		if completed > 0 {
			message = fmt.Sprintf("批量操作中断，已完成 %d 个账号；请刷新后检查其余账号", completed)
		}
		s.renderAccounts(w, r, http.StatusBadRequest, message, "")
		return
	}
	http.Redirect(w, r, "/admin/accounts?message="+url.QueryEscape(fmt.Sprintf("已完成 %d 个账号的批量操作", completed)), http.StatusSeeOther)
}

func accountSelections(values []string) ([]int64, map[int64]int64, error) {
	ids := make([]int64, 0, len(values))
	versions := make(map[int64]int64, len(values))
	for _, value := range values {
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			return nil, nil, errors.New("invalid account selection")
		}
		id, idErr := strconv.ParseInt(parts[0], 10, 64)
		version, versionErr := strconv.ParseInt(parts[1], 10, 64)
		if idErr != nil || versionErr != nil || id < 1 || version < 1 {
			return nil, nil, errors.New("invalid account selection")
		}
		if _, duplicate := versions[id]; duplicate {
			continue
		}
		ids = append(ids, id)
		versions[id] = version
	}
	return ids, versions, nil
}

func batchDuration(raw, unit string) (time.Duration, error) {
	amount, err := strconv.Atoi(raw)
	if err != nil || amount < 1 || amount > 36500 {
		return 0, errors.New("invalid batch duration")
	}
	multipliers := map[string]time.Duration{"minute": time.Minute, "hour": time.Hour, "day": 24 * time.Hour}
	multiplier, ok := multipliers[unit]
	if !ok {
		return 0, errors.New("invalid batch duration unit")
	}
	return time.Duration(amount) * multiplier, nil
}

func (s *Server) accountEnable(w http.ResponseWriter, r *http.Request) {
	s.accountAction(w, r, func(id, version int64) error { return s.accounts.Enable(r.Context(), id, version) })
}
func (s *Server) accountDisable(w http.ResponseWriter, r *http.Request) {
	s.accountAction(w, r, func(id, version int64) error { return s.accounts.Disable(r.Context(), id, version) })
}
func (s *Server) accountDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := accountID(r)
	if err != nil {
		s.renderAccounts(w, r, http.StatusBadRequest, accountError(err), "")
		return
	}
	account, err := s.accounts.Get(r.Context(), id)
	if err != nil {
		s.renderAccounts(w, r, http.StatusBadRequest, accountError(err), "")
		return
	}
	s.templates.Render(w, "account-delete-confirm", admin.ViewData{CSRFToken: csrfFromRequest(r), Account: account})
}

func (s *Server) accountDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	s.accountAction(w, r, func(id, _ int64) error { return s.accounts.Delete(r.Context(), id) })
}
func (s *Server) accountAction(w http.ResponseWriter, r *http.Request, action func(int64, int64) error) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	version, versionErr := accountVersion(r)
	if err == nil && versionErr != nil {
		err = versionErr
	}
	if err == nil && id > 0 {
		err = action(id, version)
	}
	if err != nil {
		s.renderAccounts(w, r, http.StatusBadRequest, accountError(err), "")
		return
	}
	http.Redirect(w, r, "/admin/accounts", http.StatusSeeOther)
}

func accountVersion(r *http.Request) (int64, error) {
	raw := r.Form.Get("version")
	if raw == "" {
		return 0, nil
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version < 1 {
		return 0, errors.New("invalid account version")
	}
	return version, nil
}
func (s *Server) renderAccounts(w http.ResponseWriter, r *http.Request, status int, errorMessage, message string) {
	list, err := s.accounts.List(r.Context())
	if err != nil {
		http.Error(w, "load accounts", http.StatusInternalServerError)
		return
	}
	s.templates.RenderStatus(w, "accounts", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: errorMessage, Message: message, Accounts: list}, status)
}
func accountError(err error) string {
	switch {
	case errors.Is(err, accounts.ErrNotFound):
		return "账号不存在"
	case errors.Is(err, accounts.ErrInvalidUsername):
		return "请输入用户名"
	case errors.Is(err, accounts.ErrInvalidPassword):
		return "密码至少需要 8 个字符"
	case errors.Is(err, accounts.ErrExpiredAccount):
		return "已到期账号不能启用；请先续费"
	case errors.Is(err, accounts.ErrConflict):
		return "账号已被其他操作更新，请刷新后重试"
	default:
		return "操作失败，请检查 Emby 连接及输入后重试"
	}
}
