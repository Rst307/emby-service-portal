package admin

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/payments"
	"github.com/Rst307/emby-service-portal/internal/requests"
	"github.com/Rst307/emby-service-portal/internal/tmdb"
	"github.com/Rst307/emby-service-portal/internal/update"
)

//go:embed templates/*.html
var files embed.FS

type ViewData struct {
	CSRFToken         string
	Error             string
	Message           string
	PlanEdit          PlanEditData
	Accounts          []domain.Account
	Account           domain.Account
	Invites           []domain.InviteCode
	NewInviteCode     string
	AccountCount      int
	ActiveCount       int
	DisabledCount     int
	ExpiredCount      int
	InviteCount       int
	PaymentOrderCount int
	PaymentPaidCount  int
	PaymentRevenueFen int
	TimeZone          string
	TimeZoneNow       string
	TimeZoneOptions   []TimeZoneOption
	PaymentSettings   payments.PaymentSettings
	ActivationPlans   []domain.PaymentPlan
	RenewalPlans      []domain.PaymentPlan
	PaymentOrder      domain.PaymentOrder
	PaymentOrders     []domain.PaymentOrder
	OrderTotal        int
	OrderPaidCount    int
	OrderPaidFen      int
	OrderPage         int
	OrderPageSize     int
	OrderTotalPages   int
	OrderQuery        string
	OrderFilterQuery  string
	OrderStatus       string
	OrderKind         string
	BuyerInfo         string
	// 求剧（media requests）
	PortalActive       string
	SearchResults      []requests.SearchItem
	RequestQuery       string
	TmdbConfigured     bool
	RequestEnabled     bool
	MediaRequests      []domain.MediaRequest
	RequestTotal       int
	RequestPending     int
	RequestFulfilled   int
	RequestStatus      string
	RequestPage        int
	RequestPageSize    int
	RequestTotalPages  int
	RequestFilterQuery string
	// 系统更新（self-update）
	Update update.State
}

// PlanEditData contains the editable catalog fields and the original plan metadata.
type PlanEditData struct {
	Plan         domain.PaymentPlan
	Name         string
	DurationDays string
	Price        string
	Note         string
	Submitted    bool
}

// TimeZoneOption is a selectable time zone for the settings page.
type TimeZoneOption struct {
	Name  string
	Label string
}

type Templates struct {
	templates *template.Template
	// location is swapped atomically when the display time zone changes.
	location atomic.Pointer[time.Location]
}

func NewTemplates(location *time.Location) (*Templates, error) {
	if location == nil {
		location = time.UTC
	}
	templates := &Templates{}
	templates.location.Store(location)
	functions := template.FuncMap{
		"formatTime": func(value time.Time, layout string) string { return value.In(templates.location.Load()).Format(layout) },
		"timeZone":   func() string { return templates.location.Load().String() },
		"statusLabel": func(status string) string {
			switch status {
			case "active":
				return "活跃"
			case "disabled":
				return "已禁用"
			case "expired":
				return "已过期"
			default:
				return status
			}
		},
		"formatDuration": func(minutes int) string {
			switch {
			case minutes <= 0:
				return "0 分钟"
			case minutes%1440 == 0:
				return fmt.Sprintf("%d 天", minutes/1440)
			case minutes%60 == 0:
				return fmt.Sprintf("%d 小时", minutes/60)
			default:
				return fmt.Sprintf("%d 分钟", minutes)
			}
		},
		"formatMoney": func(fen int) string {
			if fen < 0 {
				return "¥-" + fmt.Sprintf("%d.%02d", (-fen)/100, (-fen)%100)
			}
			return fmt.Sprintf("¥%d.%02d", fen/100, fen%100)
		},
		"formatBytes": func(size int64) string {
			if size <= 0 {
				return "未知大小"
			}
			const unit = 1024
			if size < unit {
				return fmt.Sprintf("%d B", size)
			}
			div, exp := int64(unit), 0
			for n := size / unit; n >= unit; n /= unit {
				div *= unit
				exp++
			}
			return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
		},
		"shortDuration": func(d time.Duration) string {
			if d <= 0 {
				return "已关闭"
			}
			if d%time.Hour == 0 {
				return fmt.Sprintf("%d 小时", int(d/time.Hour))
			}
			if d%time.Minute == 0 {
				return fmt.Sprintf("%d 分钟", int(d/time.Minute))
			}
			return d.String()
		},
		"formatPriceInput": func(fen int) string {
			if fen < 0 {
				return fmt.Sprintf("-%d.%02d", (-fen)/100, (-fen)%100)
			}
			return fmt.Sprintf("%d.%02d", fen/100, fen%100)
		},
		"planKindLabel": func(kind string) string {
			if kind == "activation" {
				return "激活码"
			}
			if kind == "renewal" {
				return "订阅续费"
			}
			return kind
		},
		"paymentStatusLabel": func(status string) string {
			switch status {
			case "pending":
				return "等待付款"
			case "paid":
				return "已付款"
			case "expired":
				return "已过期"
			case "canceled":
				return "已取消"
			case "failed":
				return "处理失败"
			default:
				return status
			}
		},
		"requestStatusLabel": func(status string) string {
			switch status {
			case "pending":
				return "待处理"
			case "fulfilled":
				return "已入库"
			case "rejected":
				return "已驳回"
			default:
				return status
			}
		},
		"requestKindLabel": func(kind string) string {
			if kind == domain.MediaRequestKindMissing {
				return "催更"
			}
			return "求剧"
		},
		"mediaTypeLabel": func(mediaType string) string {
			if mediaType == "tv" {
				return "剧集"
			}
			return "电影"
		},
		"posterURL": func(path string) string { return tmdb.PosterURL(path) },
		"yearOnly": func(date string) string {
			date = strings.TrimSpace(date)
			if len(date) >= 4 {
				return date[:4]
			}
			return date
		},
		"firstRune": func(value string) string {
			for _, r := range value {
				return string(r)
			}
			return "?"
		},
		"adminPageLabel": func(active string) string {
			switch active {
			case "dashboard":
				return "工作台"
			case "accounts":
				return "账号管理"
			case "invites":
				return "邀请码"
			case "plans":
				return "售卖方案"
			case "orders":
				return "支付订单"
			case "requests":
				return "求剧管理"
			case "settings":
				return "系统设置"
			default:
				return active
			}
		},
		"orderBuyer": func(order domain.PaymentOrder) string {
			if order.BuyerInfo != "" {
				return order.BuyerInfo
			}
			if order.AccountUsername != "" {
				return order.AccountUsername
			}
			return "未填写"
		},
		"queryEscape": url.QueryEscape,
		"add":         func(a, b int) int { return a + b },
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of arguments")
			}
			result := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key %v is not a string", values[i])
				}
				result[key] = values[i+1]
			}
			return result, nil
		},
	}
	t, err := template.New("pages").Funcs(functions).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	templates.templates = t
	return templates, nil
}

// SetLocation swaps the display time zone used by all templates.
func (t *Templates) SetLocation(location *time.Location) {
	if location != nil {
		t.location.Store(location)
	}
}

func (t *Templates) Render(w http.ResponseWriter, name string, data ViewData) {
	t.RenderStatus(w, name, data, http.StatusOK)
}

func (t *Templates) RenderStatus(w http.ResponseWriter, name string, data ViewData, status int) {
	var body bytes.Buffer
	if err := t.templates.ExecuteTemplate(&body, name, data); err != nil {
		http.Error(w, "render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}
