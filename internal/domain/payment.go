package domain

import (
	"errors"
	"time"
)

// PaymentPlan is an administrator-configured sale option. Prices are integer
// CNY fen and duration is kept in minutes for exact subscription arithmetic.
type PaymentPlan struct {
	ID              int64
	Kind            string
	Name            string
	DurationDays    int
	DurationMinutes int
	PriceFen        int
	Note            string
	Enabled         bool
	SortOrder       int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// PaymentOrder is the local source of truth for a payment-center checkout and
// its fulfillment. Plan fields are copied as a snapshot at order creation.
type PaymentOrder struct {
	ID                 int64
	PublicToken        string
	MerchantOrderNo    string
	Kind               string
	PlanID             int64
	PlanName           string
	AccountID          *int64
	AccountUsername    string
	BuyerInfo          string
	DurationDays       int
	DurationMinutes    int
	AmountFen          int
	Currency           string
	PaymentStatus      string
	FulfillmentStatus  string
	ProviderStatus     string
	PaymentURL         string
	PaymentMemo        string
	ProviderPaymentKey string
	InviteID           *int64
	ActivationCode     string
	FailureReason      string
	ExpiresAt          time.Time
	PaidAt             *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type CreatePaymentPlanInput struct {
	Kind            string
	Name            string
	DurationDays    int
	DurationMinutes int
	PriceFen        int
	Note            string
	SortOrder       int
}

type UpdatePaymentPlanInput struct {
	Name            string
	DurationDays    int
	DurationMinutes int
	PriceFen        int
	Note            string
	SortOrder       int
}

type CreatePaymentOrderInput struct {
	PublicToken     string
	MerchantOrderNo string
	Kind            string
	PlanID          int64
	PlanName        string
	AccountID       *int64
	AccountUsername string
	BuyerInfo       string
	DurationDays    int
	DurationMinutes int
	AmountFen       int
	Currency        string
	ExpiresAt       time.Time
	Now             time.Time
}

// PaymentOrderFilter controls the administrator order search. Page numbers are 1-based.
type PaymentOrderFilter struct {
	Query    string
	Status   string
	Kind     string
	Page     int
	PageSize int
}

// PaymentOrderPage is a bounded, auditable slice of payment orders plus totals
// for the current filter.
type PaymentOrderPage struct {
	Orders        []PaymentOrder
	Total         int
	PaidCount     int
	PaidAmountFen int
	Page          int
	PageSize      int
	TotalPages    int
}

type UpdatePaymentProviderInput struct {
	OrderID        int64
	ProviderStatus string
	PaymentURL     string
	PaymentMemo    string
	ExpiresAt      time.Time
	Now            time.Time
}

type FulfillPaymentOrderInput struct {
	OrderID              int64
	EventID              string
	EventType            string
	AmountFen            int
	Currency             string
	ProviderPaymentKey   string
	PayloadHash          string
	PaidAt               time.Time
	Now                  time.Time
	ActivationCode       string
	ActivationCodeHash   string
	ActivationCodePrefix string
}

var ErrPaymentPlanInUse = errors.New("payment plan is referenced by payment orders")
