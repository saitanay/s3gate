package web

import (
	"fmt"
	"log"
	"net/smtp"
	"os"
)

func SendMagicLink(email, token string) error {
	host := os.Getenv("SMTP_HOST")
	port := os.Getenv("SMTP_PORT")
	user := os.Getenv("SMTP_USER")
	pass := os.Getenv("SMTP_PASSWORD")
	from := os.Getenv("SMTP_FROM")
	fromName := os.Getenv("SMTP_FROM_NAME")
	if fromName == "" {
		fromName = "BucketCheap"
	}
	baseURL := os.Getenv("BASE_URL")

	link := fmt.Sprintf("%s/auth/verify?token=%s", baseURL, token)

	subject := "Your sign-in link for BucketCheap"
	body := fmt.Sprintf(`Hey there!

Here's your magic link to sign in:

%s

This link is valid for 15 minutes. If you didn't request this, just ignore it.

Welcome to fair-priced S3 storage. 1 TB, unlimited bandwidth, ₹99/month — built for Indian developers who deserve better than AWS pricing.

Cheers,
Vishwak @ BucketCheap
https://bucketcheap.com`, link)

	msg := fmt.Sprintf("From: %s <%s>\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		fromName, from, email, subject, body)

	auth := smtp.PlainAuth("", user, pass, host)
	addr := fmt.Sprintf("%s:%s", host, port)

	err := smtp.SendMail(addr, auth, from, []string{email}, []byte(msg))
	if err != nil {
		log.Printf("ERROR sending email to %s: %v", email, err)
		return err
	}

	log.Printf("Magic link sent to %s", email)
	return nil
}
