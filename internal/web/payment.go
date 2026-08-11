package web

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Rst307/emby-service-portal/internal/paymentcenter"
	"github.com/Rst307/emby-service-portal/internal/payments"
	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

func (s *Server) purchasePage(w http.ResponseWriter, r *http.Request) {
	plans, err := s.payments.ListPlans(r.Context(), payments.KindActivation, true)
	if err != nil {
		http.Error(w, "load activation plans", http.StatusInternalServerError)
		return
	}
	s.templates.Render(w, "purchase", admin.ViewData{CSRFToken: csrfFromRequest(r), ActivationPlans: plans})
}

func (s *Server) purchaseCreate(w http.ResponseWriter, r *http.Request) {
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if !s.allowAttempt(w, r, s.publicLimit, "activation-purchase") {
		return
	}
	planID, err := strconv.ParseInt(strings.TrimSpace(r.Form.Get("plan_id")), 10, 64)
	if err == nil && planID < 1 {
		err = errors.New("invalid plan")
	}
	buyerInfo := strings.TrimSpace(r.Form.Get("buyer_info"))
	if len(buyerInfo) > 200 {
		err = errors.New("购买人或联系方式不能超过 200 个字符")
	}
	if err == nil {
		order, createErr := s.payments.CreateActivationOrder(r.Context(), planID, buyerInfo)
		err = createErr
		if err == nil {
			http.Redirect(w, r, "/payment/"+order.PublicToken, http.StatusSeeOther)
			return
		}
	}
	plans, _ := s.payments.ListPlans(r.Context(), payments.KindActivation, true)
	s.templates.RenderStatus(w, "purchase", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: "创建支付订单失败：" + err.Error(), ActivationPlans: plans, BuyerInfo: buyerInfo}, http.StatusBadRequest)
}

func (s *Server) renewPaymentCreate(w http.ResponseWriter, r *http.Request) {
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	account, authenticated := s.portalAccount(r)
	username := r.Form.Get("username")
	if authenticated {
		username = account.Username
	}
	if !s.allowAttempt(w, r, s.publicLimit, username) {
		return
	}
	planID, err := strconv.ParseInt(strings.TrimSpace(r.Form.Get("plan_id")), 10, 64)
	if err == nil && planID < 1 {
		err = errors.New("invalid plan")
	}
	if err == nil {
		var token string
		if authenticated {
			order, createErr := s.payments.CreateRenewalOrderForAccount(r.Context(), planID, account.ID)
			err = createErr
			if err == nil {
				token = order.PublicToken
			}
		} else {
			order, createErr := s.payments.CreateRenewalOrder(r.Context(), planID, username, r.Form.Get("password"))
			err = createErr
			if err == nil {
				token = order.PublicToken
			}
		}
		if err == nil {
			http.Redirect(w, r, "/payment/"+token, http.StatusSeeOther)
			return
		}
	}
	s.renderRenewPage(w, r, http.StatusBadRequest, "创建续费订单失败："+err.Error())
}

func (s *Server) paymentPage(w http.ResponseWriter, r *http.Request) {
	order, err := s.payments.PublicOrder(r.Context(), r.PathValue("token"))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "load payment order", http.StatusInternalServerError)
		return
	}
	s.templates.Render(w, "payment", admin.ViewData{PaymentOrder: order})
}

func (s *Server) paymentStatus(w http.ResponseWriter, r *http.Request) {
	order, err := s.payments.PublicOrder(r.Context(), r.PathValue("token"))
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment order not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load payment order"})
		return
	}
	payload := map[string]any{
		"status":             order.PaymentStatus,
		"fulfillment_status": order.FulfillmentStatus,
		"payment_url":        order.PaymentURL,
		"amount_fen":         order.AmountFen,
		"currency":           order.Currency,
		"plan_name":          order.PlanName,
	}
	if order.FulfillmentStatus == "completed" && order.Kind == payments.KindActivation {
		payload["activation_code"] = order.ActivationCode
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read payment notification", http.StatusBadRequest)
		return
	}
	err = s.payments.HandleWebhook(r.Context(), r.Header, body)
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if errors.Is(err, paymentcenter.ErrInvalidNotification) {
		http.Error(w, "invalid payment notification", http.StatusBadRequest)
		return
	}
	if errors.Is(err, payments.ErrPaymentNotReady) || errors.Is(err, paymentcenter.ErrNotConfigured) {
		http.Error(w, "payment center is not configured", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "payment order not found", http.StatusNotFound)
		return
	}
	// A verified notification that cannot be fulfilled must be retried by the
	// payment center; do not acknowledge it as successful.
	http.Error(w, "payment fulfillment failed", http.StatusInternalServerError)
}
