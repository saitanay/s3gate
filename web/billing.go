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

const dodopayBaseURL = "https://api.dodopayments.com/v1/checkout/sessions"

// HandleRecharge creates a DodoPay checkout session and redirects user
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
	if amountPaise != 9900 && amountPaise != 99900 {
		http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
		return
	}

	// ₹999 gives ₹1188 credits (12 months)
	creditPaise := amountPaise
	if amountPaise == 99900 {
		creditPaise = 118800
	}

	baseURL := os.Getenv("BASE_URL")
	apiKey := os.Getenv("DODOPAY_API_KEY")

	// Pick product ID based on amount
	productID := os.Getenv("DODOPAY_PRODUCT_99")
	if amountPaise == 99900 {
		productID = os.Getenv("DODOPAY_PRODUCT_999")
	}

	reqBody := map[string]any{
		"customer": map[string]any{
			"email": user.Email,
		},
		"billing": map[string]any{
			"country": "IN",
		},
		"product_cart": []map[string]any{
			{
				"product_id": productID,
				"quantity":   1,
			},
		},
		"currency":   "INR",
		"return_url": fmt.Sprintf("%s/dashboard/billing/callback", baseURL),
		"metadata": map[string]string{
			"user_id":       user.ID,
			"credit_paise":  fmt.Sprintf("%d", creditPaise),
			"order_time":    fmt.Sprintf("%d", time.Now().UnixMilli()),
		},
	}

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", dodopayBaseURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("ERROR DodoPay request: %v", err)
		http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		log.Printf("ERROR DodoPay response %d: %s", resp.StatusCode, string(respBody))
		http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
		return
	}

	var result map[string]any
	json.Unmarshal(respBody, &result)

	// Try checkout_url or url field
	checkoutURL := ""
	if u, ok := result["url"].(string); ok && u != "" {
		checkoutURL = u
	} else if u, ok := result["checkout_url"].(string); ok && u != "" {
		checkoutURL = u
	}

	if checkoutURL != "" {
		http.Redirect(w, r, checkoutURL, http.StatusSeeOther)
	} else {
		log.Printf("ERROR DodoPay no checkout URL in response: %s", string(respBody))
		http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
	}
}

// HandleBillingCallback handles return from DodoPay after payment
func HandleBillingCallback(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
}

// HandleDodopayWebhook handles webhook notifications from DodoPay
func HandleDodopayWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Printf("DodoPay webhook: %s", string(body))

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Extract payment data
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		// Try top-level fields
		data = payload
	}

	status, _ := data["status"].(string)
	metadata, _ := data["metadata"].(map[string]any)
	if metadata == nil {
		if order, ok := data["order"].(map[string]any); ok {
			metadata, _ = order["metadata"].(map[string]any)
		}
	}

	paymentID := ""
	if pid, ok := data["payment_id"].(string); ok {
		paymentID = pid
	} else if pid, ok := data["id"].(string); ok {
		paymentID = pid
	}

	if (status == "succeeded" || status == "paid" || status == "PAID") && metadata != nil {
		userID, _ := metadata["user_id"].(string)
		creditStr, _ := metadata["credit_paise"].(string)

		if userID != "" && creditStr != "" {
			creditPaise, _ := strconv.ParseInt(creditStr, 10, 64)
			if creditPaise > 0 && paymentID != "" {
				// Dedup check
				var exists int
				db.DB.QueryRow(`SELECT COUNT(*) FROM transactions WHERE dodopay_ref = ?`, paymentID).Scan(&exists)
				if exists == 0 {
					db.CreditWallet(userID, creditPaise,
						fmt.Sprintf("Recharge ₹%d.%02d", creditPaise/100, creditPaise%100),
						paymentID)
					log.Printf("Webhook: wallet credited user=%s credits=₹%d.%02d payment=%s",
						userID, creditPaise/100, creditPaise%100, paymentID)
				}
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}
