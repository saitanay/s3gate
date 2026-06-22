package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"s3gate/db"
)

const cashfreeBaseURL = "https://api.cashfree.com/pg"

type cashfreeOrderRequest struct {
	OrderID         string            `json:"order_id"`
	OrderAmount     float64           `json:"order_amount"`
	OrderCurrency   string            `json:"order_currency"`
	CustomerDetails cashfreeCustomer  `json:"customer_details"`
	OrderMeta       cashfreeOrderMeta `json:"order_meta"`
	OrderNote       string            `json:"order_note,omitempty"`
	OrderTags       map[string]string `json:"order_tags,omitempty"`
}

type cashfreeCustomer struct {
	CustomerID    string `json:"customer_id"`
	CustomerEmail string `json:"customer_email,omitempty"`
	CustomerPhone string `json:"customer_phone"`
}

type cashfreeOrderMeta struct {
	ReturnURL      string `json:"return_url"`
	NotifyURL      string `json:"notify_url,omitempty"`
	PaymentMethods string `json:"payment_methods,omitempty"`
}

type cashfreeOrderResponse struct {
	CfOrderID        string `json:"cf_order_id"`
	OrderID          string `json:"order_id"`
	PaymentSessionID string `json:"payment_session_id"`
	OrderStatus      string `json:"order_status"`
}

// HandleRecharge creates a Cashfree order and renders checkout page
func HandleRecharge(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
		return
	}

	user := GetCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	amountPaise, _ := strconv.ParseInt(r.FormValue("amount"), 10, 64)
	if amountPaise != 900 && amountPaise != 9900 && amountPaise != 99900 {
		http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
		return
	}

	// ₹999 gives ₹1188 credits (12 months)
	creditPaise := amountPaise
	if amountPaise == 99900 {
		creditPaise = 118800
	}

	baseURL := os.Getenv("BASE_URL")
	appID := os.Getenv("CASHFREE_APP_ID")
	secretKey := os.Getenv("CASHFREE_SECRET_KEY")

	orderID := fmt.Sprintf("bc_%s_%d", user.ID[:8], time.Now().UnixMilli())
	amountRupees := float64(amountPaise) / 100.0

	reqBody := cashfreeOrderRequest{
		OrderID:       orderID,
		OrderAmount:   amountRupees,
		OrderCurrency: "INR",
		CustomerDetails: cashfreeCustomer{
			CustomerID:    user.ID,
			CustomerEmail: user.Email,
			CustomerPhone: "9999999999",
		},
		OrderMeta: cashfreeOrderMeta{
			ReturnURL:      fmt.Sprintf("%s/dashboard/billing/callback?order_id={order_id}", baseURL),
			NotifyURL:      fmt.Sprintf("%s/webhooks/cashfree", baseURL),
			PaymentMethods: "cc,dc,upi,nb",
		},
		OrderNote: "BucketCheap Wallet Recharge",
		OrderTags: map[string]string{
			"user_id":      user.ID,
			"credit_paise": fmt.Sprintf("%d", creditPaise),
		},
	}

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", cashfreeBaseURL+"/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-id", appID)
	req.Header.Set("x-client-secret", secretKey)
	req.Header.Set("x-api-version", "2025-01-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("ERROR Cashfree request: %v", err)
		http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		log.Printf("ERROR Cashfree response %d: %s", resp.StatusCode, string(respBody))
		http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
		return
	}

	var cfResp cashfreeOrderResponse
	json.Unmarshal(respBody, &cfResp)

	if cfResp.PaymentSessionID != "" {
		render(w, "checkout.html", map[string]any{"PaymentSessionID": cfResp.PaymentSessionID})
	} else {
		log.Printf("ERROR Cashfree no payment_session_id: %s", string(respBody))
		http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
	}
}

// HandleBillingCallback handles return from Cashfree after payment
func HandleBillingCallback(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
}

// HandleCashfreeWebhook handles webhook notifications from Cashfree
func HandleCashfreeWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Printf("Cashfree webhook: %s", string(body))

	var payload struct {
		Type string `json:"type"`
		Data struct {
			Order struct {
				OrderID     string            `json:"order_id"`
				OrderAmount float64           `json:"order_amount"`
				OrderTags   map[string]string `json:"order_tags"`
			} `json:"order"`
			Payment struct {
				CfPaymentID   json.Number `json:"cf_payment_id"`
				PaymentStatus string      `json:"payment_status"`
				PaymentAmount float64     `json:"payment_amount"`
			} `json:"payment"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("ERROR parsing Cashfree webhook: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if payload.Type != "PAYMENT_SUCCESS_WEBHOOK" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if payload.Data.Payment.PaymentStatus != "SUCCESS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	userID := payload.Data.Order.OrderTags["user_id"]
	creditStr := payload.Data.Order.OrderTags["credit_paise"]
	orderID := payload.Data.Order.OrderID

	if userID != "" && creditStr != "" && orderID != "" {
		creditPaise, _ := strconv.ParseInt(creditStr, 10, 64)
		if creditPaise > 0 {
			err := db.CreditWallet(userID, creditPaise,
				fmt.Sprintf("Recharge ₹%d.%02d", creditPaise/100, creditPaise%100),
				orderID)
			if err != nil {
				log.Printf("Webhook: credit skipped (likely duplicate) user=%s order=%s err=%v", userID, orderID, err)
			} else {
				log.Printf("Webhook: wallet credited user=%s credits=₹%d.%02d order=%s",
					userID, creditPaise/100, creditPaise%100, orderID)
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}
