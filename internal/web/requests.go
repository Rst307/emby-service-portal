package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

/* ----------------------------- 用户端：求剧页 ----------------------------- */

// portalRequestPage renders the standalone 求剧 page. When a search query is
// present it annotates TMDB results with the live Emby library status and the
// account's previous requests.
func (s *Server) portalRequestPage(w http.ResponseWriter, r *http.Request) {
	account, ok := s.portalAccount(r)
	if !ok {
		http.Redirect(w, r, "/portal/login", http.StatusSeeOther)
		return
	}
	data := admin.ViewData{CSRFToken: csrfFromRequest(r), Account: account, PortalActive: "request"}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query != "" {
		data.RequestQuery = query
		items, err := s.requests.Search(r.Context(), account.ID, query, 12)
		if err != nil {
			data.Error = "搜索失败：" + err.Error()
		} else {
			data.SearchResults = items
			if len(items) == 0 {
				data.Message = "没有找到匹配的影视，试试中英文标题或换个关键词。"
			}
		}
	}
	if s.tmdb != nil && !s.tmdb.Configured() {
		data.TmdbConfigured = false
	} else {
		data.TmdbConfigured = true
	}
	s.templates.Render(w, "portal-request", data)
}

// portalRequestCreate records a 求剧 submission. Only the TMDB media type and
// ID are accepted from the form; the server re-fetches the catalog record from
// TMDB and re-checks the Emby library before saving.
func (s *Server) portalRequestCreate(w http.ResponseWriter, r *http.Request) {
	account, ok := s.portalAccount(r)
	if !ok {
		http.Redirect(w, r, "/portal/login", http.StatusSeeOther)
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if !s.allowAttempt(w, r, s.requestLimit, account.Username) {
		return
	}
	mediaType := strings.TrimSpace(r.Form.Get("media_type"))
	tmdbID, err := strconv.ParseInt(strings.TrimSpace(r.Form.Get("tmdb_id")), 10, 64)
	if err != nil || tmdbID < 1 {
		http.Redirect(w, r, "/portal/request?q="+url.QueryEscape(r.Form.Get("q"))+"&error="+url.QueryEscape("非法的影视条目"), http.StatusSeeOther)
		return
	}
	_, err = s.requests.Create(r.Context(), account, mediaType, tmdbID)
	if err != nil {
		message := "求剧失败：" + err.Error()
		if errors.Is(err, domain.ErrRequestInLibrary) {
			message = "该影视已在 Emby 库存中，无需重复求剧。"
		}
		http.Redirect(w, r, "/portal/request?q="+url.QueryEscape(r.Form.Get("q"))+"&error="+url.QueryEscape(message), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/request?q="+url.QueryEscape(r.Form.Get("q"))+"&message="+url.QueryEscape("求剧成功，管理员会尽快处理。"), http.StatusSeeOther)
}

/* ----------------------------- 管理端：求剧列表 ----------------------------- */

func (s *Server) adminRequestList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	page, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))
	if err != nil || page < 1 {
		page = 1
	}
	status := normalizeRequestStatus(r.URL.Query().Get("status"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	result, err := s.requests.List(r.Context(), domain.MediaRequestFilter{Query: query, Status: status, Page: page, PageSize: 20})
	if err != nil {
		http.Error(w, "load media requests", http.StatusInternalServerError)
		return
	}
	filterValues := url.Values{}
	if query != "" {
		filterValues.Set("q", query)
	}
	if status != "" {
		filterValues.Set("status", status)
	}
	data := admin.ViewData{
		CSRFToken: csrfFromRequest(r), Message: r.URL.Query().Get("message"), Error: r.URL.Query().Get("error"),
		MediaRequests: result.Requests, RequestTotal: result.Total, RequestPending: result.Pending, RequestFulfilled: result.Fulfilled,
		RequestPage: result.Page, RequestPageSize: result.PageSize, RequestTotalPages: result.TotalPages,
		RequestQuery: query, RequestFilterQuery: filterValues.Encode(), RequestStatus: status,
		RequestEnabled: s.tmdb != nil && s.tmdb.Configured(),
	}
	s.templates.Render(w, "requests", data)
}

// adminRequestSetStatus marks a request fulfilled or rejected.
func (s *Server) adminRequestSetStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireAdmin(w, r) {
			return
		}
		if !validCSRF(r) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		id, err := requestID(r)
		if err != nil {
			http.Error(w, "invalid request ID", http.StatusBadRequest)
			return
		}
		if err := s.requests.SetStatus(r.Context(), id, status); err != nil {
			if errors.Is(err, domain.ErrRequestNotFound) {
				http.Redirect(w, r, "/admin/requests?error="+url.QueryEscape("求剧记录不存在或已删除"), http.StatusSeeOther)
				return
			}
			http.Error(w, "update media request", http.StatusInternalServerError)
			return
		}
		label := "已标记为已入库"
		if status == domain.MediaRequestRejected {
			label = "已标记为驳回"
		}
		http.Redirect(w, r, "/admin/requests?message="+url.QueryEscape("求剧记录"+label), http.StatusSeeOther)
	}
}

func (s *Server) adminRequestDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := requestID(r)
	if err != nil {
		http.Error(w, "invalid request ID", http.StatusBadRequest)
		return
	}
	if err := s.requests.Delete(r.Context(), id); err != nil {
		if errors.Is(err, domain.ErrRequestNotFound) {
			http.Redirect(w, r, "/admin/requests?error="+url.QueryEscape("求剧记录不存在或已删除"), http.StatusSeeOther)
			return
		}
		http.Error(w, "delete media request", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/requests?message="+url.QueryEscape("求剧记录已删除"), http.StatusSeeOther)
}

func requestID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid request ID")
	}
	return id, nil
}

func normalizeRequestStatus(value string) string {
	switch strings.TrimSpace(value) {
	case domain.MediaRequestPending, domain.MediaRequestFulfilled, domain.MediaRequestRejected:
		return strings.TrimSpace(value)
	default:
		return ""
	}
}
