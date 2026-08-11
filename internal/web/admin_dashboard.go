package web

import (
	"net/http"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	data := admin.ViewData{CSRFToken: csrfFromRequest(r)}
	if accounts, err := s.accounts.List(r.Context()); err == nil {
		data.AccountCount = len(accounts)
		for _, account := range accounts {
			switch account.Status {
			case "active":
				data.ActiveCount++
			case "disabled":
				data.DisabledCount++
			case "expired":
				data.ExpiredCount++
			}
		}
	}
	if invites, err := s.invites.List(r.Context()); err == nil {
		data.InviteCount = len(invites)
	}
	if orders, err := s.payments.ListOrders(r.Context(), domain.PaymentOrderFilter{Page: 1, PageSize: 1}); err == nil {
		data.PaymentOrderCount = orders.Total
		data.PaymentPaidCount = orders.PaidCount
		data.PaymentRevenueFen = orders.PaidAmountFen
	}
	s.templates.Render(w, "dashboard", data)
}
