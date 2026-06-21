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

	"s3gate/db"
)

const dodopayBaseURL = "https://live.dodopayments.com"

// DodoPay checkout session request
type dodopayCheckoutRequest struct {
	ProductCart   []dodopayProduct  `json:"product_cart"`
	Customer     dodopayCustomer   `json:"customer"`
	ReturnURL    string            `json:"return_url"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	FeatureFlags *dodopayFlags     `json:"feature_flags,omitempty"`
}

type dodopayProduct struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

type dodopayCustomer struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type dodopayFlags struct {
	AlwaysCreateNewCustomer bool `json:"always_create_new_customer"`
}

type dodopayCheckoutResponse struct {
	SessionID   string `json:"session_id"`
	CheckoutURL string `json:"checkout_url"`
}

// HandleRecharge creates a DodoPay payment link and redirects user
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
	if amountPaise < 99900 { // Min ₹999
		http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
		return
	}

	baseURL := os.Getenv("BASE_URL")
	apiKey := os.Getenv("DODOPAY_API_KEY")
	productID := os.Getenv("DODOPAY_PRODUCT_ID")

	reqBody := dodopayCheckoutRequest{
		ProductCart: []dodopayProduct{
			{ProductID: productID, Quantity: 1},
		},
		Customer: dodopayCustomer{
			Email: user.Email,
		},
		FeatureFlags: &dodopayFlags{AlwaysCreateNewCustomer: true},
		ReturnURL: fmt.Sprintf("%s/dashboard/billing/callback?user_id=%s&amount=%d", baseURL, user.ID, amountPaise),
		Metadata: map[string]string{
			"user_id":      user.ID,
			"amount_paise": fmt.Sprintf("%d", amountPaise),
		},
	}

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", dodopayBaseURL+"/checkouts", bytes.NewReader(body))
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
	if resp.StatusCode != 200 {
		log.Printf("ERROR DodoPay response %d: %s", resp.StatusCode, string(respBody))
		http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
		return
	}

	var dodopayResp dodopayCheckoutResponse
	json.Unmarshal(respBody, &dodopayResp)

	if dodopayResp.CheckoutURL != "" {
		http.Redirect(w, r, dodopayResp.CheckoutURL, http.StatusSeeOther)
	} else {
		log.Printf("ERROR DodoPay no checkout_url in response: %s", string(respBody))
		http.Redirect(w, r, "/dashboard/billing", http.StatusSeeOther)
	}
}

// HandleBillingCallback handles return from DodoPay after payment
func HandleBillingCallback(w http.ResponseWriter, r *http.Request) {
	// Don't credit here — webhook handles it (with dedup).
	// Just redirect user back to billing page.
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

	// Parse webhook payload
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Printf("DodoPay webhook: %s", string(body))

	// Extract payment data
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	status, _ := data["status"].(string)
	metadata, _ := data["metadata"].(map[string]any)

	if status == "succeeded" && metadata != nil {
		userID, _ := metadata["user_id"].(string)
		amountStr, _ := metadata["amount_paise"].(string)
		paymentID, _ := data["payment_id"].(string)

		if userID != "" && amountStr != "" {
			amountPaise, _ := strconv.ParseInt(amountStr, 10, 64)
			if amountPaise > 0 {
				// Check for duplicate
				var exists int
				db.DB.QueryRow(`SELECT COUNT(*) FROM transactions WHERE dodopay_ref = ?`, paymentID).Scan(&exists)
				if exists == 0 {
					db.CreditWallet(userID, amountPaise,
						fmt.Sprintf("Recharge ₹%d.%02d", amountPaise/100, amountPaise%100),
						paymentID)
					log.Printf("Webhook: wallet credited user=%s amount=%d payment=%s", userID, amountPaise, paymentID)
				}
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}
