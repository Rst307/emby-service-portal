package web

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/payments"
	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

func (s *Server) planList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.renderPlans(w, r, http.StatusOK, "", r.URL.Query().Get("message"))
}
func (s *Server) renderPlans(w http.ResponseWriter, r *http.Request, status int, errorMessage, message string) {
	activation, activationErr := s.payments.ListPlans(r.Context(), payments.KindActivation, false)
	renewal, renewalErr := s.payments.ListPlans(r.Context(), payments.KindRenewal, false)
	if activationErr != nil || renewalErr != nil {
		http.Error(w, "load payment plans", http.StatusInternalServerError)
		return
	}
	s.templates.RenderStatus(w, "plans", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: errorMessage, Message: message, ActivationPlans: activation, RenewalPlans: renewal}, status)
}

func (s *Server) planCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	price, err := parsePriceFen(r.Form.Get("price"))
	days, daysErr := strconv.Atoi(strings.TrimSpace(r.Form.Get("duration_days")))
	if err == nil {
		err = daysErr
	}
	if err == nil {
		_, err = s.payments.CreatePlan(r.Context(), domain.CreatePaymentPlanInput{Kind: r.Form.Get("kind"), Name: r.Form.Get("name"), DurationDays: days, PriceFen: price, Note: r.Form.Get("note"), SortOrder: 0})
	}
	if err != nil {
		s.renderPlans(w, r, http.StatusBadRequest, "创建套餐失败："+err.Error(), "")
		return
	}
	http.Redirect(w, r, "/admin/plans?message="+url.QueryEscape("套餐已添加"), http.StatusSeeOther)
}

func planEditData(plan domain.PaymentPlan) admin.PlanEditData {
	return admin.PlanEditData{
		Plan:         plan,
		Name:         plan.Name,
		DurationDays: strconv.Itoa(plan.DurationDays),
		Price:        fmt.Sprintf("%d.%02d", plan.PriceFen/100, plan.PriceFen%100),
		Note:         plan.Note,
	}
}

func (s *Server) renderPlanEdit(w http.ResponseWriter, r *http.Request, status int, errorMessage string, data admin.PlanEditData) {
	s.templates.RenderStatus(w, "plan-edit", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: errorMessage, PlanEdit: data}, status)
}

func (s *Server) planEdit(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := accountID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	plan, err := s.payments.FindPlan(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "load payment plan", http.StatusInternalServerError)
		return
	}
	s.renderPlanEdit(w, r, http.StatusOK, "", planEditData(plan))
}

func (s *Server) planUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := accountID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	plan, err := s.payments.FindPlan(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "load payment plan", http.StatusInternalServerError)
		return
	}
	form := admin.PlanEditData{
		Plan:         plan,
		Name:         r.Form.Get("name"),
		DurationDays: strings.TrimSpace(r.Form.Get("duration_days")),
		Price:        strings.TrimSpace(r.Form.Get("price")),
		Note:         r.Form.Get("note"),
		Submitted:    true,
	}
	price, priceErr := parsePriceFen(form.Price)
	days, daysErr := strconv.Atoi(form.DurationDays)
	if priceErr != nil {
		err = priceErr
	} else if daysErr != nil {
		err = daysErr
	}
	if err == nil {
		_, err = s.payments.UpdatePlan(r.Context(), id, domain.UpdatePaymentPlanInput{Name: form.Name, DurationDays: days, PriceFen: price, Note: form.Note, SortOrder: plan.SortOrder})
	}
	if err != nil {
		s.renderPlanEdit(w, r, http.StatusBadRequest, "保存方案失败："+err.Error(), form)
		return
	}
	http.Redirect(w, r, "/admin/plans?message="+url.QueryEscape("方案已保存"), http.StatusSeeOther)
}

func (s *Server) planToggle(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := accountID(r)
	enabled := r.Form.Get("enabled") == "true"
	if err == nil {
		err = s.payments.SetPlanEnabled(r.Context(), id, enabled)
	}
	if err != nil {
		s.renderPlans(w, r, http.StatusBadRequest, "更新套餐状态失败", "")
		return
	}
	http.Redirect(w, r, "/admin/plans?message="+url.QueryEscape("套餐状态已更新"), http.StatusSeeOther)
}

func (s *Server) planDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := accountID(r)
	if err == nil {
		err = s.payments.DeletePlan(r.Context(), id)
	}
	if err != nil {
		message := "删除方案失败"
		if errors.Is(err, domain.ErrPaymentPlanInUse) {
			message = "该方案已有支付订单，不能删除；如不再销售，请使用下架"
		} else if errors.Is(err, sql.ErrNoRows) {
			message = "方案不存在或已经删除"
		}
		s.renderPlans(w, r, http.StatusBadRequest, message, "")
		return
	}
	http.Redirect(w, r, "/admin/plans?message="+url.QueryEscape("方案已删除"), http.StatusSeeOther)
}

func parsePriceFen(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "-") {
		return 0, errors.New("价格必须大于 0")
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("价格格式无效")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 2 || (fraction != "" && strings.Trim(fraction, "0123456789") != "") || strings.Trim(parts[0], "0123456789") != "" {
		return 0, errors.New("价格最多保留两位小数")
	}
	if len(fraction) == 0 {
		fraction = "00"
	} else if len(fraction) == 1 {
		fraction += "0"
	}
	whole, err := strconv.Atoi(parts[0])
	if err != nil || whole < 0 {
		return 0, errors.New("价格格式无效")
	}
	cents, err := strconv.Atoi(fraction)
	if err != nil {
		return 0, errors.New("价格格式无效")
	}
	price := whole*100 + cents
	if price < 1 {
		return 0, errors.New("价格必须大于 0")
	}
	return price, nil
}
