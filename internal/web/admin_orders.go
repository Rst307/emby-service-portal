package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Rst307/emby-service-portal/internal/payments"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

func (s *Server) orderList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	page, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))
	if err != nil || page < 1 {
		page = 1
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	status := normalizePaymentOrderStatus(r.URL.Query().Get("status"))
	kind := normalizePaymentOrderKind(r.URL.Query().Get("kind"))
	result, err := s.payments.ListOrders(r.Context(), sqlite.PaymentOrderFilter{Query: query, Status: status, Kind: kind, Page: page, PageSize: 20})
	if err != nil {
		http.Error(w, "load payment orders", http.StatusInternalServerError)
		return
	}
	filterValues := url.Values{}
	if query != "" {
		filterValues.Set("q", query)
	}
	if status != "" {
		filterValues.Set("status", status)
	}
	if kind != "" {
		filterValues.Set("kind", kind)
	}
	data := admin.ViewData{
		CSRFToken: csrfFromRequest(r), Message: r.URL.Query().Get("message"),
		PaymentOrders: result.Orders, OrderTotal: result.Total, OrderPaidCount: result.PaidCount, OrderPaidFen: result.PaidAmountFen,
		OrderPage: result.Page, OrderPageSize: result.PageSize, OrderTotalPages: result.TotalPages,
		OrderQuery: query, OrderFilterQuery: filterValues.Encode(), OrderStatus: status, OrderKind: kind,
	}
	s.templates.Render(w, "orders", data)
}

func normalizePaymentOrderStatus(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "pending", "paid", "expired", "canceled", "failed":
		return value
	default:
		return ""
	}
}

func normalizePaymentOrderKind(value string) string {
	value = strings.TrimSpace(value)
	if value == payments.KindActivation || value == payments.KindRenewal {
		return value
	}
	return ""
}
